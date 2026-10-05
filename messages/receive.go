package messages

import (
	"strings"
	"time"

	"github.com/normen/whatscli/config"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func mediaDisplayText(kind MessageKind, fileName, caption string) string {
	label := "[FILE]"
	switch kind {
	case MessageKindImage:
		label = "[IMAGE]"
	case MessageKindVideo:
		label = "[VIDEO]"
	case MessageKindAudio:
		label = "[AUDIO]"
	case MessageKindDocument:
		label = "[DOCUMENT]"
	case MessageKindSticker:
		label = "[STICKER]"
	}
	parts := []string{label}
	if fileName != "" && kind == MessageKindDocument {
		parts = append(parts, fileName)
	}
	if caption != "" {
		parts = append(parts, caption)
	}
	return strings.Join(parts, " ")
}

// isNewReaction returns whether a reaction counts as new: like the phone
// notifies them, a reaction of someone else to one of the user's messages
func (eh *eventHandler) isNewReaction(reaction Message) bool {
	if reaction.SenderId == ReactorMe || reaction.Text == "" {
		return false
	}
	target, ok := eh.sm.db.GetMessage(reaction.Id)
	return ok && target.FromMe
}

func (eh *eventHandler) handleLiveMessage(evt *events.Message) {
	msg, action, ok := eh.normalizeEventMessage(evt)
	if !ok {
		return
	}
	// messages that arrived while whatscli was closed are shown when all arrived
	show := eh.sm.offlineMessages.Load() == 0

	switch action {
	case "react":
		chatID, ok := eh.sm.db.SetReaction(msg.Id, msg.SenderId, msg.Text, int64(msg.Timestamp))
		if ok && show {
			eh.sm.showIfOpen(chatID)
		}
		if ok && !eh.sm.seesChat(chatID) && eh.isNewReaction(msg) {
			eh.sm.db.AddUnreadReaction(chatID, int64(msg.Timestamp))
			if show {
				eh.sm.uiHandler.SetChats(eh.sm.db.GetChatIds())
			}
		}
		return
	case "revoke":
		if eh.sm.db.MarkMessageRevoked(msg.Id) && show {
			eh.sm.showIfOpen(msg.ChatId)
		}
		if show {
			eh.sm.uiHandler.SetChats(eh.sm.db.GetChatIds())
		}
		return
	case "ignore":
		return
	}

	markUnread := !msg.FromMe && !eh.sm.seesChat(msg.ChatId)
	isNew := eh.sm.db.AddMessage(msg, markUnread)
	if msg.ChatId == eh.sm.openChat() {
		eh.sm.describeChat(msg.ChatId)
	}
	if stored, ok := eh.sm.db.GetMessage(msg.Id); ok {
		// it may have been read on the phone already
		markUnread = markUnread && stored.Unread
	}
	if msg.ChatId == eh.sm.openChat() && show {
		if isNew {
			eh.sm.uiHandler.NewMessage(msg)
		} else {
			eh.sm.uiHandler.NewScreen(msg.ChatId, eh.sm.db.GetMessages(msg.ChatId))
		}
	}
	// also for the open chat while the user isn't looking at it
	if markUnread && isNew && msg.Timestamp > uint64(time.Now().Unix()-30) {
		// showing it can take a moment, e.g. on Windows, which starts PowerShell
		title, text := notificationText(msg, eh.sm.db.GetIdName(msg.ChatId))
		go func() {
			picture := ""
			if config.Config.General.EnableNotifications && !config.Config.General.UseTerminalBell {
				picture = eh.sm.chatPicture(msg.ChatId)
			}
			if err := sendNotification(title, text, picture); err != nil {
				eh.sm.uiHandler.PrintError(err)
			}
		}()
	}
	if show {
		eh.sm.uiHandler.SetChats(eh.sm.db.GetChatIds())
	}
}

func (eh *eventHandler) handleHistorySync(evt *events.HistorySync) {
	if evt == nil || evt.Data == nil {
		return
	}

	for _, mapping := range evt.Data.GetPhoneNumberToLidMappings() {
		eh.sm.learnLID(mapping.GetLidJID(), mapping.GetPnJID())
	}

	var chatIDs []string
	messageCount := 0
	learnedNames := false
	// Messages requested from the phone when a chat is opened are older ones, and
	// their unread count isn't the current one: that follows from the messages
	// received since, and the chats read on other devices, see handleReceipt.
	onDemand := evt.Data.GetSyncType() == waHistorySync.HistorySync_ON_DEMAND
	for _, conv := range evt.Data.GetConversations() {
		// a conversation has either a LID with its phone number, or the other way around
		eh.sm.learnLID(conv.GetID(), conv.GetPnJID())
		eh.sm.learnLID(conv.GetLidJID(), conv.GetID())
		chatID := conv.GetID()
		if chatID == "" {
			chatID = conv.GetNewJID()
		}
		if chatID == "" {
			continue
		}

		chatJID, err := types.ParseJID(chatID)
		if err != nil {
			continue
		}
		// store one-to-one chats under the phone number, like the contact list
		chatID = eh.sm.chatIdForJID(chatJID)
		chatIDs = append(chatIDs, chatID)

		chatName := conv.GetName()
		if chatName == "" {
			chatName = conv.GetDisplayName()
		}
		if chatName == "" {
			if mappedJID, err := types.ParseJID(chatID); err == nil {
				chatName = eh.sm.getChatName(mappedJID)
			}
		}
		if isFallbackName(chatID, chatName) {
			chatName = "" // no name found, keep the one from the contacts
		}

		lastMessage := int64(conv.GetLastMsgTimestamp())
		if lastMessage == 0 {
			lastMessage = int64(conv.GetConversationTimestamp())
		}
		unread := int(conv.GetUnreadCount())
		if onDemand {
			unread = 0
		}
		eh.sm.db.AddChat(Chat{
			Id:          chatID,
			IsGroup:     chatJID.Server == types.GroupServer,
			Name:        chatName,
			Unread:      unread,
			LastMessage: lastMessage,
		})

		for _, histMsg := range conv.GetMessages() {
			webMsg := histMsg.GetMessage()
			if webMsg == nil {
				continue
			}
			parsed, err := eh.sm.client().ParseWebMessage(chatJID, webMsg)
			if err != nil {
				continue
			}
			if !parsed.Info.IsFromMe && eh.sm.learnPushName(parsed.Info.Sender, webMsg.GetPushName()) {
				learnedNames = true
			}
			msg, action, ok := eh.normalizeEventMessage(parsed)
			if ok && action == "react" {
				eh.sm.db.SetReaction(msg.Id, msg.SenderId, msg.Text, int64(msg.Timestamp))
				continue
			}
			if !ok || action != "" {
				continue
			}
			// the LID of the chat may not be mapped to its phone number yet
			msg.ChatId = chatID
			if msg.FromMe {
				msg.Status = historyStatus(webMsg.GetStatus())
			}
			eh.sm.db.AddMessage(msg, false)
			messageCount++
			for _, reaction := range webMsg.GetReactions() {
				eh.sm.db.SetReaction(msg.Id, eh.sm.reactorFromKey(reaction.GetKey(), chatJID), reaction.GetText(), reaction.GetSenderTimestampMS()/1000)
			}
		}
		if !onDemand {
			eh.sm.db.UpdateChatUnread(chatID, unread)
		}

		// The phone knows whether the chat is archived, which the app state alone
		// doesn't tell, see GetChatIds. A conversation can be sent in several parts,
		// some without messages and time, which say nothing about it.
		if lastMessage > 0 {
			if conv.GetArchived() {
				eh.sm.db.SetChatArchived(chatID, true, lastMessage)
			} else {
				eh.sm.db.SetChatUnarchived(chatID, lastMessage)
			}
		}
		eh.sm.logDebug("History conversation %s: archived=%v pinned=%v last message %s, %d messages",
			chatID, conv.GetArchived(), conv.GetPinned() > 0, formatTimestamp(lastMessage), len(conv.GetMessages()))
	}

	if learnedNames {
		// of people who aren't in the contacts, which takes a moment, so beside
		// the events that keep arriving
		go func() {
			eh.sm.addContactChats()
			eh.sm.refreshContactNames()
			eh.sm.uiHandler.SetChats(eh.sm.db.GetChatIds())
		}()
	}
	eh.sm.uiHandler.SetChats(eh.sm.db.GetChatIds())
	eh.sm.showIfOpen(eh.sm.openChat())
	if cs := eh.sm.getChatSync(); cs != nil {
		cs.onHistory(evt.Data, chatIDs, messageCount)
	}
	if onDemand {
		// an answer without messages doesn't say for which chat
		if len(chatIDs) == 0 {
			eh.sm.logDebug("Empty answer of the phone, which finishes all requested chats")
			eh.sm.finishAllHistoryRequests()
		}
		for _, chatID := range chatIDs {
			eh.sm.finishHistoryRequest(chatID)
		}
	}
}

func (eh *eventHandler) normalizeEventMessage(evt *events.Message) (Message, string, bool) {
	if evt == nil || evt.Message == nil {
		return Message{}, "ignore", false
	}

	// a reaction carries the message it reacts to in Id, the emoji in Text and who reacted in SenderId
	if reaction := evt.Message.GetReactionMessage(); reaction != nil {
		return Message{
			Id:        reaction.GetKey().GetID(),
			ChatId:    eh.sm.chatIdForJID(evt.Info.Chat),
			Text:      reaction.GetText(),
			SenderId:  eh.sm.reactorID(evt.Info.Sender, evt.Info.IsFromMe),
			Timestamp: uint64(evt.Info.Timestamp.Unix()),
		}, "react", true
	}

	if protocol := evt.Message.GetProtocolMessage(); protocol != nil {
		if protocol.GetType() == waProto.ProtocolMessage_REVOKE && protocol.GetKey() != nil {
			return Message{
				Id:     protocol.GetKey().GetID(),
				ChatId: eh.sm.chatIdForJID(evt.Info.Chat),
			}, "revoke", true
		}
		return Message{}, "ignore", false
	}

	msg, ok := eh.messageFromInfo(evt.Info, evt.Message)
	return msg, "", ok
}

func (eh *eventHandler) messageFromInfo(info types.MessageInfo, raw *waProto.Message) (Message, bool) {
	if raw == nil {
		return Message{}, false
	}

	if info.Chat.IsEmpty() {
		return Message{}, false
	}
	// store one-to-one chats under the phone number, like the contact list
	chatID := eh.sm.chatIdForJID(info.Chat)

	contactID, contactName, contactShort := eh.contactForMessage(info)
	msg := Message{
		Id:           string(info.ID),
		ChatId:       chatID,
		SenderId:     info.Sender.String(),
		ContactId:    contactID,
		ContactName:  contactName,
		ContactShort: contactShort,
		Timestamp:    uint64(info.Timestamp.Unix()),
		FromMe:       info.IsFromMe,
		RawMessage:   raw,
	}

	switch {
	case raw.GetConversation() != "":
		msg.Kind = MessageKindText
		msg.Text = raw.GetConversation()
	case raw.GetExtendedTextMessage() != nil:
		ext := raw.GetExtendedTextMessage()
		msg.Kind = MessageKindText
		msg.Text, msg.Mentions = eh.sm.showMentions(ext.GetText(), ext.GetContextInfo().GetMentionedJID())
	case raw.GetImageMessage() != nil:
		image := raw.GetImageMessage()
		msg.Kind = MessageKindImage
		msg.MimeType = image.GetMimetype()
		msg.Text = mediaDisplayText(MessageKindImage, "", image.GetCaption())
	case raw.GetVideoMessage() != nil:
		video := raw.GetVideoMessage()
		msg.Kind = MessageKindVideo
		msg.MimeType = video.GetMimetype()
		msg.Text = mediaDisplayText(MessageKindVideo, "", video.GetCaption())
	case raw.GetAudioMessage() != nil:
		msg.Kind = MessageKindAudio
		msg.MimeType = raw.GetAudioMessage().GetMimetype()
		msg.Text = mediaDisplayText(MessageKindAudio, "", "")
	case raw.GetDocumentMessage() != nil:
		doc := raw.GetDocumentMessage()
		msg.Kind = MessageKindDocument
		msg.MimeType = doc.GetMimetype()
		msg.FileName = doc.GetFileName()
		msg.Text = mediaDisplayText(MessageKindDocument, doc.GetFileName(), doc.GetCaption())
	case raw.GetStickerMessage() != nil:
		sticker := raw.GetStickerMessage()
		msg.Kind = MessageKindSticker
		msg.MimeType = sticker.GetMimetype()
		msg.Text = mediaDisplayText(MessageKindSticker, "", "")
		if sticker.GetIsAnimated() {
			msg.Text = "[ANIMATED STICKER]"
		}
		// the description of its maker is used instead of one by the model, see describeChat
		msg.AltText = strings.TrimSpace(sticker.GetAccessibilityLabel())
	default:
		return Message{}, false
	}
	ctx := contextInfo(raw)
	msg.Forwarded = ctx.GetIsForwarded()
	msg.ReplyTo = eh.replyOf(info, ctx)
	return msg, true
}

func (eh *eventHandler) contactForMessage(info types.MessageInfo) (string, string, string) {
	if info.IsGroup {
		return eh.sm.contactNames(info.Sender)
	}
	return eh.sm.contactNames(info.Chat)
}
