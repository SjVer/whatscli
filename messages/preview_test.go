package messages

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOldPreviewsAreRemoved(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)
	t.Setenv("TMPDIR", tmp)
	dir := TempFolder()
	os.MkdirAll(dir, 0700)
	old, fresh := filepath.Join(dir, "old.jpg"), filepath.Join(dir, "fresh.jpg")
	os.WriteFile(old, []byte("x"), 0600)
	os.WriteFile(fresh, []byte("x"), 0600)
	os.Chtimes(old, time.Now().Add(-2*previewKeep), time.Now().Add(-2*previewKeep))

	(&SessionManager{}).cleanPreviews()
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("expected an old preview to be removed")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("expected a recent preview to be kept, it may still be open")
	}
}
