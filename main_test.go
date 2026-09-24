package main

import (
	"strings"
	"testing"
	"time"

	"github.com/normen/whatscli/messages"
)

func TestContinuesGroup(t *testing.T) {
	first := messages.Message{Id: "1", ContactId: "mam", ContactShort: "Mam", Timestamp: 1000}
	tests := []struct {
		name     string
		msg      messages.Message
		expected bool
	}{
		{"same sender within 2 minutes", messages.Message{ContactId: "mam", Timestamp: 1120}, true},
		{"same sender after more than 2 minutes", messages.Message{ContactId: "mam", Timestamp: 1121}, false},
		{"other sender", messages.Message{ContactId: "pap", Timestamp: 1010}, false},
		{"sent by me", messages.Message{ContactId: "mam", FromMe: true, Timestamp: 1010}, false},
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
	first := messages.Message{Id: "1", ContactId: "mam", ContactShort: "Mam", Timestamp: 1000, Text: "hello"}
	next := messages.Message{Id: "2", ContactId: "mam", ContactShort: "Mam", Timestamp: 1060, Text: "again"}

	firstLines := strings.Split(getTextMessageString(&first, nil), "\n")
	if len(firstLines) != 2 || !strings.Contains(firstLines[0], "Mam") || !strings.HasSuffix(firstLines[1], `hello[""]`) {
		t.Fatalf("expected the time and name on their own line above the text, got %q", firstLines)
	}
	nextLine := getTextMessageString(&next, &first)
	if nextLine != `["2"]again[""]` {
		t.Fatalf("expected only the text of a grouped message, without indentation, got %q", nextLine)
	}

	later := messages.Message{Id: "3", ContactId: "mam", ContactShort: "Mam", Timestamp: 2000, Text: "later"}
	laterLines := strings.Split(getTextMessageString(&later, &next), "\n")
	if len(laterLines) != 3 || laterLines[0] != `["3"]` || !strings.Contains(laterLines[1], "Mam") {
		t.Fatalf("expected an empty line before the header of a new group, got %q", laterLines)
	}
}

func TestFormatMessageTime(t *testing.T) {
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
	chat := messages.Chat{Id: "31612345678@s.whatsapp.net", Name: "Anna :)"}
	for search, expected := range map[string]bool{"roos": true, "ROOS": true, "3161234": true, "pap": false} {
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
	if actual := highlightSearch("a Cat and a cat [x]", "cat"); actual != "a [black:yellow]Cat[-:-] and a [black:yellow]cat[-:-] [x[]" {
		t.Fatalf("unexpected highlight %q", actual)
	}
	if actual := highlightSearch("[x]", ""); actual != "[x[]" {
		t.Fatalf("expected text to be escaped without a search, got %q", actual)
	}
}
