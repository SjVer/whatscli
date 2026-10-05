package messages

import (
	"os"
	"path/filepath"
	"time"

	"github.com/normen/whatscli/config"
)

// Opened attachments are downloaded to the preview path, by default a folder
// of whatscli in the temporary folder of the system. They can't be removed
// once they are shown, as many commands that open them, like the default app,
// return while the file is still open, so the ones of that folder that are
// older than previewKeep are removed when whatscli starts.

// previewKeep is how long opened attachments are kept in the temporary folder
const previewKeep = 24 * time.Hour

// previewDir returns the folder that opened attachments are downloaded to
func previewDir() string {
	if path := config.Config.General.PreviewPath; path != "" {
		return path
	}
	return TempFolder()
}

// TempFolder returns the folder of whatscli in the temporary folder of the
// system, whose files older than previewKeep are removed, see cleanPreviews
func TempFolder() string {
	return filepath.Join(os.TempDir(), "whatscli")
}

// cleanPreviews removes the opened attachments that are older than
// previewKeep from the temporary folder, never from a folder of the user
func (sm *SessionManager) cleanPreviews() {
	dir := TempFolder()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return // nothing was opened yet
	}
	removed := 0
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || entry.IsDir() || time.Since(info.ModTime()) < previewKeep {
			continue
		}
		if err = os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			sm.logWarn("Failed to remove the opened attachment %s: %v", entry.Name(), err)
		} else {
			removed++
		}
	}
	sm.logDebug("Removed %d opened attachments older than %s from %s", removed, previewKeep, dir)
}
