package main

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/normen/whatscli/messages"
	"github.com/rivo/tview"
)

func TestReplyIsShownAboveTheMessage(t *testing.T) {
	defer func(msgs []messages.Message) { chatMessages = msgs }(chatMessages)
	chatMessages = nil // the replied message isn't loaded
	msg := messages.Message{Id: "2", ContactShort: "Alice", Timestamp: 1000, Text: "yes!",
		ReplyTo: &messages.Reply{Id: "1", Name: "Bob", Text: "dinner at 7?\nor later"}}
	screen := drawText(t, getTextMessageString(&msg, nil, 0))
	row := func(y int) string {
		text := ""
		for x := 0; x < 40; x++ {
			r, _, _, _ := screen.GetContent(x, y)
			text += string(r)
		}
		return strings.TrimSpace(text)
	}
	if row(1) != "↱ Bob: dinner at 7? …" || row(2) != "yes!" {
		t.Errorf("expected the replied message above the reply, got %q and %q", row(1), row(2))
	}
}

func TestReplyingToASelectedMessage(t *testing.T) {
	defer func(view *tview.TextView, input *tview.TextArea, root *tview.TreeNode, chat messages.Chat, msgs []messages.Message) {
		sessionManager, app, notices = nil, nil, map[string][]notice{}
		textView, textInput, chatRoot, currentReceiver, chatMessages = view, input, root, chat, msgs
	}(textView, textInput, chatRoot, currentReceiver, chatMessages)
	sessionManager = &messages.SessionManager{CommandChannel: make(chan messages.Command, 10)}
	app = tview.NewApplication()
	notices = map[string][]notice{}
	chatRoot = tview.NewTreeNode("Chats")
	textInput = newTextInput()
	textView = tview.NewTextView().SetDynamicColors(true).SetRegions(true)
	currentReceiver = messages.Chat{Id: "family"}
	chatMessages = []messages.Message{{Id: "m1", ChatId: "family", ContactShort: "Bob", Text: "dinner at 7?"}}
	renderMessages()
	textView.Highlight("m1")

	handleMessageReply(nil)
	if replyTarget != "m1" || !strings.Contains(textView.GetText(true), "Replying to Bob: dinner at 7?") {
		t.Fatalf("expected the reply to be shown, got %q", textView.GetText(true))
	}
	typeText("yes")
	typeKeys(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	command := <-sessionManager.CommandChannel
	if command.Name != "reply" || strings.Join(command.Params, "|") != "family|m1|yes" {
		t.Fatalf("expected a reply to be sent, got %+v", command)
	}
	if replyTarget != "" || strings.Contains(textView.GetText(true), "Replying") {
		t.Error("expected the reply to be done once sent")
	}

	// Escape cancels it
	textView.Highlight("m1")
	handleMessageReply(nil)
	typeKeys(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
	if replyTarget != "" || strings.Contains(textView.GetText(true), "Replying") {
		t.Error("expected Escape to cancel the reply")
	}
}

func TestRepliesToImagesShowTheirAltText(t *testing.T) {
	defer func(msgs []messages.Message) { chatMessages = msgs }(chatMessages)
	chatMessages = []messages.Message{{Id: "img", Text: "[IMAGE] look", AltText: "a dog on a beach", Kind: messages.MessageKindImage}}
	line := replyLine(&messages.Reply{Id: "img", Name: "Bob", Text: "[IMAGE] look"})
	if !strings.Contains(line, "Bob:[::-] [IMAGE: a dog on a beach[] look") {
		t.Errorf("expected the alt text in the reply, got %q", line)
	}
	// a message that isn't loaded shows as it was quoted
	if line := replyLine(&messages.Reply{Id: "old", Name: "Bob", Text: "[IMAGE]"}); !strings.Contains(line, "[IMAGE[]") {
		t.Errorf("expected the quoted text, got %q", line)
	}
}
