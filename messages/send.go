package messages

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/normen/whatscli/config"
	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// sendText sends a message, as a reply to the message with the ID replyID, if any
func (sm *SessionManager) sendText(wid, text, replyID string) {
	client, err := sm.connectedClient()
	if err != nil {
		sm.uiHandler.PrintError(err)
		return
	}

	receiver, err := types.ParseJID(wid)
	if err != nil {
		sm.uiHandler.PrintError(fmt.Errorf("invalid JID: %v", err))
		return
	}

	raw := &waProto.Message{Conversation: proto.String(text)}
	sent, mentioned, typed := resolveMentions(text, sm.GroupMembers(wid))
	var reply *Reply
	ctx := &waProto.ContextInfo{}
	if replyID != "" {
		quoted, ok := sm.db.GetMessage(replyID)
		if !ok {
			sm.uiHandler.PrintError(errors.New("the message to reply to isn't loaded anymore, nothing was sent"))
			return
		}
		ctx = replyContext(quoted)
		reply = replyTo(quoted)
	}
	if len(mentioned) > 0 || reply != nil {
		ctx.MentionedJID = mentioned
		raw = &waProto.Message{ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text:        proto.String(sent),
			ContextInfo: ctx,
		}}
	}
	resp, err := client.SendMessage(context.Background(), receiver, raw)
	if err != nil {
		sm.uiHandler.PrintError(fmt.Errorf("failed to send message: %v", err))
		return
	}

	newMsg := sm.outgoingMessageFromSendResponse(resp, wid, raw, MessageKindText, text, "", "")
	newMsg.Mentions = typed
	newMsg.ReplyTo = reply
	sm.messageSent(newMsg)
}

// messageSent shows a message that was sent, and marks its chat as read if configured.
func (sm *SessionManager) messageSent(msg Message) {
	sm.db.AddMessage(msg, false)
	if sm.openChat() == msg.ChatId {
		sm.uiHandler.NewMessage(msg)
	}
	sm.uiHandler.SetChats(sm.db.GetChatIds())
	if config.Config.General.MarkReadOnSend {
		if _, err := sm.markChatRead(msg.ChatId); err != nil {
			sm.uiHandler.PrintError(err)
		}
	}
}

// optionalString returns a pointer to text, or nil if it is empty
func optionalString(text string) *string {
	if text == "" {
		return nil
	}
	return &text
}

// sendMedia sends a file as a message of the kind, images and videos with a caption
func (sm *SessionManager) sendMedia(chatID, path string, kind MessageKind, caption string) error {
	client, err := sm.connectedClient()
	if err != nil {
		return err
	}

	data, mimeType, fileName, err := readUploadFile(path)
	if err != nil {
		return err
	}

	receiver, err := types.ParseJID(chatID)
	if err != nil {
		return fmt.Errorf("invalid JID: %v", err)
	}

	uploadResp, err := client.Upload(context.Background(), data, uploadMediaType(kind))
	if err != nil {
		return fmt.Errorf("failed to upload file: %v", err)
	}

	fileLength := uploadResp.FileLength
	raw := &waProto.Message{}
	switch kind {
	case MessageKindImage:
		raw.ImageMessage = &waProto.ImageMessage{
			Caption:       optionalString(caption),
			Mimetype:      proto.String(mimeType),
			URL:           &uploadResp.URL,
			DirectPath:    &uploadResp.DirectPath,
			MediaKey:      uploadResp.MediaKey,
			FileEncSHA256: uploadResp.FileEncSHA256,
			FileSHA256:    uploadResp.FileSHA256,
			FileLength:    &fileLength,
		}
	case MessageKindVideo:
		raw.VideoMessage = &waProto.VideoMessage{
			Caption:       optionalString(caption),
			Mimetype:      proto.String(mimeType),
			URL:           &uploadResp.URL,
			DirectPath:    &uploadResp.DirectPath,
			MediaKey:      uploadResp.MediaKey,
			FileEncSHA256: uploadResp.FileEncSHA256,
			FileSHA256:    uploadResp.FileSHA256,
			FileLength:    &fileLength,
		}
	case MessageKindAudio:
		raw.AudioMessage = &waProto.AudioMessage{
			Mimetype:      proto.String(mimeType),
			URL:           &uploadResp.URL,
			DirectPath:    &uploadResp.DirectPath,
			MediaKey:      uploadResp.MediaKey,
			FileEncSHA256: uploadResp.FileEncSHA256,
			FileSHA256:    uploadResp.FileSHA256,
			FileLength:    &fileLength,
			PTT:           proto.Bool(false),
		}
	case MessageKindDocument:
		raw.DocumentMessage = &waProto.DocumentMessage{
			Mimetype:      proto.String(mimeType),
			Title:         proto.String(fileName),
			FileName:      proto.String(fileName),
			URL:           &uploadResp.URL,
			DirectPath:    &uploadResp.DirectPath,
			MediaKey:      uploadResp.MediaKey,
			FileEncSHA256: uploadResp.FileEncSHA256,
			FileSHA256:    uploadResp.FileSHA256,
			FileLength:    &fileLength,
		}
	default:
		return errors.New("unsupported media type")
	}

	resp, err := client.SendMessage(context.Background(), receiver, raw)
	if err != nil {
		return fmt.Errorf("failed to send media message: %v", err)
	}

	text := mediaDisplayText(kind, fileName, caption)
	newMsg := sm.outgoingMessageFromSendResponse(resp, chatID, raw, kind, text, mimeType, fileName)
	sm.messageSent(newMsg)
	return nil
}

func (sm *SessionManager) outgoingMessageFromSendResponse(resp whatsmeow.SendResponse, chatID string, raw *waProto.Message, kind MessageKind, text, mimeType, fileName string) Message {
	client := sm.client()
	selfID := ""
	if client != nil && client.Store != nil && client.Store.ID != nil {
		selfID = client.Store.ID.ToNonAD().String() // like the user's messages from other devices
	}

	contactID := chatID
	if isGroupID(chatID) {
		contactID = selfID
	}

	return Message{
		Id:           string(resp.ID),
		ChatId:       chatID,
		SenderId:     selfID,
		ContactId:    contactID,
		ContactName:  sm.db.GetIdName(contactID),
		ContactShort: sm.db.GetIdShort(contactID),
		Timestamp:    uint64(resp.Timestamp.Unix()),
		FromMe:       true,
		Text:         text,
		Kind:         kind,
		MimeType:     mimeType,
		FileName:     fileName,
		RawMessage:   raw,
	}
}

func readUploadFile(path string) ([]byte, string, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", "", err
	}
	fileName := filepath.Base(path)
	mimeType := detectMimeType(path, data)
	return data, mimeType, fileName, nil
}

func detectMimeType(path string, data []byte) string {
	if len(data) == 0 {
		if extType := mime.TypeByExtension(filepath.Ext(path)); extType != "" {
			return stripMimeParams(extType)
		}
		return "application/octet-stream"
	}
	sample := data
	if len(sample) > 512 {
		sample = sample[:512]
	}
	detected := stripMimeParams(http.DetectContentType(sample))
	if extType := mime.TypeByExtension(filepath.Ext(path)); extType != "" {
		extType = stripMimeParams(extType)
		if detected == "application/octet-stream" || strings.HasPrefix(extType, "audio/") || strings.HasPrefix(extType, "video/") {
			return extType
		}
	}
	return detected
}

func stripMimeParams(value string) string {
	if idx := strings.Index(value, ";"); idx >= 0 {
		return value[:idx]
	}
	return value
}

func uploadMediaType(kind MessageKind) whatsmeow.MediaType {
	switch kind {
	case MessageKindImage:
		return whatsmeow.MediaImage
	case MessageKindVideo:
		return whatsmeow.MediaVideo
	case MessageKindAudio:
		return whatsmeow.MediaAudio
	default:
		return whatsmeow.MediaDocument
	}
}

func commandNameForKind(kind MessageKind) string {
	switch kind {
	case MessageKindImage:
		return "sendimage"
	case MessageKindVideo:
		return "sendvideo"
	case MessageKindAudio:
		return "sendaudio"
	default:
		return "upload"
	}
}
