package messages

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	waProto "go.mau.fi/whatsmeow/binary/proto"
	"google.golang.org/protobuf/proto"
)

// MessageDatabase stores messages and contact data.
type MessageDatabase struct {
	messages     map[string][]Message
	messagesById map[string]Message
	chats        map[string]Chat
	contacts     map[string]Contact

	chatsPath    string
	saveTimer    *time.Timer
	keepArchived bool
	// how many of the newest messages of each chat are saved
	savedMessages int
	// reactions to messages that are not loaded yet
	pendingReactions map[string]map[string]string

	contactLock sync.RWMutex
	chatLock    sync.RWMutex
	messageLock sync.RWMutex
}

// Init initializes the message database.
func (md *MessageDatabase) Init() {
	md.messages = make(map[string][]Message)
	md.messagesById = make(map[string]Message)
	md.chats = make(map[string]Chat)
	md.contacts = make(map[string]Contact)
}

// Reset removes all messages, chats and contacts, including the saved chats.
func (md *MessageDatabase) Reset() error {
	md.messageLock.Lock()
	md.messages = make(map[string][]Message)
	md.messagesById = make(map[string]Message)
	md.pendingReactions = nil
	md.messageLock.Unlock()
	md.contactLock.Lock()
	md.contacts = make(map[string]Contact)
	md.contactLock.Unlock()

	md.chatLock.Lock()
	defer md.chatLock.Unlock()
	md.chats = make(map[string]Chat)
	md.keepArchived = false
	if md.saveTimer != nil {
		md.saveTimer.Stop()
		md.saveTimer = nil
	}
	if md.chatsPath != "" {
		if err := os.Remove(md.chatsPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// LoadChats restores the chats saved at path and keeps saving changes there.
// The newest savedMessages messages of each chat are saved with it.
func (md *MessageDatabase) LoadChats(path string, savedMessages int) error {
	md.messageLock.Lock()
	defer md.messageLock.Unlock()
	md.chatLock.Lock()
	defer md.chatLock.Unlock()

	md.savedMessages = savedMessages

	md.chatsPath = path
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	var chats []Chat
	if err = json.Unmarshal(data, &chats); err != nil {
		return err
	}
	for _, chat := range chats {
		chat.Unread = 0
		for _, recent := range chat.Recent {
			msg := recent.Message
			if len(recent.Raw) > 0 {
				msg.RawMessage = &waProto.Message{}
				if proto.Unmarshal(recent.Raw, msg.RawMessage) != nil {
					msg.RawMessage = nil
				}
			}
			if _, ok := md.messagesById[msg.Id]; !ok {
				md.messagesById[msg.Id] = msg
				md.messages[chat.Id] = append(md.messages[chat.Id], msg)
			}
		}
		chat.Recent = nil
		md.chats[chat.Id] = chat
	}
	return nil
}

// UpdateChatLastMessage moves a chat's last message time forward if timestamp is newer.
func (md *MessageDatabase) UpdateChatLastMessage(chatID string, timestamp int64) {
	md.chatLock.Lock()
	defer md.chatLock.Unlock()

	chat, ok := md.chats[chatID]
	if !ok {
		chat = Chat{Id: chatID, IsGroup: strings.Contains(chatID, GROUPSUFFIX)}
	}
	if timestamp > chat.LastMessage {
		chat.LastMessage = timestamp
		md.chats[chatID] = chat
		md.scheduleSaveLocked()
	}
}

// SetChatPinned sets whether a chat is pinned to the top of the chat list, and when.
func (md *MessageDatabase) SetChatPinned(chatID string, pinned bool, pinnedAt int64) {
	md.updateChatLocked(chatID, func(chat *Chat) {
		chat.Pinned = pinned
		chat.PinnedAt = pinnedAt
	})
}

// SetChatArchived sets whether a chat is moved to the archived chats, and the
// time of its last message at that point.
func (md *MessageDatabase) SetChatArchived(chatID string, archived bool, archivedAt int64) {
	md.updateChatLocked(chatID, func(chat *Chat) {
		chat.Archived = archived
		if archivedAt > chat.ArchivedAt {
			chat.ArchivedAt = archivedAt
		}
	})
}

// SetChatUnarchived records that the phone lists a chat as not archived, while
// its last message was at the given time. The phone may have unarchived it for
// a message that whatscli doesn't know, so that counts as the last incoming one.
func (md *MessageDatabase) SetChatUnarchived(chatID string, lastMessage int64) {
	md.updateChatLocked(chatID, func(chat *Chat) {
		if lastMessage > chat.LastIncoming {
			chat.LastIncoming = lastMessage
		}
		if chat.LastIncoming <= chat.ArchivedAt {
			chat.Archived = false
		}
	})
}

// MergeChat moves the messages and state of chat from into chat to, for chats
// that turn out to be the same, like the LID and phone number of a contact.
func (md *MessageDatabase) MergeChat(from, to string) {
	md.messageLock.Lock()
	for _, msg := range md.messages[from] {
		msg.ChatId = to
		md.messagesById[msg.Id] = msg
		md.messages[to] = append(md.messages[to], msg)
	}
	delete(md.messages, from)
	md.messageLock.Unlock()

	md.chatLock.Lock()
	defer md.chatLock.Unlock()
	src, ok := md.chats[from]
	if !ok {
		return
	}
	dst, ok := md.chats[to]
	if !ok {
		dst = Chat{Id: to, IsGroup: src.IsGroup}
	}
	if dst.Name == "" {
		dst.Name = src.Name
	}
	dst.LastMessage = max(dst.LastMessage, src.LastMessage)
	dst.LastIncoming = max(dst.LastIncoming, src.LastIncoming)
	dst.DeletedAt = max(dst.DeletedAt, src.DeletedAt)
	if src.ArchivedAt > dst.ArchivedAt {
		dst.Archived = src.Archived
		dst.ArchivedAt = src.ArchivedAt
	}
	if src.Pinned && src.PinnedAt > dst.PinnedAt {
		dst.Pinned = true
		dst.PinnedAt = src.PinnedAt
	}
	dst.Unread = max(dst.Unread, src.Unread)
	delete(md.chats, from)
	md.chats[to] = dst
	md.scheduleSaveLocked()
}

// SetChatDeleted records that a chat was deleted, up to the given last message time.
func (md *MessageDatabase) SetChatDeleted(chatID string, deletedAt int64) {
	md.updateChatLocked(chatID, func(chat *Chat) {
		if deletedAt > chat.DeletedAt {
			chat.DeletedAt = deletedAt
		}
	})
}

// KeepArchived returns whether archived chats stay archived when new messages arrive.
func (md *MessageDatabase) KeepArchived() bool {
	md.chatLock.RLock()
	defer md.chatLock.RUnlock()
	return md.keepArchived
}

// SetKeepArchived sets whether archived chats stay archived when new messages arrive.
func (md *MessageDatabase) SetKeepArchived(keep bool) {
	md.chatLock.Lock()
	defer md.chatLock.Unlock()
	md.keepArchived = keep
}

func (md *MessageDatabase) updateChatLocked(chatID string, update func(chat *Chat)) {
	md.chatLock.Lock()
	defer md.chatLock.Unlock()

	chat, ok := md.chats[chatID]
	if !ok {
		chat = Chat{Id: chatID, IsGroup: strings.Contains(chatID, GROUPSUFFIX)}
	}
	update(&chat)
	md.chats[chatID] = chat
	md.scheduleSaveLocked()
}

// scheduleSaveLocked saves the chats shortly, batching bursts of updates. Requires chatLock.
func (md *MessageDatabase) scheduleSaveLocked() {
	if md.chatsPath == "" || md.saveTimer != nil {
		return
	}
	md.saveTimer = time.AfterFunc(time.Second, md.saveChats)
}

func (md *MessageDatabase) saveChats() {
	md.messageLock.RLock()
	md.chatLock.Lock()
	md.saveTimer = nil
	chats := make([]Chat, 0, len(md.chats))
	for _, chat := range md.chats {
		if chat.LastMessage > 0 {
			chat.Recent = md.recentMessagesLocked(chat.Id)
			chats = append(chats, chat)
		}
	}
	path := md.chatsPath
	md.chatLock.Unlock()
	md.messageLock.RUnlock()

	if path == "" {
		return
	}
	data, err := json.Marshal(chats)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err = os.WriteFile(tmp, data, 0600); err == nil {
		os.Rename(tmp, path)
	}
}

// recentMessagesLocked returns the newest messages of a chat to save. Requires messageLock.
func (md *MessageDatabase) recentMessagesLocked(chatID string) []SavedMessage {
	msgs := make([]Message, len(md.messages[chatID]))
	copy(msgs, md.messages[chatID])
	sortMessages(msgs)
	if len(msgs) > md.savedMessages {
		msgs = msgs[len(msgs)-md.savedMessages:]
	}
	recent := make([]SavedMessage, len(msgs))
	for idx, msg := range msgs {
		recent[idx].Message = msg
		if msg.RawMessage != nil {
			recent[idx].Raw, _ = proto.Marshal(msg.RawMessage)
		}
	}
	return recent
}

// sortMessages sorts messages by time, oldest first.
func sortMessages(msgs []Message) {
	sort.Slice(msgs, func(i, j int) bool {
		if msgs[i].Timestamp == msgs[j].Timestamp {
			return msgs[i].Id < msgs[j].Id
		}
		return msgs[i].Timestamp < msgs[j].Timestamp
	})
}

// AddMessage stores a message and updates related chat state.
func (md *MessageDatabase) AddMessage(msg Message, markUnread bool) bool {
	md.messageLock.Lock()
	defer md.messageLock.Unlock()

	if existing, ok := md.messagesById[msg.Id]; ok {
		// Keep the first version, but upgrade metadata if the newer message has richer data.
		if existing.RawMessage == nil && msg.RawMessage != nil {
			existing.RawMessage = msg.RawMessage
		}
		if existing.Kind == MessageKindUnknown && msg.Kind != MessageKindUnknown {
			existing.Kind = msg.Kind
		}
		if existing.Text == "" && msg.Text != "" {
			existing.Text = msg.Text
		}
		if existing.FileName == "" && msg.FileName != "" {
			existing.FileName = msg.FileName
		}
		if existing.MimeType == "" && msg.MimeType != "" {
			existing.MimeType = msg.MimeType
		}
		existing.Unread = existing.Unread || markUnread
		md.messagesById[msg.Id] = existing
		md.replaceMessageLocked(existing)
		md.updateChatFromMessageLocked(existing, markUnread)
		return false
	}

	msg.Unread = markUnread
	for reactor, reaction := range md.pendingReactions[msg.Id] {
		msg.Reactions = withReaction(msg.Reactions, reactor, reaction)
	}
	delete(md.pendingReactions, msg.Id)
	md.messagesById[msg.Id] = msg
	md.messages[msg.ChatId] = append(md.messages[msg.ChatId], msg)
	md.updateChatFromMessageLocked(msg, markUnread)
	return true
}

func (md *MessageDatabase) replaceMessageLocked(msg Message) {
	msgs := md.messages[msg.ChatId]
	for idx, current := range msgs {
		if current.Id == msg.Id {
			msgs[idx] = msg
			md.messages[msg.ChatId] = msgs
			return
		}
	}
}

func (md *MessageDatabase) updateChatFromMessageLocked(msg Message, markUnread bool) {
	md.chatLock.Lock()
	defer md.chatLock.Unlock()

	chat, exists := md.chats[msg.ChatId]
	if !exists {
		chat = Chat{
			Id:      msg.ChatId,
			IsGroup: strings.Contains(msg.ChatId, GROUPSUFFIX),
			Name:    msg.ContactName,
		}
	}
	if chat.Name == "" {
		chat.Name = msg.ContactName
	}
	if int64(msg.Timestamp) > chat.LastMessage {
		chat.LastMessage = int64(msg.Timestamp)
	}
	if !msg.FromMe && int64(msg.Timestamp) > chat.LastIncoming {
		chat.LastIncoming = int64(msg.Timestamp)
	}
	// the message may be one of the saved newest ones
	md.scheduleSaveLocked()
	if markUnread {
		chat.Unread++
	}
	md.chats[msg.ChatId] = chat

	if msg.ContactId != "" {
		md.contactLock.Lock()
		if _, ok := md.contacts[msg.ContactId]; !ok {
			md.contacts[msg.ContactId] = Contact{
				Id:    msg.ContactId,
				Name:  msg.ContactName,
				Short: msg.ContactShort,
			}
		}
		md.contactLock.Unlock()
	}
}

// AddChat adds or updates a chat in the database.
func (md *MessageDatabase) AddChat(chat Chat) {
	md.chatLock.Lock()
	defer md.chatLock.Unlock()

	existing, ok := md.chats[chat.Id]
	if ok {
		if chat.Name == "" {
			chat.Name = existing.Name
		}
		if chat.LastMessage < existing.LastMessage {
			chat.LastMessage = existing.LastMessage
		}
		if chat.Unread < existing.Unread {
			chat.Unread = existing.Unread
		}
		chat.Pinned = existing.Pinned
		chat.PinnedAt = existing.PinnedAt
		chat.Archived = existing.Archived
		chat.ArchivedAt = existing.ArchivedAt
		chat.DeletedAt = existing.DeletedAt
		if chat.LastIncoming < existing.LastIncoming {
			chat.LastIncoming = existing.LastIncoming
		}
	}
	md.chats[chat.Id] = chat
	md.scheduleSaveLocked()
}

// UpdateChatUnread syncs unread counts from external sources such as history sync.
func (md *MessageDatabase) UpdateChatUnread(chatID string, unread int) {
	md.messageLock.Lock()
	defer md.messageLock.Unlock()

	ids := md.lastIncomingMessageIDsLocked(chatID, unread)
	unreadSet := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		unreadSet[id] = struct{}{}
	}

	msgs := md.messages[chatID]
	for idx, msg := range msgs {
		_, ok := unreadSet[msg.Id]
		msg.Unread = ok
		msgs[idx] = msg
		if stored, found := md.messagesById[msg.Id]; found {
			stored.Unread = ok
			md.messagesById[msg.Id] = stored
		}
	}
	md.messages[chatID] = msgs

	md.chatLock.Lock()
	if chat, ok := md.chats[chatID]; ok {
		chat.Unread = len(ids)
		md.chats[chatID] = chat
	}
	md.scheduleSaveLocked()
	md.chatLock.Unlock()
}

func (md *MessageDatabase) lastIncomingMessageIDsLocked(chatID string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	msgs := md.messages[chatID]
	ids := make([]string, 0, limit)
	for idx := len(msgs) - 1; idx >= 0 && len(ids) < limit; idx-- {
		if !msgs[idx].FromMe {
			ids = append(ids, msgs[idx].Id)
		}
	}
	return ids
}

// MarkChatRead clears unread state for the given chat and returns the unread messages that were cleared.
func (md *MessageDatabase) MarkChatRead(chatID string) []Message {
	md.messageLock.Lock()
	defer md.messageLock.Unlock()

	msgs := md.messages[chatID]
	cleared := make([]Message, 0)
	for idx, msg := range msgs {
		if msg.Unread {
			cleared = append(cleared, msg)
			msg.Unread = false
			msgs[idx] = msg
			stored := md.messagesById[msg.Id]
			stored.Unread = false
			md.messagesById[msg.Id] = stored
		}
	}
	md.messages[chatID] = msgs

	md.chatLock.Lock()
	if chat, ok := md.chats[chatID]; ok {
		chat.Unread = 0
		md.chats[chatID] = chat
	}
	md.scheduleSaveLocked()
	md.chatLock.Unlock()

	return cleared
}

// SetReaction sets the emoji reaction of reactor to a message, an empty reaction
// removes it. Returns the chat of the message, and false if the message is not
// loaded yet, in which case the reaction is added when it is.
func (md *MessageDatabase) SetReaction(messageID, reactor, reaction string) (string, bool) {
	md.messageLock.Lock()
	defer md.messageLock.Unlock()
	msg, ok := md.messagesById[messageID]
	if !ok {
		if md.pendingReactions == nil {
			md.pendingReactions = make(map[string]map[string]string)
		}
		md.pendingReactions[messageID] = withReaction(md.pendingReactions[messageID], reactor, reaction)
		return "", false
	}
	msg.Reactions = withReaction(msg.Reactions, reactor, reaction)
	md.messagesById[messageID] = msg
	md.replaceMessageLocked(msg)
	md.chatLock.Lock()
	md.scheduleSaveLocked()
	md.chatLock.Unlock()
	return msg.ChatId, true
}

// withReaction returns a copy of reactions with the reaction of reactor set,
// as messages are copied and would otherwise share the map
func withReaction(reactions map[string]string, reactor, reaction string) map[string]string {
	updated := make(map[string]string, len(reactions)+1)
	for key, value := range reactions {
		updated[key] = value
	}
	if reaction == "" {
		delete(updated, reactor)
	} else {
		updated[reactor] = reaction
	}
	if len(updated) == 0 {
		return nil
	}
	return updated
}

// UpdateContactNames sets the sender id and names of all messages in memory to
// what names returns for their current sender id, if it returns ok. names is
// called once per sender, without holding the lock, as it may be slow.
func (md *MessageDatabase) UpdateContactNames(names func(contactID string) (string, string, string, bool)) {
	md.messageLock.RLock()
	senders := make(map[string]struct{})
	for _, msgs := range md.messages {
		for _, msg := range msgs {
			if msg.ContactId != "" {
				senders[msg.ContactId] = struct{}{}
			}
		}
	}
	md.messageLock.RUnlock()

	type contactNames struct{ id, name, short string }
	resolved := make(map[string]contactNames, len(senders))
	for sender := range senders {
		if id, name, short, ok := names(sender); ok {
			resolved[sender] = contactNames{id, name, short}
		}
	}

	md.messageLock.Lock()
	defer md.messageLock.Unlock()
	changed := false
	for chatID, msgs := range md.messages {
		for idx, msg := range msgs {
			names, ok := resolved[msg.ContactId]
			if !ok || (names.id == msg.ContactId && names.name == msg.ContactName && names.short == msg.ContactShort) {
				continue
			}
			msg.ContactId, msg.ContactName, msg.ContactShort = names.id, names.name, names.short
			msgs[idx] = msg
			md.messagesById[msg.Id] = msg
			changed = true
		}
		md.messages[chatID] = msgs
	}
	if changed {
		md.chatLock.Lock()
		md.scheduleSaveLocked()
		md.chatLock.Unlock()
	}
}

// MarkMessageRevoked updates a message to show that it was revoked.
func (md *MessageDatabase) MarkMessageRevoked(messageID string) bool {
	md.messageLock.Lock()
	defer md.messageLock.Unlock()

	msg, ok := md.messagesById[messageID]
	if !ok {
		return false
	}
	msg.Text = "[message revoked]"
	msg.RawMessage = nil
	msg.Kind = MessageKindUnknown
	md.messagesById[messageID] = msg
	md.replaceMessageLocked(msg)
	md.chatLock.Lock()
	md.scheduleSaveLocked()
	md.chatLock.Unlock()
	return true
}

// AddContact adds or updates a contact in the database.
func (md *MessageDatabase) AddContact(contact Contact) {
	md.contactLock.Lock()
	defer md.contactLock.Unlock()

	existing, ok := md.contacts[contact.Id]
	if ok {
		if contact.Name == "" {
			contact.Name = existing.Name
		}
		if contact.Short == "" {
			contact.Short = existing.Short
		}
	}
	md.contacts[contact.Id] = contact
}

// GetChatIds returns pinned chats first, the last pinned one first, then the
// other chats with the most recent message first.
// InArchive is set for chats that are still archived: unless "keep chats archived"
// is enabled, WhatsApp unarchives a chat when a message arrives that the user didn't send,
// without syncing that.
// Hidden is set for chats without messages since they were deleted, if they ever had any.
func (md *MessageDatabase) GetChatIds() []Chat {
	md.chatLock.RLock()
	defer md.chatLock.RUnlock()

	allChats := make([]Chat, 0, len(md.chats))
	for _, chat := range md.chats {
		chat.InArchive = chat.Archived && (md.keepArchived || chat.LastIncoming <= chat.ArchivedAt)
		chat.Hidden = !chat.Pinned && chat.LastMessage <= chat.DeletedAt
		allChats = append(allChats, chat)
	}
	sort.Slice(allChats, func(i, j int) bool {
		if allChats[i].Pinned != allChats[j].Pinned {
			return allChats[i].Pinned
		}
		if allChats[i].Pinned && allChats[i].PinnedAt != allChats[j].PinnedAt {
			return allChats[i].PinnedAt > allChats[j].PinnedAt
		}
		if allChats[i].LastMessage == allChats[j].LastMessage {
			return allChats[i].Name < allChats[j].Name
		}
		return allChats[i].LastMessage > allChats[j].LastMessage
	})
	return allChats
}

// GetMessages returns all messages for the given chat, sorted by timestamp.
func (md *MessageDatabase) GetMessages(chatID string) []Message {
	md.messageLock.RLock()
	msgs := md.messages[chatID]
	out := make([]Message, len(msgs))
	copy(out, msgs)
	md.messageLock.RUnlock()

	sortMessages(out)
	return out
}

// GetMessage returns a single message by ID.
func (md *MessageDatabase) GetMessage(id string) (Message, bool) {
	md.messageLock.RLock()
	defer md.messageLock.RUnlock()
	msg, ok := md.messagesById[id]
	return msg, ok
}

// GetOldestMessage returns the oldest stored message in a chat.
func (md *MessageDatabase) GetOldestMessage(chatID string) (Message, bool) {
	md.messageLock.RLock()
	defer md.messageLock.RUnlock()
	msgs := md.messages[chatID]
	if len(msgs) == 0 {
		return Message{}, false
	}
	oldest := msgs[0]
	for _, m := range msgs[1:] {
		if m.Timestamp < oldest.Timestamp || (m.Timestamp == oldest.Timestamp && m.Id < oldest.Id) {
			oldest = m
		}
	}
	return oldest, true
}

// GetMessageInfo returns a human-readable description of a message.
func (md *MessageDatabase) GetMessageInfo(id string) string {
	msg, ok := md.GetMessage(id)
	if !ok {
		return "Message not found"
	}

	name := md.GetIdName(msg.ContactId)
	short := md.GetIdShort(msg.ContactId)
	direction := "←"
	if msg.FromMe {
		direction = "→"
	}

	kind := string(msg.Kind)
	if kind == "" {
		kind = string(MessageKindUnknown)
	}

	info := fmt.Sprintf(
		"Type: %s\nFrom: %s (%s) %s\nTime: %s",
		kind,
		name,
		short,
		direction,
		time.Unix(int64(msg.Timestamp), 0).Format(time.RFC1123),
	)
	if msg.FileName != "" {
		info += "\nFile: " + msg.FileName
	}
	if msg.MimeType != "" {
		info += "\nMIME: " + msg.MimeType
	}
	return info
}

// GetIdName resolves a contact or chat ID to a display name.
func (md *MessageDatabase) GetIdName(id string) string {
	if id == "" {
		return "Unknown"
	}

	md.contactLock.RLock()
	contact, ok := md.contacts[id]
	md.contactLock.RUnlock()
	if ok {
		if contact.Name != "" {
			return contact.Name
		}
		if contact.Short != "" {
			return contact.Short
		}
	}

	md.chatLock.RLock()
	chat, ok := md.chats[id]
	md.chatLock.RUnlock()
	if ok && chat.Name != "" {
		return chat.Name
	}

	return strings.TrimSuffix(strings.TrimSuffix(id, CONTACTSUFFIX), GROUPSUFFIX)
}

// GetIdShort resolves a contact or chat ID to a short display name.
func (md *MessageDatabase) GetIdShort(id string) string {
	if id == "" {
		return "Unknown"
	}

	md.contactLock.RLock()
	contact, ok := md.contacts[id]
	md.contactLock.RUnlock()
	if ok {
		if contact.Short != "" {
			return contact.Short
		}
		if contact.Name != "" {
			return contact.Name
		}
	}

	md.chatLock.RLock()
	chat, ok := md.chats[id]
	md.chatLock.RUnlock()
	if ok && chat.Name != "" {
		return chat.Name
	}

	return strings.TrimSuffix(strings.TrimSuffix(id, CONTACTSUFFIX), GROUPSUFFIX)
}
