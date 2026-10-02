package messages

import (
	"testing"
	"time"

	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

func replyEvent(id string, chat, sender types.JID, text string, ctx *waProto.ContextInfo) *events.Message {
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat, Sender: sender, IsGroup: chat.Server == types.GroupServer},
			ID:            id,
			Timestamp:     time.Now(),
		},
		Message: &waProto.Message{ExtendedTextMessage: &waProto.ExtendedTextMessage{Text: proto.String(text), ContextInfo: ctx}},
	}
}

func TestRepliesShowWhatTheyReplyTo(t *testing.T) {
	sm := newTestSession(&recordingUi{})
	family := types.NewJID("123-456", types.GroupServer)
	alice := types.NewJID("111", types.DefaultUserServer)
	bob := types.NewJID("222", types.DefaultUserServer)
	sm.db.AddMessage(Message{Id: "known", ChatId: family.String(), ContactShort: "Bob", Text: "dinner at 7?"}, false)

	// to a loaded message
	sm.eventHandler.Handle(replyEvent("r1", family, alice, "yes!", &waProto.ContextInfo{
		StanzaID: proto.String("known"), Participant: proto.String(bob.String()),
	}))
	// to one that isn't loaded, from the copy in the reply
	sm.eventHandler.Handle(replyEvent("r2", family, alice, "me too", &waProto.ContextInfo{
		StanzaID:      proto.String("old"),
		Participant:   proto.String(bob.String()),
		QuotedMessage: &waProto.Message{Conversation: proto.String("who wants pizza")},
	}))

	for id, expected := range map[string]Reply{"r1": {Id: "known", Name: "Bob", Text: "dinner at 7?"}, "r2": {Id: "old", Text: "who wants pizza"}} {
		msg, _ := sm.db.GetMessage(id)
		if msg.ReplyTo == nil || msg.ReplyTo.Id != expected.Id || msg.ReplyTo.Text != expected.Text || expected.Name != "" && msg.ReplyTo.Name != expected.Name {
			t.Errorf("%s: expected a reply to %+v, got %+v", id, expected, msg.ReplyTo)
		}
	}
	if msg, _ := sm.db.GetMessage("known"); msg.ReplyTo != nil {
		t.Errorf("expected no reply for a message that doesn't reply, got %+v", msg.ReplyTo)
	}
}

func TestReplyContextRefersToTheMessage(t *testing.T) {
	quoted := Message{Id: "m1", SenderId: "111:3@s.whatsapp.net", RawMessage: &waProto.Message{Conversation: proto.String("hi")}}
	ctx := replyContext(quoted)
	if ctx.GetStanzaID() != "m1" || ctx.GetParticipant() != "111@s.whatsapp.net" || ctx.GetQuotedMessage().GetConversation() != "hi" {
		t.Errorf("unexpected context %v", ctx)
	}
}
