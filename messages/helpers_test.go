package messages

import (
	"io"
	"time"

	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// recordingUi keeps what the session manager shows: the last chat list and
// how often it was shown, the printed text and errors, and the notices by
// chat and key
type recordingUi struct {
	chatLists int
	chats     []Chat
	printed   []string
	errors    []error
	notices   map[string]string
}

func (u *recordingUi) NewMessage(Message)               {}
func (u *recordingUi) NewScreen(string, []Message)      {}
func (u *recordingUi) PrintError(err error)             { u.errors = append(u.errors, err) }
func (u *recordingUi) PrintText(text string)            { u.printed = append(u.printed, text) }
func (u *recordingUi) SetStatus(SessionStatus)          {}
func (u *recordingUi) OpenFile(string)                  {}
func (u *recordingUi) CloseChat(string)                 {}
func (u *recordingUi) GetWriter() io.Writer             { return io.Discard }
func (u *recordingUi) SetNotice(chatID, key, text string) {
	if u.notices == nil {
		u.notices = map[string]string{}
	}
	u.notices[chatID+"/"+key] = text
}
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

// newTestDB returns an empty message database
func newTestDB() *MessageDatabase {
	db := &MessageDatabase{}
	db.Init()
	return db
}
