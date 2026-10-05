package messages

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/normen/whatscli/config"
	"github.com/rivo/tview"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
)

func (sm *SessionManager) loadRecentChats() {
	client, err := sm.connectedClient()
	if err != nil {
		sm.uiHandler.PrintError(err)
		signal(sm.LoginFailed, err)
		return
	}

	// Right after pairing the phone has not sent the keys for the app state yet.
	// whatsmeow syncs all of it once they arrive, see AppStateSyncComplete.
	hasAppStateKeys := sm.hasAppStateKeys()

	// Sync app state that is missing, e.g. because the cache database was deleted.
	if hasAppStateKeys {
		for _, name := range appstate.AllPatchNames {
			if name == appstate.WAPatchRegularLow {
				continue // fully synced below
			}
			sm.printAppStateError(client.FetchAppState(context.Background(), name, false, true), name)
		}
	}

	addedChats := sm.addContactChats()
	sm.mergeLIDChats()
	sm.refreshContactNames()

	groups, err := client.GetJoinedGroups(context.Background())
	if err == nil {
		for _, group := range groups {
			sm.db.AddChat(Chat{
				Id:      group.JID.String(),
				IsGroup: true,
				Name:    group.Name,
			})
			addedChats++
		}
	}

	sm.uiHandler.SetChats(sm.db.GetChatIds())
	if addedChats > 0 {
		sm.uiHandler.SetNotice("", contactsNotice, fmt.Sprintf("Loaded %d contacts and groups", addedChats))
	}

	// The app state holds the pinned and archived state of chats, and the last
	// message time of chats that were read, marked unread or archived on another
	// device. Get what changed since the last sync; the rest is saved with the
	// chats. Syncing everything again would also undo a repair by the phone, as
	// the server keeps the patches that didn't add up, see recoverAppState.
	if hasAppStateKeys {
		sm.printAppStateError(client.FetchAppState(context.Background(), appstate.WAPatchRegularLow, false, false), appstate.WAPatchRegularLow)
		sm.uiHandler.SetChats(sm.db.GetChatIds())
	}
	signal(sm.ChatsLoaded, struct{}{})
}

// hasAppStateKeys returns whether the phone has sent any keys to decrypt the app state.
func (sm *SessionManager) hasAppStateKeys() bool {
	keyID, err := sm.client().Store.AppStateKeys.GetLatestAppStateSyncKeyID(context.Background())
	return err == nil && keyID != nil
}

// printAppStateError prints an error from syncing app state, except for missing
// keys: whatsmeow requests those from the phone and syncs again when they arrive.
func (sm *SessionManager) printAppStateError(err error, name appstate.WAPatchName) {
	if err != nil && !errors.Is(err, appstate.ErrKeyNotFound) {
		sm.uiHandler.PrintError(fmt.Errorf("failed to sync %s: %v", name, err))
		sm.recoverAppState(err, name)
	}
}

// recoverAppState asks the phone for a copy of app state that failed to sync
// because it doesn't add up, a patch on the server whose hash doesn't match.
// Syncing again doesn't help then, but the phone's copy replaces it, and later
// patches work again. It is asked again when the phone didn't answer within
// recoveryWait, see recoveryDone.
func (sm *SessionManager) recoverAppState(err error, name appstate.WAPatchName) bool {
	if !errors.Is(err, appstate.ErrMismatchingLTHash) && !errors.Is(err, appstate.ErrMismatchingPatchMAC) {
		return false
	}
	sm.recoveryLock.Lock()
	if sm.recoveryRequested == nil {
		sm.recoveryRequested = make(map[appstate.WAPatchName]time.Time)
	}
	// asked again when the phone didn't answer, e.g. as it was offline
	requested := time.Since(sm.recoveryRequested[name]) < recoveryWait
	if !requested {
		sm.recoveryRequested[name] = time.Now()
	}
	sm.recoveryLock.Unlock()
	if requested {
		return true
	}
	sm.logWarn("The synced %s doesn't add up, asking the phone to repair it: %v", name, err)
	if _, err := sm.client().SendPeerMessage(context.Background(), whatsmeow.BuildAppStateRecoveryRequest(name)); err != nil {
		sm.uiHandler.PrintError(fmt.Errorf("failed to ask your phone to repair the chat settings: %v", err))
		return false
	}
	sm.uiHandler.SetNotice("", appStateNotice, "Asked your phone for a fresh copy of the chat settings, as the synced ones don't add up")
	return true
}

// rangeTimestamp returns the last message time in seconds of an app state message range.
func rangeTimestamp(msgRange *waSyncAction.SyncActionMessageRange) int64 {
	timestamp := msgRange.GetLastMessageTimestamp()
	if timestamp > 1e12 { // milliseconds
		timestamp /= 1000
	}
	return timestamp
}

// updateChatActivity records a chat's last message time from an app state event.
func (sm *SessionManager) updateChatActivity(jid types.JID, msgRange *waSyncAction.SyncActionMessageRange, fromFullSync bool) {
	timestamp := rangeTimestamp(msgRange)
	if timestamp > 0 {
		sm.db.UpdateChatLastMessage(sm.chatIdForJID(jid), timestamp)
	}
	sm.refreshChats(fromFullSync)
}

// refreshChats shows the changed chat list after an app state event. Events of a
// full sync are followed by one refresh when it completes, see AppStateSyncComplete.
func (sm *SessionManager) refreshChats(fromFullSync bool) {
	if !fromFullSync {
		sm.uiHandler.SetChats(sm.db.GetChatIds())
	}
}

func (sm *SessionManager) markCurrentChatRead() {
	if sm.openChat() == "" {
		sm.printCommandUsage("read", "-> only works in a chat")
		return
	}
	count, err := sm.markChatRead(sm.openChat())
	if err != nil {
		sm.uiHandler.PrintError(err)
	} else if count == 0 {
		sm.uiHandler.PrintText("No unread messages in current chat")
	}
}

// setChatArchived archives or unarchives a chat, also on the phone.
// Like on the phone, archiving unpins the chat, and it stays archived until a
// newer message arrives, unless "keep chats archived" is enabled.
func (sm *SessionManager) setChatArchived(params []string, archive bool) {
	command, state, done := "unarchive", "not archived", "Unarchived"
	if archive {
		command, state, done = "archive", "archived", "Archived"
	}
	if !checkParam(params, 1) {
		sm.printCommandUsage(command, "[chat-id[], or press "+config.Config.Keymap.ChatArchive+" in the chat list")
		return
	}
	client, err := sm.connectedClient()
	if err != nil {
		sm.uiHandler.PrintError(err)
		return
	}
	chatID := params[0]
	var chat Chat
	for _, listed := range sm.db.GetChatIds() {
		if listed.Id == chatID {
			chat = listed
		}
	}
	if chat.Id != "" && chat.InArchive == archive {
		sm.uiHandler.PrintText(tview.Escape(sm.db.GetIdName(chatID) + " is " + state + " already"))
		return
	}
	target, err := sm.phoneChatJID(chatID)
	if err != nil {
		sm.uiHandler.PrintError(err)
		return
	}

	// the newest message, until which the chat is archived
	var lastTime time.Time
	var lastKey *waCommon.MessageKey
	if msgs := sm.db.GetMessages(chatID); len(msgs) > 0 {
		last := msgs[len(msgs)-1]
		lastTime = time.Unix(int64(last.Timestamp), 0)
		sender := types.EmptyJID // your own message
		if !last.FromMe {
			sender, _ = types.ParseJID(last.SenderId)
		}
		lastKey = client.BuildMessageKey(target, sender, types.MessageID(last.Id))
	}
	if err = sm.sendAppState(appstate.BuildArchive(target, archive, lastTime, lastKey)); err != nil {
		sm.uiHandler.PrintError(fmt.Errorf("failed to %s the chat: %v", command, err))
		return
	}

	// the phone's change comes back as app state events too, show it right away
	if !archive {
		sm.db.SetChatArchived(chatID, false, 0)
		sm.uiHandler.SetChats(sm.db.GetChatIds())
		sm.uiHandler.PrintText(done + " " + tview.Escape(sm.db.GetIdName(chatID)))
		return
	}
	sm.db.SetChatArchived(chatID, true, max(lastTime.Unix(), chat.LastMessage, chat.LastIncoming))
	sm.db.SetChatPinned(chatID, false, 0)
	sm.uiHandler.SetChats(sm.db.GetChatIds())
	sm.uiHandler.PrintText(done + " " + tview.Escape(sm.db.GetIdName(chatID)))
	// like on the phone, an archived chat is left
	sm.uiHandler.CloseChat(chatID)
}

// recoveryWait is how long the phone is waited for to repair the chat
// settings before it is asked again, see recoverAppState
const recoveryWait = 2 * time.Minute

// recoveryDone is called when the phone repaired an app state collection,
// which can be changed again, see sendAppState
func (sm *SessionManager) recoveryDone(name appstate.WAPatchName) {
	sm.recoveryLock.Lock()
	defer sm.recoveryLock.Unlock()
	delete(sm.recoveryRequested, name)
}

// errAppStateRepairing is returned by sendAppState while the phone repairs the chat settings
var errAppStateRepairing = errors.New("the chat settings are being repaired by your phone, try again in a moment")

// sendAppState sends a change of chat settings to the phone. When the phone
// changed them too, the server refuses the change until whatsmeow has caught
// up, so it syncs and sends the change once more. When the synced settings
// don't add up, the phone is asked to repair them, see recoverAppState.
func (sm *SessionManager) sendAppState(patch appstate.PatchInfo) error {
	client := sm.client()
	ctx := context.Background()
	err := client.SendAppState(ctx, patch)
	if err == nil {
		return nil
	} else if sm.recoverAppState(err, patch.Type) {
		return errAppStateRepairing
	}
	sm.logWarn("Sending %s failed, syncing it and retrying: %v", patch.Type, err)
	if syncErr := client.FetchAppState(ctx, patch.Type, false, false); syncErr != nil {
		if sm.recoverAppState(syncErr, patch.Type) {
			return errAppStateRepairing
		}
		return fmt.Errorf("%v (syncing failed too: %v)", err, syncErr)
	}
	return client.SendAppState(ctx, patch)
}

// markChatRead marks the unread messages of a chat as read, also on the phone,
// and returns how many there were.
func (sm *SessionManager) markChatRead(chatID string) (int, error) {
	client, err := sm.connectedClient()
	if err != nil {
		return 0, err
	}

	chatJID, err := types.ParseJID(chatID)
	if err != nil {
		return 0, fmt.Errorf("invalid JID: %v", err)
	}

	unreadMessages := sm.db.MarkChatRead(chatID)
	sm.uiHandler.SetChats(sm.db.GetChatIds())
	if len(unreadMessages) == 0 {
		return 0, nil
	}
	sm.showIfOpen(chatID) // no longer shown as unread

	type senderBatch struct {
		sender    types.JID
		ids       []types.MessageID
		timestamp time.Time
	}
	batches := make(map[string]*senderBatch)
	for _, msg := range unreadMessages {
		sender := chatJID
		if isGroupID(chatID) && msg.SenderId != "" {
			sender, err = types.ParseJID(msg.SenderId)
			if err != nil {
				continue
			}
		}
		key := sender.String()
		if _, ok := batches[key]; !ok {
			batches[key] = &senderBatch{sender: sender}
		}
		batches[key].ids = append(batches[key].ids, types.MessageID(msg.Id))
		ts := time.Unix(int64(msg.Timestamp), 0)
		if ts.After(batches[key].timestamp) {
			batches[key].timestamp = ts
		}
	}

	var failed error
	for _, batch := range batches {
		if batch.timestamp.IsZero() {
			batch.timestamp = time.Now()
		}
		if err := client.MarkRead(context.Background(), batch.ids, batch.timestamp, chatJID, batch.sender); err != nil {
			failed = fmt.Errorf("failed to mark messages as read: %v", err)
		}
	}
	return len(unreadMessages), failed
}
