package ai

import (
	"errors"
	"os/exec"
	"runtime"
	"sync"
)

// ffmpegURL is the build of ffmpeg that is downloaded on Windows
const ffmpegURL = "https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-n8.1-latest-win64-lgpl-shared-8.1.zip"

// FFmpeg is ffmpeg, which takes the frames of videos, see VideoFrames: the one
// on the PATH, or on Windows one that is downloaded once, found by Ensure
type FFmpeg struct {
	lock  sync.Mutex
	path  string
	err   error
	found bool
}

// lookPath finds a program on the PATH, a variable for the tests
var lookPath = exec.LookPath

// Ensure returns the path of ffmpeg, downloading it into dir the first time
// on Windows, unless dir is empty. notice tells about the download, "" when
// it is over. A failure is remembered.
func (f *FFmpeg) Ensure(dir string, notice func(string), log Logger) (string, error) {
	f.lock.Lock()
	defer f.lock.Unlock()
	if f.found {
		return f.path, f.err
	}
	f.found = true
	if path, err := lookPath("ffmpeg"); err == nil {
		f.path = path
		return path, nil
	}
	if runtime.GOOS != "windows" || dir == "" {
		f.err = errors.New("install ffmpeg for the alt texts of videos")
	} else {
		notice("Downloading ffmpeg for the alt texts of videos...")
		f.path, f.err = downloadTool(dir, ffmpegURL, "ffmpeg")
		notice("")
	}
	if f.err != nil {
		logf(log, "Videos are described by their preview only: %v", f.err)
	}
	return f.path, f.err
}
