package messages

import (
	"testing"
	"time"

	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

func reactionEvent(target string, chat, sender types.JID, emoji string, at time.Time) *events.Message {
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat, Sender: sender, IsGroup: chat.Server == types.GroupServer},
			ID:            "r-" + target + emoji,
			Timestamp:     at,
		},
		Message: &waProto.Message{ReactionMessage: &waProto.ReactionMessage{
			Key:  &waProto.MessageKey{ID: proto.String(target)},
			Text: proto.String(emoji),
		}},
	}
}

func TestReactionsToOwnMessagesAreNew(t *testing.T) {
	ui := &recordingUi{}
	sm := newTestSession(ui)
	family := types.NewJID("123-456", types.GroupServer)
	alice := types.NewJID("111", types.DefaultUserServer)
	start := time.Now().Add(-time.Hour)
	sm.db.AddMessage(Message{Id: "mine", ChatId: family.String(), FromMe: true, Timestamp: uint64(start.Unix())}, false)
	sm.db.AddMessage(Message{Id: "hers", ChatId: family.String(), SenderId: alice.String(), Timestamp: uint64(start.Unix())}, false)

	sm.eventHandler.Handle(reactionEvent("mine", family, alice, "👍", start.Add(time.Minute)))
	sm.eventHandler.Handle(reactionEvent("hers", family, alice, "😂", start.Add(time.Minute)))
	if count := ui.chats[0].NewCount(); count != 1 {
		t.Fatalf("expected only the reaction to the user's message to be new, got %d", count)
	}

	// seen when the chat is read on the phone
	sm.eventHandler.Handle(&events.Receipt{
		MessageSource: types.MessageSource{Chat: family, IsFromMe: true},
		MessageIDs:    []types.MessageID{"hers"},
		Type:          types.ReceiptTypeRead,
		Timestamp:     start.Add(2 * time.Minute),
	})
	if count := ui.chats[0].NewCount(); count != 0 {
		t.Fatalf("expected the reaction to be seen, got %d new", count)
	}

	sm.eventHandler.Handle(reactionEvent("mine", family, alice, "❤️", start.Add(3*time.Minute)))
	sm.db.MarkChatRead(family.String())
	if count := sm.db.GetChatIds()[0].NewCount(); count != 0 {
		t.Errorf("expected reading the chat to clear the reaction, got %d new", count)
	}
}
