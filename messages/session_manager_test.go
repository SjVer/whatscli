package messages

import (
	"testing"

	"github.com/normen/whatscli/config"
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
