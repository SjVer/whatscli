package messages

import (
	"io"
	"testing"
	"time"

	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// recordingUi counts how often the chat list is shown, and keeps the last one
type recordingUi struct {
	chatLists int
	chats     []Chat
}

func (u *recordingUi) NewMessage(Message)               {}
func (u *recordingUi) NewScreen([]Message)              {}
func (u *recordingUi) PrintError(error)                 {}
func (u *recordingUi) PrintText(string)                 {}
func (u *recordingUi) SetStatus(SessionStatus)          {}
func (u *recordingUi) OpenFile(string)                  {}
func (u *recordingUi) CloseChat(string)                 {}
func (u *recordingUi) SetNotice(string, string, string) {}
func (u *recordingUi) GetWriter() io.Writer             { return io.Discard }
func (u *recordingUi) SetChats(chats []Chat) {
	u.chatLists++
	u.chats = chats
}

func (u *recordingUi) unread(chatID string) int {
	for _, chat := range u.chats {
		if chat.Id == chatID {
			return chat.Unread
		}
	}
	return -1
}

func newTestSession(ui *recordingUi) *SessionManager {
	sm := &SessionManager{uiHandler: ui, db: &MessageDatabase{}}
	sm.db.Init()
	sm.eventHandler = &eventHandler{sm: sm}
	return sm
}

func incomingMessage(id string, chat types.JID, at time.Time) *events.Message {
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat, Sender: chat},
			ID:            id,
			Timestamp:     at,
		},
		Message: &waProto.Message{Conversation: proto.String("hi " + id)},
	}
}

func TestMessagesFromWhileOfflineAreShownOnceAndReadOnThePhone(t *testing.T) {
	ui := &recordingUi{}
	sm := newTestSession(ui)
	alice := types.NewJID("111", types.DefaultUserServer)
	bob := types.NewJID("222", types.DefaultUserServer)
	start := time.Now().Add(-time.Hour)

	sm.eventHandler.Handle(&events.OfflineSyncPreview{Messages: 5})
	for i, id := range []string{"a1", "a2", "a3"} {
		sm.eventHandler.Handle(incomingMessage(id, alice, start.Add(time.Duration(i)*time.Minute)))
	}
	sm.eventHandler.Handle(incomingMessage("b1", bob, start))
	// read on the phone up to the second message of Alice
	sm.eventHandler.Handle(&events.Receipt{
		MessageSource: types.MessageSource{Chat: alice, IsFromMe: true},
		MessageIDs:    []types.MessageID{"a2"},
		Type:          types.ReceiptTypeRead,
		Timestamp:     start.Add(10 * time.Minute),
	})
	// Bob's chat was read before his next message arrives here
	sm.eventHandler.Handle(&events.Receipt{
		MessageSource: types.MessageSource{Chat: bob, IsFromMe: true},
		MessageIDs:    []types.MessageID{"b2"},
		Type:          types.ReceiptTypeReadSelf,
		Timestamp:     start.Add(10 * time.Minute),
	})
	sm.eventHandler.Handle(incomingMessage("b2", bob, start.Add(time.Minute)))
	if ui.chatLists != 0 {
		t.Fatalf("expected the chat list not to be shown while receiving, it was shown %d times", ui.chatLists)
	}

	sm.eventHandler.Handle(&events.OfflineSyncCompleted{})
	if ui.chatLists != 1 {
		t.Fatalf("expected the chat list to be shown once, it was shown %d times", ui.chatLists)
	}
	if unread := ui.unread(alice.String()); unread != 1 {
		t.Errorf("expected only the message after the read one to be new, got %d", unread)
	}
	if unread := ui.unread(bob.String()); unread != 0 {
		t.Errorf("expected the messages read on the phone not to be new, got %d", unread)
	}

	// a message after being read is new again, and shown right away
	sm.eventHandler.Handle(incomingMessage("b3", bob, start.Add(20*time.Minute)))
	if ui.chatLists != 2 || ui.unread(bob.String()) != 1 {
		t.Errorf("expected a new message to show in the chat list, got %d unread after %d lists", ui.unread(bob.String()), ui.chatLists)
	}
}

func TestReceiptsOfOthersDontMarkRead(t *testing.T) {
	ui := &recordingUi{}
	sm := newTestSession(ui)
	alice := types.NewJID("111", types.DefaultUserServer)
	sm.eventHandler.Handle(incomingMessage("a1", alice, time.Now().Add(-time.Hour)))
	// Alice read a message of the user
	sm.eventHandler.Handle(&events.Receipt{
		MessageSource: types.MessageSource{Chat: alice, Sender: alice},
		MessageIDs:    []types.MessageID{"mine"},
		Type:          types.ReceiptTypeRead,
		Timestamp:     time.Now(),
	})
	if unread := ui.unread(alice.String()); unread != 1 {
		t.Errorf("expected the message to stay new, got %d", unread)
	}
}

func TestMarkChatReadUntil(t *testing.T) {
	db := &MessageDatabase{}
	db.Init()
	chat := "123@s.whatsapp.net"
	for i, id := range []string{"m1", "m2", "m3"} {
		db.AddMessage(Message{Id: id, ChatId: chat, Timestamp: uint64(100 + i)}, true)
	}
	if !db.MarkChatReadUntil(chat, 101) {
		t.Fatal("expected reading to change the chat")
	}
	if db.MarkChatReadUntil(chat, 100) {
		t.Error("expected reading an older message to change nothing")
	}
	if chats := db.GetChatIds(); chats[0].Unread != 1 {
		t.Errorf("expected 1 unread message, got %d", chats[0].Unread)
	}
	// received late, but read already
	db.AddMessage(Message{Id: "m0", ChatId: chat, Timestamp: 99}, true)
	if msg, _ := db.GetMessage("m0"); msg.Unread {
		t.Error("expected a message before the read ones not to be new")
	}
	if chats := db.GetChatIds(); chats[0].Unread != 1 {
		t.Errorf("expected 1 unread message still, got %d", chats[0].Unread)
	}
}

func TestOlderMessagesFromThePhoneDontMarkUnread(t *testing.T) {
	ui := &recordingUi{}
	sm := newTestSession(ui)
	alice := types.NewJID("111", types.DefaultUserServer)
	start := time.Now().Add(-time.Hour)
	sm.eventHandler.Handle(incomingMessage("a1", alice, start))
	sm.eventHandler.Handle(incomingMessage("a2", alice, start.Add(time.Minute)))
	sm.db.MarkChatReadUntil(alice.String(), start.Add(time.Minute).Unix())

	conversation := func(syncType waHistorySync.HistorySync_HistorySyncType) *events.HistorySync {
		return &events.HistorySync{Data: &waHistorySync.HistorySync{
			SyncType: syncType.Enum(),
			Conversations: []*waHistorySync.Conversation{{
				ID:               proto.String(alice.String()),
				Name:             proto.String("Alice"),
				UnreadCount:      proto.Uint32(2),
				LastMsgTimestamp: proto.Uint64(uint64(start.Add(time.Minute).Unix())),
			}},
		}}
	}
	// the answer when the chat is opened
	sm.eventHandler.Handle(conversation(waHistorySync.HistorySync_ON_DEMAND))
	if unread := ui.unread(alice.String()); unread != 0 {
		t.Fatalf("expected the older messages not to change what is new, got %d", unread)
	}
	// nor the phone's count of a chat that was read on another device since
	sm.eventHandler.Handle(conversation(waHistorySync.HistorySync_RECENT))
	if unread := ui.unread(alice.String()); unread != 0 {
		t.Fatalf("expected the messages read on another device to stay read, got %d", unread)
	}
}

func TestMessagesInTheOpenChatAreNewWhileNotLookedAt(t *testing.T) {
	ui := &recordingUi{}
	sm := newTestSession(ui)
	alice := types.NewJID("111", types.DefaultUserServer)
	sm.currentReceiver = alice.String()
	looking := true
	sm.ChatSeen = func() bool { return looking }

	sm.eventHandler.Handle(incomingMessage("a1", alice, time.Now().Add(-time.Minute)))
	if unread := ui.unread(alice.String()); unread != 0 {
		t.Fatalf("expected a message in the chat the user looks at to be read, got %d new", unread)
	}
	// e.g. another window has focus
	looking = false
	sm.eventHandler.Handle(incomingMessage("a2", alice, time.Now().Add(-time.Minute)))
	if unread := ui.unread(alice.String()); unread != 1 {
		t.Fatalf("expected a message in the open chat to be new while it isn't looked at, got %d", unread)
	}
}
