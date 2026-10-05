package messages

import (
	"context"
	"fmt"

	"go.mau.fi/whatsmeow/appstate"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

type eventHandler struct {
	sm *SessionManager
}

func (eh *eventHandler) Handle(evt interface{}) {
	switch v := evt.(type) {
	case *events.Message:
		eh.handleLiveMessage(v)
	case *events.HistorySync:
		eh.handleHistorySync(v)
	case *events.OfflineSyncPreview:
		eh.sm.startOfflineSync(v.Messages)
		if v.Total > 0 {
			go eh.sm.requestOfflineBatches()
		}
	case *events.OfflineSyncCompleted:
		eh.sm.finishOfflineSync()
		signal(eh.sm.OfflineSynced, struct{}{})
	case *events.Receipt:
		if v.IsFromMe {
			eh.handleReceipt(v)
		} else {
			eh.handleStatusReceipt(v)
		}
	case *events.AppStateSyncComplete:
		// full syncs don't refresh the chat list per event, see refreshChats
		if v.Name == appstate.WAPatchCriticalUnblockLow { // the contact list
			eh.sm.addContactChats()
			eh.sm.refreshContactNames()
		}
		eh.sm.uiHandler.SetChats(eh.sm.db.GetChatIds())
		if v.Recovery {
			eh.sm.recoveryDone(v.Name)
			eh.sm.uiHandler.SetNotice("", appStateNotice, "Chat settings repaired")
		}
		if cs := eh.sm.getChatSync(); cs != nil {
			cs.onAppStateSynced(v.Name)
		}
	case *events.DeleteChat:
		eh.sm.logAppState("delete", v.JID, v.Timestamp, v.FromFullSync, "", v.Action.GetMessageRange())
		deletedAt := rangeTimestamp(v.Action.GetMessageRange())
		if deletedAt <= 0 {
			deletedAt = v.Timestamp.Unix()
		}
		eh.sm.db.SetChatDeleted(eh.sm.chatIdForJID(v.JID), deletedAt)
		eh.sm.refreshChats(v.FromFullSync)
	case *events.GroupInfo:
		// e.g. someone joined or left, see loadedMembers
		eh.sm.forgetMembers(eh.sm.chatIdForJID(v.JID))
	case *events.PairSuccess:
		// pairing sets up the device store, move the cached parts again
		if client := eh.sm.client(); client == nil {
			eh.sm.logWarn("Paired without a client")
		} else if err := eh.sm.useCacheStore(client.Store); err != nil {
			eh.sm.uiHandler.PrintError(err)
		}
	case *events.MarkChatAsRead:
		eh.sm.logAppState("read", v.JID, v.Timestamp, v.FromFullSync, fmt.Sprintf("read=%v", v.Action.GetRead()), v.Action.GetMessageRange())
		if v.Action.GetRead() {
			readUntil := rangeTimestamp(v.Action.GetMessageRange())
			if readUntil <= 0 {
				readUntil = v.Timestamp.Unix()
			}
			eh.sm.chatReadElsewhere(eh.sm.chatIdForJID(v.JID), readUntil, max(readUntil, v.Timestamp.Unix()))
		}
		eh.sm.updateChatActivity(v.JID, v.Action.GetMessageRange(), v.FromFullSync)
	case *events.Archive:
		eh.sm.logAppState("archive", v.JID, v.Timestamp, v.FromFullSync, fmt.Sprintf("archived=%v", v.Action.GetArchived()), v.Action.GetMessageRange())
		eh.sm.db.SetChatArchived(eh.sm.chatIdForJID(v.JID), v.Action.GetArchived(), rangeTimestamp(v.Action.GetMessageRange()))
		eh.sm.updateChatActivity(v.JID, v.Action.GetMessageRange(), v.FromFullSync)
	case *events.UnarchiveChatsSetting:
		eh.sm.logAppState("unarchive setting", types.EmptyJID, v.Timestamp, v.FromFullSync, fmt.Sprintf("unarchive=%v", v.Action.GetUnarchiveChats()), nil)
		eh.sm.db.SetKeepArchived(!v.Action.GetUnarchiveChats())
		eh.sm.refreshChats(v.FromFullSync)
	case *events.PushNameSetting:
		eh.sm.nameOwnChat(v.Action.GetName())
		eh.sm.uiHandler.SetChats(eh.sm.db.GetChatIds())
	case *events.Pin:
		eh.sm.logAppState("pin", v.JID, v.Timestamp, v.FromFullSync, fmt.Sprintf("pinned=%v", v.Action.GetPinned()), nil)
		eh.sm.db.SetChatPinned(eh.sm.chatIdForJID(v.JID), v.Action.GetPinned(), v.Timestamp.Unix())
		eh.sm.refreshChats(v.FromFullSync)
	case *events.Connected:
		eh.sm.sendStatus()
		eh.sm.connection.connected()
		// the open chat couldn't be loaded while disconnected
		if chatID := eh.sm.openChat(); chatID != "" {
			go eh.sm.loadChatOnce(chatID)
		}
	case *events.Disconnected:
		eh.sm.finishOfflineSync()
		eh.sm.sendStatus()
		eh.sm.connection.disconnected()
	case *events.KeepAliveTimeout:
		eh.sm.connection.unresponsive()
	case *events.KeepAliveRestored:
		eh.sm.connection.connected()
	case *events.LoggedOut:
		eh.sm.sendStatus()
		reason := v.Reason.String()
		eh.sm.uiHandler.SetNotice("", connectionNotice, "Logged out: "+reason)
		eh.sm.connection.loggedOut(reason)
	}
}

// startOfflineSync is called when WhatsApp starts delivering the messages that
// arrived while whatscli was closed. They are only shown when all arrived, see
// finishOfflineSync, as there can be thousands after a while, most of them read
// on the phone already, see handleReceipt.
func (sm *SessionManager) startOfflineSync(messages int) {
	if messages > 0 {
		sm.offlineMessages.Store(int64(messages))
		sm.updateActivity()
	}
}

// offlineBatchSize is how many of the events that arrived while whatscli was
// closed WhatsApp sends at a time, like other WhatsApp Web clients ask for
const offlineBatchSize = 100

// requestOfflineBatches asks WhatsApp to send the events that arrived while
// whatscli was closed in batches, instead of one at a time as it does unasked,
// which takes minutes after a while.
func (sm *SessionManager) requestOfflineBatches() {
	err := sm.client().DangerousInternals().SendNode(context.Background(), waBinary.Node{
		Tag:     "ib",
		Content: []waBinary.Node{{Tag: "offline_batch", Attrs: waBinary.Attrs{"count": fmt.Sprint(offlineBatchSize)}}},
	})
	if err != nil {
		sm.logWarn("Failed to request the offline messages in batches: %v", err)
	}
}

// finishOfflineSync shows the messages that arrived while whatscli was closed.
func (sm *SessionManager) finishOfflineSync() {
	if sm.offlineMessages.Swap(0) == 0 {
		return
	}
	sm.updateActivity()
	sm.uiHandler.SetChats(sm.db.GetChatIds())
	sm.showIfOpen(sm.openChat())
}

// handleReceipt marks the messages of a chat as read when they were read on the
// phone or another device.
func (eh *eventHandler) handleReceipt(evt *events.Receipt) {
	if !evt.IsFromMe || (evt.Type != types.ReceiptTypeRead && evt.Type != types.ReceiptTypeReadSelf) {
		return
	}
	// reading a message reads the ones before it
	readUntil := int64(0)
	for _, id := range evt.MessageIDs {
		if msg, ok := eh.sm.db.GetMessage(id); ok {
			readUntil = max(readUntil, int64(msg.Timestamp))
		}
	}
	if readUntil == 0 {
		// the messages weren't received yet, but were sent before they were read
		readUntil = evt.Timestamp.Unix()
	}
	// the reactions until then were seen too
	eh.sm.chatReadElsewhere(eh.sm.chatIdForJID(evt.Chat), readUntil, evt.Timestamp.Unix())
}

// chatReadElsewhere records that a chat was read on the phone or another
// device: its messages until readUntil, and the reactions given until
// seenUntil. The change is shown unless the messages from while offline are
// still arriving, see finishOfflineSync.
func (sm *SessionManager) chatReadElsewhere(chatID string, readUntil, seenUntil int64) {
	seen := sm.db.ClearUnreadReactions(chatID, seenUntil)
	read := sm.db.MarkChatReadUntil(chatID, readUntil)
	if (seen || read) && sm.offlineMessages.Load() == 0 {
		sm.uiHandler.SetChats(sm.db.GetChatIds())
		sm.showIfOpen(chatID)
	}
}

// seesChat returns whether the user is looking at a chat: it is open, and
// whatscli and its message panel or input have focus, see ChatSeen. Messages
// that arrive while it isn't stay new when the user looks again.
func (sm *SessionManager) seesChat(chatID string) bool {
	return chatID != "" && chatID == sm.openChat() && (sm.ChatSeen == nil || sm.ChatSeen())
}

// showIfOpen shows the messages of a chat again if it is open, e.g. when they
// were read
func (sm *SessionManager) showIfOpen(chatID string) {
	if chatID != "" && chatID == sm.openChat() {
		sm.uiHandler.NewScreen(chatID, sm.db.GetMessages(chatID))
		sm.describeChat(chatID) // e.g. images loaded from the phone
	}
}
