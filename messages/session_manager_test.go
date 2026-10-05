package messages

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow/appstate"
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

func TestFileExtensionsAreTheCommonOnes(t *testing.T) {
	for mimeType, expected := range map[string]string{"audio/ogg; codecs=opus": ".ogg", "video/mp4": ".mp4", "image/png": ".png", "": ""} {
		if ext := fileExtension(mimeType); ext != expected {
			t.Errorf("%q: expected %q, got %q", mimeType, expected, ext)
		}
	}
}

func TestAppStateCanBeChangedOnceItIsRepaired(t *testing.T) {
	sm := newTestSession(&recordingUi{})
	sm.recoveryRequested = map[appstate.WAPatchName]time.Time{appstate.WAPatchRegularLow: time.Now()}
	// while the phone repairs it, changes wait
	if !sm.recoverAppState(appstate.ErrMismatchingLTHash, appstate.WAPatchRegularLow) {
		t.Fatal("expected to wait for the repair")
	}
	sm.recoveryDone(appstate.WAPatchRegularLow)
	if _, ok := sm.recoveryRequested[appstate.WAPatchRegularLow]; ok {
		t.Error("expected the repair to be done")
	}
	// a repair the phone didn't answer is asked again
	sm.recoveryRequested[appstate.WAPatchRegularLow] = time.Now().Add(-2 * recoveryWait)
	if requested := time.Since(sm.recoveryRequested[appstate.WAPatchRegularLow]) < recoveryWait; requested {
		t.Error("expected an old request to be asked again")
	}
}

// errorUi keeps the printed errors
type errorUi struct {
	recordingUi
	errors []error
}

func (u *errorUi) PrintError(err error) { u.errors = append(u.errors, err) }

func TestOnlyOwnMessagesCanBeRevoked(t *testing.T) {
	ui := &errorUi{}
	sm := newTestSession(&ui.recordingUi)
	sm.uiHandler = ui
	sm.db.AddMessage(Message{Id: "theirs", ChatId: "111@s.whatsapp.net", Timestamp: 100}, false)
	sm.revokeMessage([]string{"theirs"})
	if len(ui.errors) != 1 || ui.errors[0].Error() != "only your own messages can be revoked" {
		t.Errorf("expected to be told that it can't be revoked, got %v", ui.errors)
	}
}
