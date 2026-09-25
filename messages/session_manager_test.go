package messages

import (
	"testing"
	"time"

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
	sm.db.AddContact(Contact{Id: "456@s.whatsapp.net", Name: "Mam"})
	sm.db.AddMessage(Message{Id: "m1", ChatId: "123@s.whatsapp.net", Timestamp: 100}, false)
	sm.db.AddMessage(Message{Id: "m2", ChatId: "123@s.whatsapp.net", Timestamp: 200}, false)
	sm.db.SetReaction("m1", "456@s.whatsapp.net", "👍")
	sm.db.SetReaction("m1", ReactorMe, "❤️")

	if info := sm.reactionInfo("m1"); info != "\nReactions:\n  ❤️ You\n  👍 Mam" {
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
