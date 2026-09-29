package messages

import (
	"testing"
	"time"
)

func TestConnectionLostNotifications(t *testing.T) {
	notifications := make(chan string, 10)
	defer func(send func(string, string, string) error, delay time.Duration) {
		sendNotification, connectionLostDelay = send, delay
	}(sendNotification, connectionLostDelay)
	sendNotification = func(title, text, icon string) error {
		notifications <- text
		return nil
	}
	connectionLostDelay = 50 * time.Millisecond
	next := func() string {
		select {
		case text := <-notifications:
			return text
		case <-time.After(200 * time.Millisecond):
			return ""
		}
	}

	var watch connectionWatch
	watch.disconnected()
	watch.connected() // back quickly
	if text := next(); text != "" {
		t.Fatalf("expected no notification for a short drop, got %q", text)
	}

	watch.disconnected()
	if text := next(); text != "Connection to WhatsApp lost, reconnecting..." {
		t.Fatalf("expected a notification for a lost connection, got %q", text)
	}
	watch.unresponsive() // still lost
	if text := next(); text != "" {
		t.Fatalf("expected one notification per outage, got %q", text)
	}
	watch.connected()
	if text := next(); text != "Connected to WhatsApp again" {
		t.Fatalf("expected a notification when it is back, got %q", text)
	}

	watch.unresponsive()
	if text := next(); text != "Connection to WhatsApp lost, reconnecting..." {
		t.Fatalf("expected no answers to pings to be notified right away, got %q", text)
	}
	watch.loggedOut("the phone removed this device")
	if text := next(); text != "Logged out: the phone removed this device" {
		t.Fatalf("expected being logged out to be notified, got %q", text)
	}
}
