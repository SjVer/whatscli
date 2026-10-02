package main

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/normen/whatscli/messages"
	"github.com/rivo/tview"
)

// Replies: the reply key starts a reply to the selected message, which is
// shown as a notice below the chat until the reply is sent or Escape cancels it.

// replyTarget is the ID of the message the typed message replies to, or ""
var replyTarget string

// replyNotice is the key of the notice of the message that is replied to
const replyNotice = "reply"

// excerpt returns the first line of a text, shortened to about length characters
func excerpt(text string, length int) string {
	line, _, more := strings.Cut(text, "\n")
	if runes := []rune(line); len(runes) > length {
		return string(runes[:length]) + "…"
	} else if more {
		return line + " …"
	}
	return line
}

// replyLine returns the line above a reply that shows what it replies to
func replyLine(reply *messages.Reply) string {
	return "[gray::-]↱ [::b]" + nameText(reply.SenderId, reply.Name) + ":[::-] " + tview.Escape(excerpt(reply.Text, 50)) + "[-::-]\n"
}

// starts a reply to the selected message, which is typed in the input
func handleMessageReply(ev *tcell.EventKey) *tcell.EventKey {
	hls := textView.GetHighlights()
	if len(hls) == 0 {
		return nil
	}
	for _, msg := range chatMessages {
		if msg.Id != hls[0] {
			continue
		}
		name := msg.ContactShort
		if msg.FromMe {
			name = "yourself"
		}
		replyTarget = msg.Id
		showNotice(currentReceiver.Id, replyNotice, "Replying to "+name+": "+excerpt(msg.Text, 50)+" (Esc to cancel)")
	}
	ResetMsgSelection()
	app.SetFocus(textInput)
	return nil
}

// cancelReply stops replying, e.g. when the reply was sent
func cancelReply() {
	if replyTarget != "" {
		replyTarget = ""
		showNotice(currentReceiver.Id, replyNotice, "")
	}
}

// sendMessage sends the typed message to the open chat, as a reply if one was started
func sendMessage(text string) {
	text = replaceShortcodes(text)
	command := messages.Command{Name: "send", Params: []string{currentReceiver.Id, text}}
	if replyTarget != "" {
		command = messages.Command{Name: "reply", Params: []string{currentReceiver.Id, replyTarget, text}}
		cancelReply()
	}
	sessionManager.CommandChannel <- command
}
