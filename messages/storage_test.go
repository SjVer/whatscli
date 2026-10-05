package messages

import (
	"path/filepath"
	"testing"

	waProto "go.mau.fi/whatsmeow/binary/proto"
	"google.golang.org/protobuf/proto"
)

func TestAddMessageAndMarkChatRead(t *testing.T) {
	db := &MessageDatabase{}
	db.Init()

	first := Message{
		Id:           "msg-1",
		ChatId:       "123@s.whatsapp.net",
		ContactId:    "123@s.whatsapp.net",
		ContactName:  "Alice",
		ContactShort: "Alice",
		Timestamp:    100,
		Text:         "hello",
		Kind:         MessageKindText,
	}
	second := Message{
		Id:           "msg-2",
		ChatId:       "123@s.whatsapp.net",
		ContactId:    "123@s.whatsapp.net",
		ContactName:  "Alice",
		ContactShort: "Alice",
		Timestamp:    101,
		Text:         "[IMAGE]",
		Kind:         MessageKindImage,
	}

	if !db.AddMessage(first, false) {
		t.Fatal("expected first message to be new")
	}
	if !db.AddMessage(second, true) {
		t.Fatal("expected second message to be new")
	}

	msgs := db.GetMessages(first.ChatId)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Id != "msg-1" || msgs[1].Id != "msg-2" {
		t.Fatalf("messages not sorted by timestamp: %#v", msgs)
	}

	chats := db.GetChatIds()
	if len(chats) != 1 {
		t.Fatalf("expected 1 chat, got %d", len(chats))
	}
	if chats[0].Unread != 1 {
		t.Fatalf("expected unread count 1, got %d", chats[0].Unread)
	}

	cleared := db.MarkChatRead(first.ChatId)
	if len(cleared) != 1 || cleared[0].Id != "msg-2" {
		t.Fatalf("expected msg-2 to be cleared, got %#v", cleared)
	}

	chats = db.GetChatIds()
	if chats[0].Unread != 0 {
		t.Fatalf("expected unread count 0 after mark read, got %d", chats[0].Unread)
	}
}

func TestUpdateChatUnreadMarksLatestIncomingMessages(t *testing.T) {
	db := &MessageDatabase{}
	db.Init()

	for idx := 0; idx < 4; idx++ {
		db.AddMessage(Message{
			Id:           string(rune('a' + idx)),
			ChatId:       "group@g.us",
			SenderId:     "user@s.whatsapp.net",
			ContactId:    "user@s.whatsapp.net",
			ContactName:  "User",
			ContactShort: "User",
			Timestamp:    uint64(idx + 1),
			FromMe:       idx == 0,
			Text:         "payload",
			Kind:         MessageKindText,
		}, false)
	}

	db.UpdateChatUnread("group@g.us", 2)

	msgs := db.GetMessages("group@g.us")
	unread := 0
	for _, msg := range msgs {
		if msg.Unread {
			unread++
		}
	}
	if unread != 2 {
		t.Fatalf("expected 2 unread messages, got %d", unread)
	}
}

func TestGetChatIdsArchivedHiddenAndOrder(t *testing.T) {
	db := &MessageDatabase{}
	db.Init()

	db.AddChat(Chat{Id: "contact@s.whatsapp.net", Name: "Contact without chat"})
	db.UpdateChatLastMessage("pinned@s.whatsapp.net", 50)
	db.SetChatPinned("pinned@s.whatsapp.net", true, 10)
	// pinned chats are ordered by when they were pinned, not by their messages
	db.UpdateChatLastMessage("pinned-later@s.whatsapp.net", 40)
	db.SetChatPinned("pinned-later@s.whatsapp.net", true, 20)
	db.UpdateChatLastMessage("recent@s.whatsapp.net", 300)
	db.UpdateChatLastMessage("archived@s.whatsapp.net", 200)
	db.SetChatArchived("archived@s.whatsapp.net", true, 200)
	db.SetChatArchived("unarchived@s.whatsapp.net", true, 100)
	db.AddMessage(Message{Id: "in", ChatId: "unarchived@s.whatsapp.net", Timestamp: 250}, false)
	// messages sent by the user don't unarchive, only move the chat up in the archive
	db.SetChatArchived("replied@s.whatsapp.net", true, 100)
	db.AddMessage(Message{Id: "out", ChatId: "replied@s.whatsapp.net", Timestamp: 260, FromMe: true}, false)
	db.UpdateChatLastMessage("deleted@s.whatsapp.net", 150)
	db.SetChatDeleted("deleted@s.whatsapp.net", 150)

	chats := db.GetChatIds()
	byId := make(map[string]Chat)
	var order []string
	for _, chat := range chats {
		byId[chat.Id] = chat
		if !chat.Hidden && !chat.InArchive {
			order = append(order, chat.Id)
		}
	}

	expected := []string{"pinned-later@s.whatsapp.net", "pinned@s.whatsapp.net", "recent@s.whatsapp.net", "unarchived@s.whatsapp.net"}
	if len(order) != len(expected) {
		t.Fatalf("expected shown chats %v, got %v", expected, order)
	}
	for idx := range expected {
		if order[idx] != expected[idx] {
			t.Fatalf("expected shown chats %v, got %v", expected, order)
		}
	}
	if !byId["archived@s.whatsapp.net"].InArchive {
		t.Fatal("expected chat without messages since archiving to stay archived")
	}
	if !byId["replied@s.whatsapp.net"].InArchive {
		t.Fatal("expected chat with only own messages since archiving to stay archived")
	}
	if !byId["contact@s.whatsapp.net"].Hidden || !byId["deleted@s.whatsapp.net"].Hidden {
		t.Fatal("expected chats without messages to be hidden")
	}

	// a new message brings a deleted chat back, as on the phone
	db.UpdateChatLastMessage("deleted@s.whatsapp.net", 400)
	for _, chat := range db.GetChatIds() {
		if chat.Id == "deleted@s.whatsapp.net" && chat.Hidden {
			t.Fatal("expected deleted chat with a new message to be shown")
		}
	}

	// with "keep chats archived", new messages don't unarchive
	db.SetKeepArchived(true)
	for _, chat := range db.GetChatIds() {
		if chat.Id == "unarchived@s.whatsapp.net" && !chat.InArchive {
			t.Fatal("expected chat to stay archived with keep chats archived")
		}
	}
}

func TestSetChatUnarchivedOverridesOlderArchiveRecords(t *testing.T) {
	db := &MessageDatabase{}
	db.Init()

	// the phone lists the chat as not archived, for a message whatscli couldn't parse
	db.SetChatArchived("bob@s.whatsapp.net", true, 100)
	db.SetChatUnarchived("bob@s.whatsapp.net", 200)
	// archive records synced later at startup don't archive it again
	db.SetChatArchived("bob@s.whatsapp.net", true, 100)
	db.UpdateChatLastMessage("bob@s.whatsapp.net", 200)

	for _, chat := range db.GetChatIds() {
		if chat.Id == "bob@s.whatsapp.net" && chat.InArchive {
			t.Fatal("expected chat that the phone lists as not archived to stay unarchived")
		}
	}
}

func TestMergeChatMovesLIDChatToPhoneNumber(t *testing.T) {
	db := &MessageDatabase{}
	db.Init()

	db.AddChat(Chat{Id: "123@s.whatsapp.net", Name: "Alice"})
	db.AddMessage(Message{Id: "m1", ChatId: "456@lid", Timestamp: 300}, false)
	db.SetChatArchived("456@lid", true, 300)

	db.MergeChat("456@lid", "123@s.whatsapp.net")

	chats := db.GetChatIds()
	if len(chats) != 1 {
		t.Fatalf("expected 1 chat after merging, got %d", len(chats))
	}
	chat := chats[0]
	if chat.Id != "123@s.whatsapp.net" || chat.Name != "Alice" || !chat.Archived || chat.LastMessage != 300 || chat.LastIncoming != 300 {
		t.Fatalf("unexpected merged chat %+v", chat)
	}
	msgs := db.GetMessages("123@s.whatsapp.net")
	if len(msgs) != 1 || msgs[0].ChatId != "123@s.whatsapp.net" {
		t.Fatalf("expected message to move to the phone number chat, got %+v", msgs)
	}
	if msg, _ := db.GetMessage("m1"); msg.ChatId != "123@s.whatsapp.net" {
		t.Fatalf("expected message lookup to use the phone number chat, got %s", msg.ChatId)
	}
}

func TestNewestMessagesAreSavedAndLoaded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chats.json")
	db := &MessageDatabase{}
	db.Init()
	if err := db.LoadChats(path, 2); err != nil {
		t.Fatal(err)
	}
	db.AddMessage(Message{Id: "oldest", ChatId: "123@s.whatsapp.net", Timestamp: 50, Text: "oldest"}, false)
	db.AddMessage(Message{Id: "old", ChatId: "123@s.whatsapp.net", Timestamp: 100, Text: "old"}, false)
	db.AddMessage(Message{Id: "new", ChatId: "123@s.whatsapp.net", Timestamp: 200, Text: "new",
		RawMessage: &waProto.Message{Conversation: proto.String("new")}}, false)
	db.saveChats()

	loaded := &MessageDatabase{}
	loaded.Init()
	if err := loaded.LoadChats(path, 2); err != nil {
		t.Fatal(err)
	}
	msgs := loaded.GetMessages("123@s.whatsapp.net")
	if len(msgs) != 2 || msgs[0].Id != "old" || msgs[1].Id != "new" || msgs[1].Text != "new" {
		t.Fatalf("expected only the 2 newest messages to be loaded, got %+v", msgs)
	}
	if msgs[1].RawMessage.GetConversation() != "new" {
		t.Fatal("expected the raw message of a saved message to be loaded")
	}
	if oldest, ok := loaded.GetOldestMessage("123@s.whatsapp.net"); !ok || oldest.Id != "old" {
		t.Fatal("expected the oldest saved message to be the anchor for loading older ones")
	}
}

func TestSetReaction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chats.json")
	db := &MessageDatabase{}
	db.Init()
	if err := db.LoadChats(path, 10); err != nil {
		t.Fatal(err)
	}
	db.AddMessage(Message{Id: "m1", ChatId: "123@s.whatsapp.net", Timestamp: 100}, false)

	if chatID, ok := db.SetReaction("m1", "456@s.whatsapp.net", "👍", 150); !ok || chatID != "123@s.whatsapp.net" {
		t.Fatal("expected the reaction to be set on the loaded message")
	}
	db.SetReaction("m1", ReactorMe, "😭", 150)
	db.SetReaction("m1", ReactorMe, "❤️", 150) // replaces the own reaction
	if msg, _ := db.GetMessage("m1"); len(msg.Reactions) != 2 || msg.Reactions[ReactorMe] != "❤️" {
		t.Fatalf("unexpected reactions %v", msg.Reactions)
	}
	db.SetReaction("m1", "456@s.whatsapp.net", "", 150) // removes it
	db.saveChats()

	loaded := &MessageDatabase{}
	loaded.Init()
	if err := loaded.LoadChats(path, 10); err != nil {
		t.Fatal(err)
	}
	if msg, _ := loaded.GetMessage("m1"); len(msg.Reactions) != 1 || msg.Reactions[ReactorMe] != "❤️" {
		t.Fatalf("expected the reactions to be saved, got %v", msg.Reactions)
	}
}

func TestReactionToMessageLoadedLater(t *testing.T) {
	db := &MessageDatabase{}
	db.Init()
	if _, ok := db.SetReaction("old", "456@s.whatsapp.net", "👍", 150); ok {
		t.Fatal("expected the message not to be loaded yet")
	}
	db.AddMessage(Message{Id: "old", ChatId: "123@s.whatsapp.net", Timestamp: 100}, false)
	if msg, _ := db.GetMessage("old"); msg.Reactions["456@s.whatsapp.net"] != "👍" {
		t.Fatalf("expected the reaction to be added when the message is loaded, got %v", msg.Reactions)
	}
}

func TestUpdateContactNamesLooksUpEachSenderOnce(t *testing.T) {
	db := &MessageDatabase{}
	db.Init()
	db.AddMessage(Message{Id: "m1", ChatId: "group@g.us", ContactId: "456@lid", Timestamp: 100}, false)
	db.AddMessage(Message{Id: "m2", ChatId: "group@g.us", ContactId: "456@lid", Timestamp: 200}, false)

	lookups := 0
	db.UpdateContactNames(func(contactID string) (string, string, string, bool) {
		lookups++
		return "123@s.whatsapp.net", "Alice Smith", "Alice", true
	})
	if lookups != 1 {
		t.Fatalf("expected one lookup for the sender of both messages, got %d", lookups)
	}
	for _, msg := range db.GetMessages("group@g.us") {
		if msg.ContactId != "123@s.whatsapp.net" || msg.ContactName != "Alice Smith" || msg.ContactShort != "Alice" {
			t.Fatalf("expected the names to be updated, got %+v", msg)
		}
	}
}

func TestReactionTimes(t *testing.T) {
	db := &MessageDatabase{}
	db.Init()
	db.AddMessage(Message{Id: "m1", ChatId: "123@s.whatsapp.net", Timestamp: 100}, false)
	db.SetReaction("m1", "456@s.whatsapp.net", "👍", 200)
	if msg, _ := db.GetMessage("m1"); msg.ReactionTimes["456@s.whatsapp.net"] != 200 {
		t.Fatalf("expected the time of the reaction, got %v", msg.ReactionTimes)
	}
	db.SetReaction("m1", "456@s.whatsapp.net", "", 300)
	if msg, _ := db.GetMessage("m1"); len(msg.ReactionTimes) != 0 {
		t.Fatalf("expected the time to be removed with the reaction, got %v", msg.ReactionTimes)
	}

	// reactions to messages loaded later keep their time
	db.SetReaction("m2", ReactorMe, "❤️", 400)
	db.AddMessage(Message{Id: "m2", ChatId: "123@s.whatsapp.net", Timestamp: 350}, false)
	if msg, _ := db.GetMessage("m2"); msg.ReactionTimes[ReactorMe] != 400 || msg.Reactions[ReactorMe] != "❤️" {
		t.Fatalf("expected the pending reaction with its time, got %+v", msg)
	}
}

func TestUnreadStateAfterLoadingAndMerging(t *testing.T) {
	db := &MessageDatabase{}
	db.Init()
	chat := "111@s.whatsapp.net"
	// history sync sends the newest first
	db.AddMessage(Message{Id: "new", ChatId: chat, Timestamp: 300}, false)
	db.AddMessage(Message{Id: "old", ChatId: chat, Timestamp: 100}, false)
	db.UpdateChatUnread(chat, 1)
	if msg, _ := db.GetMessage("new"); !msg.Unread {
		t.Error("expected the newest message to be the unread one")
	}

	// saved and loaded again, the count is that of the unread messages
	path := filepath.Join(t.TempDir(), "chats.json")
	db.chatsPath, db.savedMessages = path, 50
	db.saveChats()
	loaded := &MessageDatabase{}
	loaded.Init()
	if err := loaded.LoadChats(path, 50); err != nil {
		t.Fatal(err)
	}
	if chats := loaded.GetChatIds(); chats[0].Unread != 1 {
		t.Errorf("expected 1 unread message after loading, got %d", chats[0].Unread)
	}

	// a message in both chats is merged once
	loaded.AddMessage(Message{Id: "new", ChatId: "222@lid", Timestamp: 300}, false)
	loaded.MergeChat("222@lid", chat)
	if msgs := loaded.GetMessages(chat); len(msgs) != 2 {
		t.Errorf("expected 2 messages after merging, got %d", len(msgs))
	}
}

func TestGroupStatusesOnceTheMembersAreKnown(t *testing.T) {
	db := &MessageDatabase{}
	db.Init()
	db.AddMessage(Message{Id: "m1", ChatId: "123-456@g.us", FromMe: true}, false)
	// before the members are known
	db.SetReceipt("m1", "alice", StatusRead, func(map[string]MessageStatus) MessageStatus { return StatusDelivered })
	members := []string{"alice"}
	if !db.UpdateGroupStatuses("123-456@g.us", func(receipts map[string]MessageStatus) MessageStatus { return groupStatus(receipts, members) }) {
		t.Fatal("expected the status to change")
	}
	if msg, _ := db.GetMessage("m1"); msg.Status != StatusRead {
		t.Errorf("expected read once the only member is known, got %v", msg.Status)
	}
}
