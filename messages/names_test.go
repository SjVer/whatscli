package messages

import (
	"os"
	"path/filepath"
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func TestDisplayIDWritesPhoneNumbers(t *testing.T) {
	for id, expected := range map[string]string{
		"31612345678@s.whatsapp.net":   "+31 6 12345678",
		"14155552671@s.whatsapp.net":   "+1 415-555-2671",
		"4915123456789@s.whatsapp.net": "+49 1512 3456789",
		"10000000000001@lid":           "Unknown",
		"123-456@g.us":                 "123-456",
	} {
		if actual := DisplayID(id); actual != expected {
			t.Errorf("%s: expected %q, got %q", id, expected, actual)
		}
	}
}

func TestContactDisplayNames(t *testing.T) {
	for _, test := range []struct {
		contact     types.ContactInfo
		name, short string
	}{
		{types.ContactInfo{FullName: "Alice Smith", FirstName: "Alice", PushName: "Ali"}, "Alice Smith", "Alice"},
		{types.ContactInfo{PushName: "Bob"}, "Bob", "Bob"},
		{types.ContactInfo{BusinessName: "Bakery"}, "Bakery", "Bakery"},
		{types.ContactInfo{}, "", ""},
	} {
		if name, short := contactDisplayNames(test.contact); name != test.name || short != test.short {
			t.Errorf("%+v: expected %q and %q, got %q and %q", test.contact, test.name, test.short, name, short)
		}
	}
}

func TestNumbersArentKeptAsChatNames(t *testing.T) {
	db := &MessageDatabase{}
	db.Init()
	chat := "31612345678@s.whatsapp.net"
	db.AddMessage(Message{Id: "m1", ChatId: chat, ContactName: "31612345678", Timestamp: 100}, false)
	if chats := db.GetChatIds(); chats[0].Name != "" {
		t.Fatalf("expected the number not to be kept as the name, got %q", chats[0].Name)
	}
	if name := db.GetIdName(chat); name != "+31 6 12345678" {
		t.Errorf("expected the number to be shown written out, got %q", name)
	}
	db.AddMessage(Message{Id: "m2", ChatId: chat, ContactName: "Alice", Timestamp: 200}, false)
	if name := db.GetIdName(chat); name != "Alice" {
		t.Errorf("expected the profile name once it is known, got %q", name)
	}
}

func TestMentionsOfUnknownPeopleKeepTheirNumber(t *testing.T) {
	sm := newTestSession(&recordingUi{})
	text, shown := sm.showMentions("hi @31612345678", []string{"31612345678@s.whatsapp.net"})
	if text != "hi @31612345678" || len(shown) != 1 || shown[0] != "@31612345678" {
		t.Errorf("expected the number to stay, got %q and %q", text, shown)
	}
}

func TestSavedNumbersAreDroppedAsChatNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chats.json")
	os.WriteFile(path, []byte(`[{"Id":"31612345678@s.whatsapp.net","Name":"31612345678","LastMessage":100,
		"Recent":[{"Id":"m1","ChatId":"31612345678@s.whatsapp.net","FromMe":true,"Timestamp":100}]}]`), 0600)
	db := &MessageDatabase{}
	db.Init()
	if err := db.LoadChats(path, 50); err != nil {
		t.Fatal(err)
	}
	if chats := db.GetChatIds(); chats[0].Name != "" {
		t.Errorf("expected the saved number not to be the name, got %q", chats[0].Name)
	}
	if msg, _ := db.GetMessage("m1"); msg.Status != StatusSent {
		t.Errorf("expected a saved message of the user to be sent at least, got %v", msg.Status)
	}
}
