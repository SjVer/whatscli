package main

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/normen/whatscli/messages"
	"github.com/rivo/tview"
)

func TestNoticesAreShownDimBelowTheirChat(t *testing.T) {
	notices = map[string][]notice{}
	textView = tview.NewTextView().SetDynamicColors(true).SetRegions(true)
	messageSearch = ""
	currentReceiver = messages.Chat{Id: "alice"}
	chatMessages = []messages.Message{{Id: "1", ChatId: "alice", ContactShort: "Alice", Timestamp: 1000, Text: "hi"}}

	setNotice("alice", "history", "Your phone didn't send older messages, is it online?")
	setNotice("bob", "history", "Your phone has no older messages")
	renderMessages()
	text := textView.GetText(true)
	if !strings.HasSuffix(text, "hi\n\nYour phone didn't send older messages, is it online?\n") {
		t.Fatalf("expected the notice on its own line below an empty line, got %q", text)
	}
	if strings.Contains(text, "no older messages") {
		t.Error("expected the notice of another chat not to be shown")
	}

	screen := newScreen(t, 80, 10)
	textView.SetRect(0, 0, 80, 10)
	textView.Draw(screen)
	for y := 0; y < 10; y++ {
		if r, _, style, _ := screen.GetContent(0, y); r == 'Y' {
			if _, _, attr := style.Decompose(); attr&tcell.AttrDim == 0 {
				t.Error("expected the notice to be dim")
			}
		}
	}

	// a newer notice replaces it, and an empty one removes it
	if !setNotice("alice", "history", "Your phone has no older messages") || len(notices["alice"]) != 1 {
		t.Fatalf("expected the notice to be replaced, got %v", notices["alice"])
	}
	if !setNotice("alice", "history", "") || len(notices["alice"]) != 0 {
		t.Fatalf("expected the notice to be removed, got %v", notices["alice"])
	}
	if setNotice("alice", "history", "") {
		t.Error("expected removing a missing notice to change nothing")
	}
	renderMessages()
	if text := textView.GetText(true); strings.Contains(text, "phone") {
		t.Errorf("expected no notice after removing it, got %q", text)
	}
}
