package messages

import (
	"testing"
)

func TestNotificationText(t *testing.T) {
	title, text := notificationText(Message{ChatId: "123@s.whatsapp.net", ContactShort: "Alice", Text: "hi"}, "Alice")
	if title != "Alice" || text != "hi" {
		t.Errorf("unexpected one-to-one notification %q %q", title, text)
	}
	title, text = notificationText(Message{ChatId: "456@g.us", ContactShort: "Bob", Text: "dinner"}, "Family")
	if title != "Family" || text != "Bob: dinner" {
		t.Errorf("unexpected group notification %q %q", title, text)
	}
}
