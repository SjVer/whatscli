package messages

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Videos are described by several of their frames: more for longer videos,
// but fewer than their length grows, as scenes repeat and every frame takes
// time and room in the model, see frameCount.

// frameCount returns how many frames of a video of the length are described:
// 2 for up to 3 seconds, like most GIFs, one more for every doubling, and at
// most 8, from about 2 minutes
func frameCount(seconds int) int {
	if seconds < 4 {
		return 2
	}
	return min(8, 2+int(math.Log2(float64(seconds)/2)))
}

// frameTimes returns when the frames of a video or animation of the length are
// taken, in seconds: in the middle of equal parts, which skips black first
// frames and fade-outs
func frameTimes(length float64) []float64 {
	count := frameCount(int(length))
	times := make([]float64, count)
	for i := range times {
		times[i] = (float64(i) + 0.5) * length / float64(count)
	}
	return times
}

// framesPrompt asks the model for the alt text of a video or an animated
// sticker, the kind, from its frames
func framesPrompt(kind string, length float64, times []float64) string {
	at := make([]string, len(times))
	for i, t := range times {
		at[i] = fmt.Sprintf("%.1fs", t)
	}
	return fmt.Sprintf("These are %d frames, in order, from %s of %.0f seconds, at %s. "+
		"Describe what happens in it for someone who can't see it, in at most 12 words, without a preamble.",
		len(times), kind, length, strings.Join(at, ", "))
}

// videoFrames takes the frames of a video at the times with ffmpeg, as JPEG
func videoFrames(ffmpeg string, video []byte, times []float64) ([][]byte, error) {
	// ffmpeg seeks in a file
	file := filepath.Join(TempFolder(), fmt.Sprintf("video-%d.mp4", time.Now().UnixNano()))
	if err := os.MkdirAll(TempFolder(), 0700); err != nil {
		return nil, err
	} else if err = os.WriteFile(file, video, 0600); err != nil {
		return nil, err
	}
	defer os.Remove(file)
	frames := make([][]byte, 0, len(times))
	for _, t := range times {
		var out, errOut bytes.Buffer
		cmd := exec.Command(ffmpeg, "-v", "error", "-ss", fmt.Sprintf("%.2f", t), "-i", file,
			"-frames:v", "1", "-vf", "scale=512:-2", "-f", "image2pipe", "-c:v", "mjpeg", "-q:v", "4", "-")
		cmd.Stdout, cmd.Stderr = &out, &errOut
		hideWindow(cmd)
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("ffmpeg: %v %s", err, strings.TrimSpace(errOut.String()))
		} else if out.Len() > 0 {
			frames = append(frames, out.Bytes())
		}
	}
	if len(frames) == 0 {
		return nil, fmt.Errorf("ffmpeg found no frames")
	}
	return frames, nil
}
