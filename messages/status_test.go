package messages

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func receipt(chat, sender types.JID, receiptType types.ReceiptType, ids ...types.MessageID) *events.Receipt {
	return &events.Receipt{
		MessageSource: types.MessageSource{Chat: chat, Sender: sender, IsGroup: chat.Server == types.GroupServer},
		MessageIDs:    ids,
		Type:          receiptType,
		Timestamp:     time.Now(),
	}
}

func TestReceiptsTellHowFarMessagesGot(t *testing.T) {
	sm := newTestSession(&recordingUi{})
	alice := types.NewJID("111", types.DefaultUserServer)
	sm.db.AddMessage(Message{Id: "m1", ChatId: alice.String(), FromMe: true, Status: StatusSent}, false)
	status := func(id string) MessageStatus {
		msg, _ := sm.db.GetMessage(id)
		return msg.Status
	}

	sm.eventHandler.Handle(receipt(alice, alice, types.ReceiptTypeDelivered, "m1"))
	if status("m1") != StatusDelivered {
		t.Fatalf("expected delivered, got %v", status("m1"))
	}
	sm.eventHandler.Handle(receipt(alice, alice, types.ReceiptTypeRead, "m1"))
	// a late delivery receipt doesn't undo reading
	sm.eventHandler.Handle(receipt(alice, alice, types.ReceiptTypeDelivered, "m1"))
	if status("m1") != StatusRead {
		t.Fatalf("expected read, got %v", status("m1"))
	}

	// in a group whose members aren't known, it can only be told that it arrived
	family := types.NewJID("123-456", types.GroupServer)
	sm.db.AddMessage(Message{Id: "g1", ChatId: family.String(), FromMe: true, Status: StatusSent}, false)
	sm.eventHandler.Handle(receipt(family, alice, types.ReceiptTypeRead, "g1"))
	if status("g1") != StatusDelivered {
		t.Fatalf("expected delivered while the members aren't known, got %v", status("g1"))
	}
	if info := sm.receiptInfo("g1"); info == "" {
		t.Error("expected the info to tell who read it")
	}
}

func TestGroupMessagesAreReadWhenAllMembersReadThem(t *testing.T) {
	members := []string{"alice", "bob"}
	if status := groupStatus(map[string]MessageStatus{"alice": StatusRead}, members); status != StatusSent {
		t.Errorf("expected sent while bob has nothing, got %v", status)
	}
	if status := groupStatus(map[string]MessageStatus{"alice": StatusRead, "bob": StatusDelivered}, members); status != StatusDelivered {
		t.Errorf("expected delivered, got %v", status)
	}
	if status := groupStatus(map[string]MessageStatus{"alice": StatusRead, "bob": StatusRead}, members); status != StatusRead {
		t.Errorf("expected read, got %v", status)
	}
}

func TestStatusOnlyGoesUp(t *testing.T) {
	db := newTestDB()
	chat := "111@s.whatsapp.net"
	db.AddMessage(Message{Id: "m1", ChatId: chat, FromMe: true}, false)
	if msg, _ := db.GetMessage("m1"); msg.Status != StatusSent {
		t.Fatalf("expected a message of the user to be sent at least, got %v", msg.Status)
	}
	db.AddMessage(Message{Id: "m1", ChatId: chat, FromMe: true, Status: StatusRead,
		ReplyTo: &Reply{Id: "m0", Text: "first"}}, false)
	db.AddMessage(Message{Id: "m1", ChatId: chat, FromMe: true, Status: StatusDelivered,
		ReplyTo: &Reply{Id: "m0", Text: "second"}}, false)
	if msg, _ := db.GetMessage("m1"); msg.Status != StatusRead || msg.ReplyTo.Text != "first" {
		t.Errorf("expected the highest status and the first reply, got %v and %+v", msg.Status, msg.ReplyTo)
	}
	for status, expected := range map[waWeb.WebMessageInfo_Status]MessageStatus{
		waWeb.WebMessageInfo_PENDING:      StatusUnknown,
		waWeb.WebMessageInfo_SERVER_ACK:   StatusSent,
		waWeb.WebMessageInfo_DELIVERY_ACK: StatusDelivered,
		waWeb.WebMessageInfo_READ:         StatusRead,
		waWeb.WebMessageInfo_PLAYED:       StatusRead,
	} {
		if actual := historyStatus(status); actual != expected {
			t.Errorf("%v: expected %v, got %v", status, expected, actual)
		}
	}
}
