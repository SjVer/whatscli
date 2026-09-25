// this package manages the messages
package messages

import (
	"io"

	waProto "go.mau.fi/whatsmeow/binary/proto"
)

// TODO: move these funcs/interface to channels
type UiMessageHandler interface {
	NewMessage(Message)
	NewScreen([]Message)
	SetChats([]Chat)
	PrintError(error)
	PrintText(string)
	PrintFile(string)
	SetStatus(SessionStatus)
	OpenFile(string)
	GetWriter() io.Writer
}

// data struct for current session status
type SessionStatus struct {
	Connected bool
	LastSeen  string
	// what whatscli is waiting for, e.g. messages from the phone
	Activity string
}

// message struct for status messages
type StatusMsg struct {
	connected bool
	err       error
}

// message object for commands
type Command struct {
	Name   string
	Params []string
}

type MessageKind string

const (
	MessageKindText     MessageKind = "text"
	MessageKindImage    MessageKind = "image"
	MessageKindVideo    MessageKind = "video"
	MessageKindAudio    MessageKind = "audio"
	MessageKindDocument MessageKind = "document"
	MessageKindUnknown  MessageKind = "unknown"
)

// internal message representation to abstract from message lib
type Message struct {
	Id           string
	ChatId       string // the source of the message (group id or contact id)
	SenderId     string
	ContactId    string
	ContactName  string
	ContactShort string
	Timestamp    uint64
	FromMe       bool
	Forwarded    bool
	Text         string
	Kind         MessageKind
	MimeType     string
	FileName     string
	Unread       bool
	RawMessage   *waProto.Message `json:"-"` // saved as SavedMessage.Raw
	// emoji reactions by who reacted, see ReactorMe
	Reactions map[string]string `json:",omitempty"`
}

// ReactorMe is the key of the user's own reaction in Message.Reactions
const ReactorMe = "me"

// SavedMessage is a message as it is saved with its chat.
type SavedMessage struct {
	Message
	Raw []byte `json:",omitempty"`
}

// internal contact representation to abstract from message lib
type Chat struct {
	Id      string
	IsGroup bool
	Name    string
	Unread  int
	//TODO: convert to uint64
	LastMessage int64
	Pinned      bool
	// time when the chat was pinned, the last pinned chat is listed first
	PinnedAt int64
	// whether the chat was archived on the phone, see InArchive
	Archived bool
	// time of the last message that wasn't sent by the user
	LastIncoming int64
	// time of the last message when the chat was archived
	ArchivedAt int64
	// time of the last message when the chat was deleted
	DeletedAt int64
	// the newest messages, only used to save and load them: the phone only
	// sends messages before a message it knows, see RequestChatHistory
	Recent []SavedMessage `json:",omitempty"`
	// set by GetChatIds for chats that WhatsApp doesn't list: chats without
	// messages, e.g. contacts that were never written to, or deleted chats
	Hidden bool `json:"-"`
	// set by GetChatIds for chats that are still archived, see GetChatIds
	InArchive bool `json:"-"`
}

type Contact struct {
	Id    string
	Name  string
	Short string
}

const GROUPSUFFIX = "@g.us"
const CONTACTSUFFIX = "@s.whatsapp.net"
const STATUSSUFFIX = "status@broadcast"
