package messages

import (
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/normen/whatscli/config"
)

// The frames of videos are taken with ffmpeg, see videoFrames: the one on the
// PATH, or on Windows one that is downloaded once next to the config.

// ffmpegURL is the build of ffmpeg that is downloaded on Windows
const ffmpegURL = "https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-n8.1-latest-win64-lgpl-shared-8.1.zip"

// ffmpegTool is the path of ffmpeg, or why there is none, found once
type ffmpegTool struct {
	lock  sync.Mutex
	path  string
	err   error
	found bool
}

// lookPath finds a program on the PATH, a variable for the tests
var lookPath = exec.LookPath

// ensureFFmpeg returns the path of ffmpeg, downloading it the first time on
// Windows. A failure is remembered, and videos are described by their preview.
func (sm *SessionManager) ensureFFmpeg() (string, error) {
	sm.ffmpeg.lock.Lock()
	defer sm.ffmpeg.lock.Unlock()
	if sm.ffmpeg.found {
		return sm.ffmpeg.path, sm.ffmpeg.err
	}
	sm.ffmpeg.found = true
	if path, err := lookPath("ffmpeg"); err == nil {
		sm.ffmpeg.path = path
		return path, nil
	}
	configPath := config.GetConfigFilePath()
	if runtime.GOOS != "windows" || configPath == "" {
		sm.ffmpeg.err = errors.New("install ffmpeg for the alt texts of videos")
	} else {
		sm.uiHandler.SetNotice("", altTextNotice, "Downloading ffmpeg for the alt texts of videos...")
		sm.ffmpeg.path, sm.ffmpeg.err = downloadTool(filepath.Join(filepath.Dir(configPath), "llama", "ffmpeg"), ffmpegURL, "ffmpeg")
		sm.uiHandler.SetNotice("", altTextNotice, "")
	}
	if sm.ffmpeg.err != nil {
		sm.logWarn("Videos are described by their preview only: %v", sm.ffmpeg.err)
	}
	return sm.ffmpeg.path, sm.ffmpeg.err
}
