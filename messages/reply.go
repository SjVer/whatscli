package messages

import (
	"time"

	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
)

// Replies: a message that replies to another one has the ID and sender of that
// message in its context info, and a copy of it, which is shown above the reply.

// Reply is the message a message replies to, as it is shown above it
type Reply struct {
	Id string
	// who sent it, and their short name, "You" for the user
	SenderId string `json:",omitempty"`
	Name     string
	Text     string
}

// contextInfo returns the context info of a message, which says what it
// replies to, who it mentions and whether it was forwarded
func contextInfo(raw *waProto.Message) *waProto.ContextInfo {
	switch {
	case raw.GetExtendedTextMessage() != nil:
		return raw.GetExtendedTextMessage().GetContextInfo()
	case raw.GetImageMessage() != nil:
		return raw.GetImageMessage().GetContextInfo()
	case raw.GetVideoMessage() != nil:
		return raw.GetVideoMessage().GetContextInfo()
	case raw.GetAudioMessage() != nil:
		return raw.GetAudioMessage().GetContextInfo()
	case raw.GetDocumentMessage() != nil:
		return raw.GetDocumentMessage().GetContextInfo()
	case raw.GetStickerMessage() != nil:
		return raw.GetStickerMessage().GetContextInfo()
	}
	return nil
}

// replyOf returns the message that a message in the chat of info replies to,
// or nil. The copy in the reply is used when the message isn't loaded.
func (eh *eventHandler) replyOf(info types.MessageInfo, ctx *waProto.ContextInfo) *Reply {
	id := ctx.GetStanzaID()
	if id == "" {
		return nil
	}
	if quoted, ok := eh.sm.db.GetMessage(id); ok {
		return replyTo(quoted)
	}
	reply := &Reply{Id: id}
	sender, err := types.ParseJID(ctx.GetParticipant())
	if err != nil || sender.IsEmpty() {
		sender = info.Chat // in a chat with one person
	}
	if eh.sm.isOwnUser(sender) {
		reply.Name = "You"
	} else {
		reply.SenderId, _, reply.Name = eh.sm.contactNames(sender)
	}
	if quoted := ctx.GetQuotedMessage(); quoted != nil {
		quotedInfo := info
		quotedInfo.ID, quotedInfo.Sender = id, sender
		if msg, ok := eh.messageFromInfo(quotedInfo, quoted); ok {
			reply.Text = msg.Text
		}
	}
	return reply
}

// messageInfo returns the info of a message that is known, like whatsmeow gives it
func messageInfo(msg Message) types.MessageInfo {
	info := types.MessageInfo{ID: msg.Id, Timestamp: time.Unix(int64(msg.Timestamp), 0)}
	info.Chat, _ = types.ParseJID(msg.ChatId)
	info.Sender, _ = types.ParseJID(msg.SenderId)
	info.IsFromMe = msg.FromMe
	info.IsGroup = isGroupID(msg.ChatId)
	return info
}

// replyTo returns how a reply to msg shows it
func replyTo(msg Message) *Reply {
	name := msg.ContactShort
	if msg.FromMe {
		name = "You"
	}
	return &Reply{Id: msg.Id, SenderId: msg.ContactId, Name: name, Text: msg.Text}
}

// replyContext returns the context info of a reply to msg
func (sm *SessionManager) replyContext(msg Message) *waProto.ContextInfo {
	ctx := &waProto.ContextInfo{StanzaID: &msg.Id, QuotedMessage: msg.RawMessage}
	if sender, err := types.ParseJID(msg.SenderId); err == nil && !sender.IsEmpty() {
		participant := sender.ToNonAD().String()
		ctx.Participant = &participant
	}
	return ctx
}
