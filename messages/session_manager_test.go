package messages

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/normen/whatscli/config"
	waLog "go.mau.fi/whatsmeow/util/log"
)

func TestDownloadFileNameSanitizesPathTraversal(t *testing.T) {
	msg := Message{
		Id:       "msg-1",
		FileName: "../../.ssh/authorized_keys",
	}

	got := downloadFileName(msg)
	if got != "authorized_keys" {
		t.Fatalf("expected sanitized basename, got %q", got)
	}
}

func TestDownloadFileNameSanitizesWindowsPathTraversal(t *testing.T) {
	msg := Message{
		Id:       "msg-2",
		FileName: `..\..\AppData\Roaming\startup.bat`,
	}

	got := downloadFileName(msg)
	if got != "startup.bat" {
		t.Fatalf("expected sanitized basename, got %q", got)
	}
}

func TestDownloadFileNameFallsBackForInvalidName(t *testing.T) {
	msg := Message{
		Id:       "msg-3",
		FileName: "..",
		MimeType: "image/png",
	}

	got := downloadFileName(msg)
	if got != "msg-3.png" {
		t.Fatalf("expected fallback filename, got %q", got)
	}
}

func TestReactionInfoListsWhoReacted(t *testing.T) {
	sm := &SessionManager{db: &MessageDatabase{}}
	sm.db.Init()
	sm.db.AddContact(Contact{Id: "456@s.whatsapp.net", Name: "Alice"})
	sm.db.AddMessage(Message{Id: "m1", ChatId: "123@s.whatsapp.net", Timestamp: 100}, false)
	sm.db.AddMessage(Message{Id: "m2", ChatId: "123@s.whatsapp.net", Timestamp: 200}, false)
	sm.db.SetReaction("m1", "456@s.whatsapp.net", "👍", 150)
	sm.db.SetReaction("m1", ReactorMe, "❤️", 150)

	if info := sm.reactionInfo("m1"); info != "\nReactions:\n  ❤️ You\n  👍 Alice" {
		t.Fatalf("unexpected reaction info %q", info)
	}
	if info := sm.reactionInfo("m2"); info != "" {
		t.Fatalf("expected nothing for a message without reactions, got %q", info)
	}
}

func TestActivityLoggerNotesReceivedData(t *testing.T) {
	sm := &SessionManager{}
	logger := activityLogger{Logger: waLog.Noop, received: sm.noteReceived}
	logger.Sub("Send").Debugf("sent")
	logger.Debugf("something else")
	if !sm.LastReceived().IsZero() {
		t.Fatal("expected only received data to count")
	}
	logger.Sub("Recv").Debugf("<iq type=\"result\"/>")
	if since := time.Since(sm.LastReceived()); since < 0 || since > time.Second {
		t.Fatalf("expected received data to be noted just now, got %v ago", since)
	}
}

func TestOpenCommandPerKind(t *testing.T) {
	general := config.Config.General
	defer func() { config.Config.General = general }()
	config.Config.General.ImageCommand = "feh"
	config.Config.General.VideoCommand = "mpv"
	for kind, expected := range map[MessageKind]string{MessageKindImage: "feh", MessageKindVideo: "mpv", MessageKindAudio: "", MessageKindText: ""} {
		if actual := openCommand(kind); actual != expected {
			t.Errorf("%s: expected %q, got %q", kind, expected, actual)
		}
	}
}

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

func TestRoundPicture(t *testing.T) {
	// a red picture, wider than high
	picture := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 40; x++ {
			picture.Set(x, y, color.RGBA{255, 0, 0, 255})
		}
	}
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, picture, nil); err != nil {
		t.Fatal(err)
	}
	data, err := roundPicture(jpg.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	round, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if bounds := round.Bounds(); bounds.Dx() != 20 || bounds.Dy() != 20 {
		t.Fatalf("expected the middle as a square, got %v", bounds)
	}
	if _, _, _, a := round.At(0, 0).RGBA(); a != 0 {
		t.Error("expected the corners to be transparent")
	}
	if r, _, _, a := round.At(10, 10).RGBA(); a != 0xffff || r < 0xf000 {
		t.Error("expected the middle to be the picture")
	}
}

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

func TestChatPictureFiles(t *testing.T) {
	if name := pictureFileName("123456789", "1710000000"); name != "123456789_1710000000.png" {
		t.Errorf("unexpected file name %q", name)
	}
	if name := pictureFileName("../evil", "1/2"); strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		t.Errorf("expected a safe file name, got %q", name)
	}
	// without a connection there is no picture, and the app icon is used
	sm := &SessionManager{}
	if path := sm.chatPicture("123@s.whatsapp.net"); path != "" {
		t.Errorf("expected no picture, got %q", path)
	}
	if _, ok := sm.pictures.checked["123@s.whatsapp.net"]; !ok {
		t.Error("expected the result to be remembered")
	}
}
