package messages

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/normen/whatscli/config"
)

func TestTimesAreReadAsPeopleWriteThem(t *testing.T) {
	for text, expected := range map[string]time.Duration{
		"10m": 10 * time.Minute, "2h": 2 * time.Hour, "1h30m": 90 * time.Minute, "1d": 24 * time.Hour,
		"2w": 14 * 24 * time.Hour, "3 days": 72 * time.Hour, "1.5 hours": 90 * time.Minute, "45 Min": 45 * time.Minute,
		"1h 15m": 75 * time.Minute, "30s": 30 * time.Second,
	} {
		if actual, err := parseDuration(text); err != nil || actual != expected {
			t.Errorf("%q: expected %v, got %v, %v", text, expected, actual, err)
		}
	}
	for _, text := range []string{"", "10", "m", "10 parsecs", "1h-5m"} {
		if _, err := parseDuration(text); err == nil {
			t.Errorf("%q: expected an error", text)
		}
	}
}

func TestMessagesToRecap(t *testing.T) {
	now := time.Unix(100000, 0)
	var msgs []Message
	for minutes := 60; minutes >= 0; minutes -= 10 { // 60 to 0 minutes ago
		msgs = append(msgs, Message{Id: string(rune('a' + len(msgs))), Timestamp: uint64(now.Add(-time.Duration(minutes) * time.Minute).Unix())})
	}
	if recent := messagesSince(msgs, now.Add(-25*time.Minute)); len(recent) != 3 || recent[0].Id != "e" {
		t.Errorf("expected the 3 messages of the last 25 minutes, got %v", recent)
	}
	if recent := messagesSince(msgs, now.Add(time.Minute)); recent != nil {
		t.Errorf("expected no messages, got %v", recent)
	}
	if last := unreadOrLast(msgs, 4); len(last) != 4 || last[0].Id != "d" {
		t.Errorf("expected the last 4 messages without unread ones, got %v", last)
	}
	msgs[5].Unread, msgs[6].Unread = true, true
	if unread := unreadOrLast(msgs, 4); len(unread) != 2 {
		t.Errorf("expected the 2 unread messages, got %d", len(unread))
	}
}

// fakeModel answers like llama-server with answer, and keeps the prompt it was asked
func fakeModel(t *testing.T, sm *SessionManager, answer string) *string {
	var prompt string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct {
				Content []map[string]any `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&request)
		prompt, _ = request.Messages[0].Content[0]["text"].(string)
		reply, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": answer}}}})
		w.Write(reply)
	}))
	t.Cleanup(server.Close)
	model := config.Config.General.AiModel
	config.Config.General.AiModel = "a/model"
	t.Cleanup(func() { config.Config.General.AiModel = model })
	sm.model.SetURL(server.URL)
	return &prompt
}

func TestTranscriptKeepsTheNewestMessages(t *testing.T) {
	at := uint64(time.Date(2026, 10, 5, 14, 30, 0, 0, time.Local).Unix())
	msgs := []Message{
		{Timestamp: at, ContactShort: "Alice", Text: "is anyone\ncoming?"},
		{Timestamp: at, FromMe: true, Text: "[IMAGE] me", AltText: "a dog on a beach", Kind: MessageKindImage},
		{Timestamp: at, ContactName: "Bob Example", Text: "yes"},
	}
	expected := "Mon 5 Oct 14:30 Alice: is anyone coming?\nMon 5 Oct 14:30 Me: [IMAGE: a dog on a beach] me\nMon 5 Oct 14:30 Bob Example: yes"
	if text := transcript(msgs); text != expected {
		t.Errorf("expected\n%s\ngot\n%s", expected, text)
	}
	long := []Message{{ContactShort: "Alice", Text: "old"}, {ContactShort: "Alice", Text: strings.Repeat("x", maxTranscript-30)}, {ContactShort: "Bob", Text: "new"}}
	if text := transcript(long); strings.Contains(text, "old") || !strings.HasSuffix(text, "Bob: new") {
		t.Errorf("expected the oldest message to be left out, got %d characters", len(text))
	}
}

func TestRecapIsShownBelowTheChat(t *testing.T) {
	ui := &recordingUi{}
	sm := newTestSession(ui)
	prompt := fakeModel(t, sm, "Here's the recap:\n Alice asks who comes, Bob does. ")
	chat := "111@s.whatsapp.net"
	sm.currentReceiver = chat
	now := uint64(time.Now().Unix())
	sm.db.AddMessage(Message{Id: "old", ChatId: chat, ContactShort: "Alice", Text: "last week", Timestamp: now - 7*24*3600}, false)
	sm.db.AddMessage(Message{Id: "1", ChatId: chat, ContactShort: "Alice", Text: "who comes?", Timestamp: now - 60}, false)
	sm.db.AddMessage(Message{Id: "2", ChatId: chat, ContactShort: "Bob", Text: "me", Timestamp: now - 30}, false)

	sm.recap([]string{"10", "min"})
	if notice := ui.notices[chat+"/"+recapNotice]; notice != "Recap of 2 messages: Alice asks who comes, Bob does." {
		t.Errorf("unexpected notice %q", notice)
	}
	if !strings.Contains(*prompt, "Alice: who comes?") || strings.Contains(*prompt, "last week") {
		t.Errorf("expected the messages of the last 10 minutes in the prompt, got %q", *prompt)
	}

	sm.recap([]string{"soon"})
	sm.recap(nil) // the last messages, as none are unread
	if notice := ui.notices[chat+"/"+recapNotice]; !strings.HasPrefix(notice, "Recap of 3 messages") {
		t.Errorf("expected the last messages to be recapped, got %q", notice)
	}
	if len(ui.errors) != 1 {
		t.Errorf("expected an error for a time that can't be read, got %v", ui.errors)
	}
}

func TestThePreambleOfTheModelIsLeftOut(t *testing.T) {
	if text := withoutPreamble("Here's a summary of the conversation:\n\nAlice asks.\n\nBob answers."); text != "Alice asks.\nBob answers." {
		t.Errorf("unexpected recap %q", text)
	}
	if text := withoutPreamble("Alice asks: who comes?"); text != "Alice asks: who comes?" {
		t.Errorf("expected a recap of one line to be kept, got %q", text)
	}
}

func TestTheTimeOfAQuestionCanBeSeveralWords(t *testing.T) {
	for params, expected := range map[string]struct {
		since    time.Duration
		question string
	}{
		"5d what did we plan for sunday?": {5 * 24 * time.Hour, "what did we plan for sunday?"},
		"3 days who comes":                {72 * time.Hour, "who comes"},
		"1h 30m where?":                   {90 * time.Minute, "where?"},
		"what did we plan?":               {0, "what did we plan?"},
		"2h":                              {2 * time.Hour, ""},
	} {
		since, question := splitTime(strings.Fields(params))
		if since != expected.since || question != expected.question {
			t.Errorf("%q: expected %v %q, got %v %q", params, expected.since, expected.question, since, question)
		}
	}
}

func TestAskingAboutTheChat(t *testing.T) {
	ui := &recordingUi{}
	sm := newTestSession(ui)
	prompt := fakeModel(t, sm, "A picnic in the park.")
	chat := "111@s.whatsapp.net"
	sm.currentReceiver = chat
	now := uint64(time.Now().Unix())
	sm.db.AddMessage(Message{Id: "old", ChatId: chat, ContactShort: "Alice", Text: "last month", Timestamp: now - 30*24*3600}, false)
	sm.db.AddMessage(Message{Id: "1", ChatId: chat, ContactShort: "Alice", Text: "picnic on sunday?", Timestamp: now - 2*24*3600}, false)

	sm.ask(strings.Fields("5d what did we plan for sunday?"))
	if notice := ui.notices[chat+"/"+askNotice]; notice != "Q: what did we plan for sunday?\nA: A picnic in the park." {
		t.Errorf("unexpected notice %q", notice)
	}
	if !strings.Contains(*prompt, "Alice: picnic on sunday?") || strings.Contains(*prompt, "last month") ||
		!strings.HasSuffix(*prompt, "Question: what did we plan for sunday?") || !strings.HasPrefix(*prompt, "Today is ") {
		t.Errorf("unexpected prompt %q", *prompt)
	}
	// without a time, all loaded messages
	sm.ask(strings.Fields("what happened last month?"))
	if !strings.Contains(*prompt, "last month") {
		t.Errorf("expected all messages without a time, got %q", *prompt)
	}
	sm.ask([]string{"5d"})
	if len(ui.printed) != 1 {
		t.Errorf("expected the usage without a question, got %v", ui.printed)
	}
}
