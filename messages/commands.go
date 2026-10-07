package messages

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/normen/whatscli/config"
	"github.com/rivo/tview"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

var urlPattern = regexp.MustCompile(`https?://[^\s]+`)

func (sm *SessionManager) execCommand(command Command) {
	switch command.Name {
	default:
		sm.uiHandler.PrintText("[" + config.Config.Colors.Negative + "]Unknown command: [-]" + tview.Escape(command.Name))
	case "backlog":
		sm.loadBacklog()
	case "login", "connect":
		err := sm.login()
		if err != nil {
			sm.uiHandler.PrintError(fmt.Errorf("WhatsApp connection failed: %v", err))
			sm.uiHandler.PrintText("Try using /reset to completely reset the connection")
		} else {
			sm.uiHandler.SetNotice("", connectionNotice, "")
		}
	case "reset":
		sm.resetSession()
	case "disconnect":
		sm.uiHandler.PrintError(sm.disconnect())
	case "logout":
		sm.uiHandler.PrintError(sm.logout())
	case "send":
		if checkParam(command.Params, 2) {
			sm.sendText(command.Params[0], strings.Join(command.Params[1:], " "), "")
		} else {
			sm.printCommandUsage("send", "[chat-id[] [message text[]")
		}
	case "reply":
		if checkParam(command.Params, 3) {
			sm.sendText(command.Params[0], strings.Join(command.Params[2:], " "), command.Params[1])
		} else {
			sm.printCommandUsage("reply", "[chat-id[] [message-id[] [message text[]")
		}
	case "select":
		if checkParam(command.Params, 1) {
			sm.setCurrentReceiver(command.Params[0])
		} else {
			sm.printCommandUsage("select", "[chat-id[]")
		}
	case "read":
		sm.markCurrentChatRead()
	case "archive", "unarchive":
		// from the archive key of the chat list
		sm.setChatArchived(command.Params, command.Name == "archive")
	case "info":
		if checkParam(command.Params, 1) {
			sm.uiHandler.PrintText(sm.db.GetMessageInfo(command.Params[0]) + sm.receiptInfo(command.Params[0]) + sm.reactionInfo(command.Params[0]))
		} else {
			sm.printCommandUsage("info", "[message-id[]")
		}
	case "download", "open":
		// downloads can take a while, the other commands don't wait for them
		go sm.downloadCommand(command.Params, command.Name == "open")
	case "url":
		sm.openMessageURL(command.Params)
	case "upload":
		sm.sendMediaCommand(command.Params, MessageKindDocument)
	case "sendimage":
		sm.sendMediaCommand(command.Params, MessageKindImage)
	case "pasteimage":
		// an image from the clipboard, saved by the UI, with a caption
		if checkParam(command.Params, 2) {
			sm.uiHandler.PrintError(sm.sendMedia(command.Params[0], command.Params[1], MessageKindImage, strings.Join(command.Params[2:], " ")))
		} else {
			sm.printCommandUsage("pasteimage", "[chat-id[] [/path/to/image[] [caption[]")
		}
	case "sendvideo":
		sm.sendMediaCommand(command.Params, MessageKindVideo)
	case "sendaudio":
		sm.sendMediaCommand(command.Params, MessageKindAudio)
	case "revoke":
		sm.revokeMessage(command.Params)
	case "react":
		sm.sendReaction(command.Params)
	case "leave":
		sm.leaveCurrentGroup()
	case "create":
		sm.createGroup(command.Params)
	case "add":
		sm.updateCurrentGroupParticipants(command.Params, whatsmeow.ParticipantChangeAdd, "add", "added new members")
	case "remove":
		sm.updateCurrentGroupParticipants(command.Params, whatsmeow.ParticipantChangeRemove, "remove", "removed members")
	case "admin":
		sm.updateCurrentGroupParticipants(command.Params, whatsmeow.ParticipantChangePromote, "admin", "promoted members")
	case "removeadmin":
		sm.updateCurrentGroupParticipants(command.Params, whatsmeow.ParticipantChangeDemote, "removeadmin", "demoted members")
	case "subject":
		sm.updateCurrentGroupSubject(command.Params)
	case "colorlist":
		out := ""
		for idx := range tcell.ColorNames {
			out += "[" + idx + "]" + idx + "[-]\n"
		}
		sm.uiHandler.PrintText(out)
	case "relink":
		sm.relink()
	case "recap":
		// the model takes a while, the other commands don't wait for it
		go sm.recap(command.Params)
	case "ask":
		go sm.ask(command.Params)
	}
}

func (sm *SessionManager) loadBacklog() {
	if sm.openChat() == "" {
		sm.printCommandUsage("backlog", "-> only works in a chat")
		return
	}
	if err := sm.RequestChatHistory(sm.openChat()); err != nil {
		sm.uiHandler.PrintError(err)
	}
}

// downloadCommand downloads the attachment of a message: to the download path,
// or to open it with its default app, see previewDir.
func (sm *SessionManager) downloadCommand(params []string, open bool) {
	name := "download"
	if open {
		name = "open"
	}
	if !checkParam(params, 1) {
		sm.printCommandUsage(name, "[message-id[]")
		return
	}

	msg, ok := sm.db.GetMessage(params[0])
	if !ok {
		sm.uiHandler.PrintError(errors.New("message not found"))
		return
	}
	path, err := sm.downloadMessage(msg, open)
	if err != nil {
		sm.uiHandler.PrintError(err)
		return
	}
	if open {
		sm.uiHandler.OpenFile(path)
		return
	}
	sm.uiHandler.PrintText("[::d] -> " + tview.Escape(path) + "[::-]")
}

func (sm *SessionManager) openMessageURL(params []string) {
	if !checkParam(params, 1) {
		sm.printCommandUsage("url", "[message-id[]")
		return
	}
	msg, ok := sm.db.GetMessage(params[0])
	if !ok {
		sm.uiHandler.PrintError(errors.New("message not found"))
		return
	}
	url := urlPattern.FindString(msg.Text)
	if url == "" {
		sm.uiHandler.PrintText("No URL found in message")
		return
	}
	sm.uiHandler.OpenFile(url)
}

func (sm *SessionManager) sendMediaCommand(params []string, kind MessageKind) {
	if sm.openChat() == "" {
		sm.printCommandUsage(commandNameForKind(kind), "-> only works in a chat")
		return
	}
	if !checkParam(params, 1) {
		sm.printCommandUsage(commandNameForKind(kind), "/path/to/file")
		return
	}
	path := strings.Join(params, " ")
	sm.uiHandler.PrintError(sm.sendMedia(sm.openChat(), path, kind, ""))
}

func (sm *SessionManager) revokeMessage(params []string) {
	if !checkParam(params, 1) {
		sm.printCommandUsage("revoke", "[message-id[]")
		return
	}
	msg, ok := sm.db.GetMessage(params[0])
	if !ok {
		sm.uiHandler.PrintError(errors.New("message not found"))
		return
	} else if !msg.FromMe {
		sm.uiHandler.PrintError(errors.New("only your own messages can be revoked"))
		return
	}
	client, err := sm.connectedClient()
	if err != nil {
		sm.uiHandler.PrintError(err)
		return
	}
	chatJID, err := types.ParseJID(msg.ChatId)
	if err != nil {
		sm.uiHandler.PrintError(fmt.Errorf("invalid chat JID: %v", err))
		return
	}
	if _, err = client.RevokeMessage(context.Background(), chatJID, types.MessageID(msg.Id)); err != nil {
		sm.uiHandler.PrintError(err)
		return
	}
	sm.db.MarkMessageRevoked(msg.Id)
	sm.showIfOpen(msg.ChatId)
	sm.uiHandler.PrintText("revoked: " + msg.Id)
}

// sendReaction reacts to the message with the id in params[0] with the emoji in
// params[1], or removes the reaction without one
func (sm *SessionManager) sendReaction(params []string) {
	if !checkParam(params, 1) {
		sm.printCommandUsage("react", "[message-id[] [emoji[]")
		return
	}
	client, err := sm.connectedClient()
	if err != nil {
		sm.uiHandler.PrintError(err)
		return
	}
	msg, ok := sm.db.GetMessage(params[0])
	if !ok {
		sm.uiHandler.PrintError(errors.New("message not found"))
		return
	}
	chatJID, err := types.ParseJID(msg.ChatId)
	if err != nil {
		sm.uiHandler.PrintError(fmt.Errorf("invalid chat JID: %v", err))
		return
	}
	// an empty sender means a message of your own
	sender := types.EmptyJID
	if !msg.FromMe {
		if sender, err = types.ParseJID(msg.SenderId); err != nil || sender.IsEmpty() {
			sender = chatJID
		}
	}
	reaction := strings.Join(params[1:], " ")
	if _, err = client.SendMessage(context.Background(), chatJID, client.BuildReaction(chatJID, sender, types.MessageID(msg.Id), reaction)); err != nil {
		sm.uiHandler.PrintError(fmt.Errorf("failed to send reaction: %v", err))
		return
	}
	if chatID, ok := sm.db.SetReaction(msg.Id, ReactorMe, reaction, time.Now().Unix()); ok {
		sm.showIfOpen(chatID)
	}
}

func (sm *SessionManager) printCommandUsage(command, usage string) {
	sm.uiHandler.PrintText("[" + config.Config.Colors.Negative + "]Usage:[-] " + command + " " + usage)
}

// checkParam returns whether a command has at least length params
func checkParam(arr []string, length int) bool {
	return len(arr) >= length
}
