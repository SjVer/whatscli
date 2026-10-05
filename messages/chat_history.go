package messages

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/normen/whatscli/config"
	"go.mau.fi/whatsmeow/types"
)

// historyTimeout is how long to wait for the phone to answer a request.
const historyTimeout = 30 * time.Second

// historyRequests tracks the chats whose messages were requested from the phone.
type historyRequests struct {
	lock    sync.Mutex
	pending map[string]*time.Timer
	// chats that were loaded from the phone since whatscli started
	loaded map[string]bool
}

// historyNotice is the key of the notices about loading a chat from the phone
const historyNotice = "history"

// errNoKnownMessage is returned by LoadChat and RequestChatHistory for a chat
// without messages in memory, as the phone only answers for a message it knows
var errNoKnownMessage = errors.New("no message of this chat is known yet, it loads once a message arrives or after /relink")

// loadChatOnce requests the messages of a chat the first time it is opened
// after starting whatscli, and again next time if that failed, see LoadChat.
func (sm *SessionManager) loadChatOnce(chatID string) {
	sm.history.lock.Lock()
	loaded := sm.history.loaded[chatID]
	sm.history.lock.Unlock()
	if loaded {
		return
	}
	err := sm.LoadChat(chatID)
	if errors.Is(err, errNoKnownMessage) {
		return // e.g. a contact that was never written to
	} else if err != nil {
		sm.uiHandler.SetNotice(chatID, historyNotice, err.Error())
		return
	}
	sm.history.lock.Lock()
	if sm.history.loaded == nil {
		sm.history.loaded = make(map[string]bool)
	}
	sm.history.loaded[chatID] = true
	sm.history.lock.Unlock()
}

// RequestChatHistory asks the phone for older messages of a chat, before the
// oldest one in memory, e.g. for the backlog key. The phone doesn't answer
// requests that don't refer to a message it knows.
func (sm *SessionManager) RequestChatHistory(chatID string) error {
	oldest, ok := sm.db.GetOldestMessage(chatID)
	if !ok {
		return errNoKnownMessage
	}
	return sm.requestHistory(chatID, oldest, historyCount())
}

// LoadChat asks the phone for the messages of a chat before the newest one:
// the saved ones again, as only the phone knows how far the user's messages
// got, see MessageStatus, and as many older ones. The newest message itself
// isn't in the answer, so its status only comes from receipts. Its count grows
// with the messages in memory, which are about historyCount when a chat is
// first opened, see loadChatOnce.
func (sm *SessionManager) LoadChat(chatID string) error {
	msgs := sm.db.GetMessages(chatID)
	if len(msgs) == 0 {
		return errNoKnownMessage
	}
	return sm.requestHistory(chatID, msgs[len(msgs)-1], len(msgs)-1+historyCount())
}

// requestHistory asks the phone for count messages of a chat before a known message.
func (sm *SessionManager) requestHistory(chatID string, known Message, count int) error {
	client, err := sm.connectedClient()
	if err != nil {
		return err
	}
	jid, err := sm.phoneChatJID(chatID)
	if err != nil {
		return err
	}
	anchor := &types.MessageInfo{
		MessageSource: types.MessageSource{Chat: jid, IsFromMe: known.FromMe, IsGroup: jid.Server == types.GroupServer},
		ID:            types.MessageID(known.Id),
		Timestamp:     time.Unix(int64(known.Timestamp), 0),
	}
	if sender, err := types.ParseJID(known.SenderId); err == nil {
		anchor.Sender = sender
	}

	sm.history.lock.Lock()
	if sm.history.pending == nil {
		sm.history.pending = make(map[string]*time.Timer)
	}
	if _, ok := sm.history.pending[chatID]; ok {
		sm.history.lock.Unlock()
		return nil // already requested
	}
	var timer *time.Timer
	timer = time.AfterFunc(historyTimeout, func() {
		sm.history.lock.Lock()
		current := sm.history.pending[chatID] == timer
		if current {
			// loaded again when the chat is opened next time
			delete(sm.history.loaded, chatID)
		}
		sm.history.lock.Unlock()
		if current && sm.finishHistoryRequest(chatID) {
			sm.uiHandler.SetNotice(chatID, historyNotice, "Your phone didn't send older messages, is it online?")
		}
	})
	sm.history.pending[chatID] = timer
	sm.history.lock.Unlock()
	sm.updateActivity()
	sm.uiHandler.SetNotice(chatID, historyNotice, "")

	sm.logDebug("Requesting %d messages of %s before message %s from %s", count, chatID, anchor.ID, anchor.Timestamp)
	req := client.BuildHistorySyncRequest(anchor, count)
	if _, err = client.SendPeerMessage(context.Background(), req); err != nil {
		sm.finishHistoryRequest(chatID)
		return fmt.Errorf("failed to request messages from your phone: %v", err)
	}
	return nil
}

// resetHistoryRequests forgets the requested and loaded chats, whose messages are removed.
func (sm *SessionManager) resetHistoryRequests() {
	sm.history.lock.Lock()
	for _, timer := range sm.history.pending {
		timer.Stop()
	}
	sm.history.pending = nil
	sm.history.loaded = nil
	sm.history.lock.Unlock()
	sm.updateActivity()
}

// phoneChatJID returns the JID the phone knows a chat under: one-to-one chats
// under their LID, if it is known.
func (sm *SessionManager) phoneChatJID(chatID string) (types.JID, error) {
	client := sm.client()
	jid, err := types.ParseJID(chatID)
	if err != nil {
		return jid, fmt.Errorf("invalid chat: %v", err)
	}
	if jid.Server == types.DefaultUserServer && client != nil && client.Store.LIDs != nil {
		if lid, err := client.Store.LIDs.GetLIDForPN(context.Background(), jid); err == nil && !lid.IsEmpty() {
			return lid, nil
		}
	}
	return jid, nil
}

// historyCount returns how many messages are loaded from the phone at a time,
// and saved of each chat.
func historyCount() int {
	if count := config.Config.General.BacklogMsgQuantity; count > 0 {
		return count
	}
	return 50 // recommended by whatsmeow
}

// finishAllHistoryRequests marks all requests as answered.
func (sm *SessionManager) finishAllHistoryRequests() {
	sm.history.lock.Lock()
	chatIDs := make([]string, 0, len(sm.history.pending))
	for chatID := range sm.history.pending {
		chatIDs = append(chatIDs, chatID)
	}
	sm.history.lock.Unlock()
	for _, chatID := range chatIDs {
		sm.finishHistoryRequest(chatID)
	}
}

// HistoryPending returns whether messages of a chat were requested and not received yet.
func (sm *SessionManager) HistoryPending(chatID string) bool {
	sm.history.lock.Lock()
	defer sm.history.lock.Unlock()
	_, ok := sm.history.pending[chatID]
	return ok
}

// finishHistoryRequest marks a request as answered, and returns whether it was pending.
func (sm *SessionManager) finishHistoryRequest(chatID string) bool {
	sm.history.lock.Lock()
	timer, ok := sm.history.pending[chatID]
	if ok {
		timer.Stop()
		delete(sm.history.pending, chatID)
	}
	sm.history.lock.Unlock()
	if ok {
		sm.updateActivity()
	}
	return ok
}

// updateActivity shows in the status bar whether messages are being loaded or received.
func (sm *SessionManager) updateActivity() {
	sm.history.lock.Lock()
	activity := ""
	if count := len(sm.history.pending); count == 1 {
		activity = "loading messages..."
	} else if count > 1 {
		activity = fmt.Sprintf("loading messages of %d chats...", count)
	}
	sm.history.lock.Unlock()
	if count := sm.offlineMessages.Load(); count > 0 && activity == "" {
		activity = fmt.Sprintf("receiving %d messages from while offline...", count)
	}

	sm.statusLock.Lock()
	sm.statusInfo.Activity = activity
	status := sm.statusInfo
	sm.statusLock.Unlock()
	sm.uiHandler.SetStatus(status)
}
