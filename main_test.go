package main

import (
	"bytes"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/normen/whatscli/config"
	"github.com/normen/whatscli/messages"
	"github.com/rivo/tview"
)

func TestContinuesGroup(t *testing.T) {
	withUI(t)
	first := messages.Message{Id: "1", ContactId: "alice", ContactShort: "Alice", Timestamp: 1000}
	tests := []struct {
		name     string
		msg      messages.Message
		expected bool
	}{
		{"same sender within 2 minutes", messages.Message{ContactId: "alice", Timestamp: 1120}, true},
		{"same sender after more than 2 minutes", messages.Message{ContactId: "alice", Timestamp: 1121}, false},
		{"other sender", messages.Message{ContactId: "bob", Timestamp: 1010}, false},
		{"sent by me", messages.Message{ContactId: "alice", FromMe: true, Timestamp: 1010}, false},
	}
	for _, test := range tests {
		if actual := continuesGroup(&first, &test.msg); actual != test.expected {
			t.Errorf("%s: expected %v, got %v", test.name, test.expected, actual)
		}
	}
	if continuesGroup(nil, &first) {
		t.Error("expected the first message not to continue a group")
	}
}

func TestGroupedMessagesAreShownBelowOneHeader(t *testing.T) {
	withUI(t)
	first := messages.Message{Id: "1", ContactId: "alice", ContactShort: "Alice", Timestamp: 1000, Text: "hello"}
	next := messages.Message{Id: "2", ContactId: "alice", ContactShort: "Alice", Timestamp: 1060, Text: "again"}

	firstLines := strings.Split(getTextMessageString(&first, nil, 0), "\n")
	if len(firstLines) != 2 || !strings.Contains(firstLines[0], "Alice") || !strings.HasSuffix(firstLines[1], `hello[""]`) {
		t.Fatalf("expected the time and name on their own line above the text, got %q", firstLines)
	}
	nextLine := getTextMessageString(&next, &first, 0)
	if nextLine != `["2"]again[""]` {
		t.Fatalf("expected only the text of a grouped message, without indentation, got %q", nextLine)
	}

	later := messages.Message{Id: "3", ContactId: "alice", ContactShort: "Alice", Timestamp: 2000, Text: "later"}
	laterLines := strings.Split(getTextMessageString(&later, &next, 0), "\n")
	if len(laterLines) != 3 || laterLines[0] != `["3"]` || !strings.Contains(laterLines[1], "Alice") {
		t.Fatalf("expected an empty line before the header of a new group, got %q", laterLines)
	}
}

func TestFormatMessageTime(t *testing.T) {
	withUI(t)
	now := time.Date(2026, 9, 24, 16, 30, 0, 0, time.Local)
	tests := []struct {
		sent     time.Time
		expected string
	}{
		{time.Date(2026, 9, 24, 0, 5, 0, 0, time.Local), "00:05"},
		{time.Date(2026, 9, 23, 23, 59, 0, 0, time.Local), "Wed 23:59"},
		{time.Date(2026, 9, 18, 9, 0, 0, 0, time.Local), "Fri 09:00"},
		{time.Date(2026, 9, 17, 9, 0, 0, 0, time.Local), "17 Sep 09:00"},
		{time.Date(2025, 12, 31, 9, 0, 0, 0, time.Local), "31 Dec 2025"},
	}
	for _, test := range tests {
		if actual := formatMessageTime(test.sent, now); actual != test.expected {
			t.Errorf("expected %s for %s, got %s", test.expected, test.sent, actual)
		}
	}
}

func TestSearchMatching(t *testing.T) {
	withUI(t)
	chat := messages.Chat{Id: "31612345678@s.whatsapp.net", Name: "Carol :)"}
	for search, expected := range map[string]bool{"carol": true, "CAROL": true, "3161234": true, "bob": false} {
		if chatMatches(chat, search) != expected {
			t.Errorf("chat search %q: expected %v", search, expected)
		}
	}
	msg := messages.Message{Text: "See you at the Station"}
	if !messageMatches(msg, "station") || messageMatches(msg, "train") || !messageMatches(msg, "") {
		t.Error("expected message search to ignore case and match everything when empty")
	}
}

func TestHighlightSearch(t *testing.T) {
	withUI(t)
	if actual := highlightSearch("a Cat and a cat [x]", "cat"); actual != "a [black:yellow]Cat[-:-] and a [black:yellow]cat[-:-] [x[]" {
		t.Fatalf("unexpected highlight %q", actual)
	}
	if actual := highlightSearch("[x]", ""); actual != "[x[]" {
		t.Fatalf("expected text to be escaped without a search, got %q", actual)
	}
}

func TestReactionSummary(t *testing.T) {
	withUI(t)
	summary := reactionSummary(map[string]string{"a": "👍", "b": "❤️", "c": "👍", "me": "😭"})
	if summary != "👍2 ❤️ 😭" {
		t.Fatalf("unexpected summary %q", summary)
	}
	if reactionSummary(nil) != "" {
		t.Fatal("expected no summary without reactions")
	}
	msg := messages.Message{Id: "1", ContactId: "alice", ContactShort: "Alice", Timestamp: 1000, Text: "hi",
		Reactions: map[string]string{"me": "👍"}}
	lines := strings.Split(getTextMessageString(&msg, nil, 0), "\n")
	if len(lines) != 3 || !strings.Contains(lines[2], "↳") || !strings.Contains(lines[2], "👍") {
		t.Fatalf("expected the marked reactions on a line below the message, got %q", lines)
	}
}

func TestSwitchDraft(t *testing.T) {
	withUI(t)
	drafts = map[string]string{}
	if text := switchDraft("", "alice", "/search bob"); text != "" {
		t.Fatalf("expected no draft for a chat that wasn't typed in, got %q", text)
	}
	if text := switchDraft("alice", "bob", "half a message"); text != "" {
		t.Fatalf("expected no draft for bob, got %q", text)
	}
	if text := switchDraft("bob", "alice", ""); text != "half a message" {
		t.Fatalf("expected the draft of alice back, got %q", text)
	}
	// the open chat's draft is in the input, and no draft anymore
	if drafts["alice"] != "" {
		t.Fatal("expected the draft of the open chat to have moved into the input")
	}
	if text := switchDraft("alice", "", ""); text != "/search bob" {
		t.Fatalf("expected the draft of Chats back, got %q", text)
	}
	// a sent or cleared message leaves no draft
	if _, ok := drafts["alice"]; ok {
		t.Fatal("expected the draft of alice to be removed after clearing it")
	}
}

func TestChatNodeTextMarksDrafts(t *testing.T) {
	withUI(t)
	drafts = map[string]string{"alice": "half a message"}
	if text := chatNodeText(messages.Chat{Id: "alice", Name: "Alice"}); text != "Alice ✎" {
		t.Fatalf("expected a draft marker, got %q", text)
	}
	if text := chatNodeText(messages.Chat{Id: "bob", Name: "Bob"}); text != "Bob" {
		t.Fatalf("expected no marker without a draft, got %q", text)
	}
}

func TestShiftEnterStartsANewLine(t *testing.T) {
	withUI(t)
	textInput = newTextInput()
	setInput("one")
	typeKeys(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModShift), tcell.NewEventKey(tcell.KeyRune, 't', tcell.ModNone))
	if text := textInput.GetText(); text != "one\nt" {
		t.Fatalf("expected a new line, got %q", text)
	}
}

func TestWrappedLineCount(t *testing.T) {
	withUI(t)
	tests := []struct {
		text     string
		width    int
		expected int
	}{
		{"", 10, 1},
		{"short", 10, 1},
		{"exactly 10", 10, 1},
		{"one two three", 10, 2},
		{"one two three four five", 10, 3},
		{"one\ntwo", 10, 2},
		{"abcdefghijklmnopqrstuvwxyz", 10, 3},
		{"😭😭😭😭😭😭", 10, 2}, // emoji are two columns wide
	}
	for _, test := range tests {
		if actual := wrappedLineCount(test.text, test.width); actual != test.expected {
			t.Errorf("%q at width %d: expected %d lines, got %d", test.text, test.width, test.expected, actual)
		}
	}
}

// typeKeys sends keys to the input field, through its key handling
// typeText types text in the input, key by key
func typeText(text string) {
	for _, r := range text {
		typeKeys(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
}

func typeKeys(keys ...*tcell.EventKey) {
	for _, key := range keys {
		textInput.InputHandler()(key, func(tview.Primitive) {})
	}
}

func TestWordDeletingKeys(t *testing.T) {
	withUI(t)
	textInput = newTextInput()
	left := tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModNone)

	setInput("one two three")
	typeKeys(tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModCtrl))
	if text := textInput.GetText(); text != "one two " {
		t.Errorf("expected Ctrl+Backspace to delete the word before the cursor, got %q", text)
	}

	setInput("one two three")
	for range "two three" {
		typeKeys(left)
	}
	typeKeys(tcell.NewEventKey(tcell.KeyDelete, 0, tcell.ModCtrl))
	if text := textInput.GetText(); text != "one three" {
		t.Errorf("expected Ctrl+Delete to delete until the next word, got %q", text)
	}
	// the cursor stays where it was, typing continues there
	typeKeys(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))
	if text := textInput.GetText(); text != "one xthree" {
		t.Errorf("expected the cursor to stay before the next word, got %q", text)
	}
}

func TestNextWordStart(t *testing.T) {
	withUI(t)
	text := "one two  three 😭 end"
	for pos, expected := range map[int]int{0: 4, 1: 4, 3: 4, 4: 9, 7: 9, 9: 15, 15: 20, 20: len(text)} {
		if actual := nextWordStart(text, pos); actual != expected {
			t.Errorf("from %d in %q: expected %d, got %d", pos, text, expected, actual)
		}
	}
}

func TestCtrlArrowsMoveByWords(t *testing.T) {
	withUI(t)
	textInput = newTextInput()
	setInput("one two three")
	typeKeys(tcell.NewEventKey(tcell.KeyHome, 0, tcell.ModNone))

	cursor := func() int {
		_, pos, _ := textInput.GetSelection()
		return pos
	}
	for _, expected := range []int{4, 8, 13, 13} {
		typeKeys(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModCtrl))
		if cursor() != expected {
			t.Fatalf("expected Ctrl+Right to move to %d, got %d", expected, cursor())
		}
	}
	for _, expected := range []int{8, 4, 0, 0} {
		typeKeys(tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModCtrl))
		if cursor() != expected {
			t.Fatalf("expected Ctrl+Left to move to %d, got %d", expected, cursor())
		}
	}
}

func TestSyncText(t *testing.T) {
	withUI(t)
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.Local)
	if text := syncText(true, time.Time{}, now); text != "" {
		t.Errorf("expected nothing before anything was received, got %q", text)
	}
	if text := syncText(true, now.Add(-12*time.Second), now); text != "[gray]synced just now[-]" {
		t.Errorf("unexpected text %q", text)
	}
	if text := syncText(true, now.Add(-90*time.Second), now); !strings.Contains(text, config.Config.Colors.Negative) || !strings.Contains(text, "1 min ago") {
		t.Errorf("expected a warning after a minute without data, got %q", text)
	}
	if text := syncText(false, now.Add(-90*time.Second), now); !strings.HasPrefix(text, "[gray]") {
		t.Errorf("expected no warning while offline, which is shown already, got %q", text)
	}
	for duration, expected := range map[time.Duration]string{
		5 * time.Second:  "just now",
		59 * time.Second: "just now",
		time.Minute:      "1 min ago",
		45 * time.Minute: "45 min ago",
		time.Hour:        "1 hour ago",
		5 * time.Hour:    "5 hours ago",
		24 * time.Hour:   "1 day ago",
		50 * time.Hour:   "2 days ago",
	} {
		if actual := timeAgo(duration); actual != expected {
			t.Errorf("%v: expected %s, got %s", duration, expected, actual)
		}
	}
}

func TestCommandsAreColored(t *testing.T) {
	withUI(t)
	textInput = newTextInput()
	color := func() tcell.Color {
		fg, _, _ := textInput.GetTextStyle().Decompose()
		return fg
	}
	silentColor := tcell.ColorNames[config.Config.Colors.SilentCommandText]
	commandColor := tcell.ColorNames[config.Config.Colors.CommandText]
	inputColor := tcell.ColorNames[config.Config.Colors.InputText]

	for text, expected := range map[string]tcell.Color{
		"/search alice":       silentColor,
		"/archive":            silentColor,
		"/unknown":            silentColor,
		"hello":               inputColor,
		"/react 👍":            commandColor, // these send something to the chat
		"/upload C:/file.txt": commandColor,
	} {
		setInput(text)
		if color() != expected {
			t.Errorf("%q: expected %v, got %v", text, expected, color())
		}
	}

	// while typing too
	setInput("")
	typeText("/arch")
	if color() != silentColor {
		t.Error("expected a command being typed to be colored")
	}
}

func TestReactionLinesInTheChat(t *testing.T) {
	withUI(t)
	defer func(names func(string) string) { reactorName = names }(reactorName)
	reactorName = func(reactor string) string { return map[string]string{"bob": "Bob", "me": "You"}[reactor] }
	messageSearch = ""
	msgs := []messages.Message{
		{Id: "1", ContactId: "alice", ContactShort: "Alice", Timestamp: 1000, Text: "are you coming tonight?",
			Reactions: map[string]string{"bob": "👍"}, ReactionTimes: map[string]int64{"bob": 1100}},
		{Id: "2", ContactId: "alice", ContactShort: "Alice", Timestamp: 1200, Text: "never mind"},
	}
	out := getMessagesString(msgs, 0)
	first := strings.Index(out, "are you coming")
	reaction := strings.Index(out, `Bob reacted 👍 to "are you coming tonight?"`)
	second := strings.Index(out, "never mind")
	if first < 0 || reaction < first || second < reaction {
		t.Fatalf("expected the reaction line between the messages, got %q", out)
	}
	if !strings.Contains(out, "[::d](") {
		t.Fatalf("expected the reaction line to be dimmed, got %q", out)
	}
	// the message after the reaction starts a new group, with a header
	if strings.Count(out, "Alice:") != 2 {
		t.Fatalf("expected a new group after the reaction, got %q", out)
	}
	// the reaction is still shown under the message
	if !strings.Contains(out, "↳") {
		t.Fatalf("expected the reactions under the message, got %q", out)
	}

	messageSearch = "coming"
	defer func() { messageSearch = "" }()
	if strings.Contains(getMessagesString(msgs, 0), "reacted") {
		t.Fatal("expected search results to list only messages")
	}
}

func TestNotificationIconIsAPNG(t *testing.T) {
	withUI(t)
	icon, err := png.Decode(bytes.NewReader(notificationIcon))
	if err != nil {
		t.Fatal(err)
	}
	if icon.Bounds().Dx() == 0 {
		t.Fatal("expected an icon")
	}
}

func TestWindowTitleCountsNewMessagesAndReactions(t *testing.T) {
	withUI(t)
	chats := []messages.Chat{
		{Id: "alice", Unread: 2, UnreadReactions: []int64{100}},
		{Id: "bob", UnreadReactions: []int64{100, 200}},
		{Id: "carol", Unread: 5, InArchive: true},
		{Id: "family", Unread: 1, Hidden: true},
	}
	if title := windowTitle(chats); title != "WhatsCLI (5)" {
		t.Errorf("expected 5 new, got %q", title)
	}
	if title := windowTitle(nil); title != "WhatsCLI" {
		t.Errorf("expected no count without new messages, got %q", title)
	}
	if text := chatNodeText(chats[1]); !strings.Contains(text, "]2[") {
		t.Errorf("expected the reactions to be counted in the chat list, got %q", text)
	}
}

func TestMovingThroughMessagesStopsAtTheEnds(t *testing.T) {
	withUI(t)
	curRegions = []messages.Message{{Id: "1"}, {Id: "2"}, {Id: "3"}}
	defer func() { curRegions = nil }()
	for _, test := range []struct {
		from   string
		offset int
		to     string
	}{{"3", 1, "3"}, {"1", -1, "1"}, {"2", 1, "3"}, {"2", -10, "1"}} {
		if id := GetOffsetMsgId(test.from, test.offset); id != test.to {
			t.Errorf("from %s by %d: expected %s, got %s", test.from, test.offset, test.to, id)
		}
	}
}
