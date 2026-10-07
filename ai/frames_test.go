package ai

import (
	"bytes"
	"image"
	"image/jpeg"
	"os"
	"os/exec"
	"testing"
)

func TestFrameCountGrowsWithTheLength(t *testing.T) {
	for seconds, expected := range map[int]int{0: 2, 1: 2, 3: 2, 4: 3, 7: 3, 8: 4, 15: 4, 16: 5, 31: 5, 32: 6, 64: 7, 127: 7, 128: 8, 3600: 8} {
		if count := FrameCount(seconds); count != expected {
			t.Errorf("%d seconds: expected %d frames, got %d", seconds, expected, count)
		}
	}
}

func TestFramesAreInTheMiddleOfEqualParts(t *testing.T) {
	times := FrameTimes(10) // 4 frames
	for i, expected := range []float64{1.25, 3.75, 6.25, 8.75} {
		if times[i] != expected {
			t.Errorf("frame %d: expected %v, got %v", i, expected, times[i])
		}
	}
}

func TestFFmpegFromThePathIsUsed(t *testing.T) {
	defer func(look func(string) (string, error)) { lookPath = look }(lookPath)
	lookPath = func(string) (string, error) { return "/usr/bin/ffmpeg", nil }
	var ffmpeg FFmpeg
	if path, err := ffmpeg.Ensure("", func(string) {}, nil); path != "/usr/bin/ffmpeg" || err != nil {
		t.Errorf("expected ffmpeg from the path, got %q, %v", path, err)
	}
}

func TestTakingFramesOfAVideo(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg isn't installed")
	}
	tmp := t.TempDir()
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)
	t.Setenv("TMPDIR", tmp)
	// a video of 4 seconds made by ffmpeg
	video, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "testsrc=duration=4:size=320x240:rate=10",
		"-pix_fmt", "yuv420p", "-movflags", "frag_keyframe+empty_moov", "-f", "mp4", "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	frames, err := VideoFrames(ffmpeg, t.TempDir(), video, FrameTimes(4))
	if err != nil || len(frames) != 3 {
		t.Fatalf("expected 3 frames, got %d, %v", len(frames), err)
	}
	if picture, err := jpeg.Decode(bytes.NewReader(frames[0])); err != nil || picture.Bounds().Dx() != 512 {
		t.Errorf("expected a JPEG 512 wide, got %v", err)
	}
}

func TestFramesOfAnAnimatedSticker(t *testing.T) {
	// 4 frames of 250 ms, the first whole, the others a strip at the bottom
	data, err := os.ReadFile("testdata/animated.webp")
	if err != nil {
		t.Fatal(err)
	}
	frames, length, err := WebPFrames(data, FrameTimes)
	if err != nil || length != 1 || len(frames) != 2 {
		t.Fatalf("expected 2 frames of 1 second, got %d of %v, %v", len(frames), length, err)
	}
	first, second := frames[0].(*image.RGBA), frames[1].(*image.RGBA)
	if first.Bounds().Dx() != 48 || first.Bounds().Dy() != 48 {
		t.Fatalf("expected frames of the whole canvas, got %v", first.Bounds())
	}
	// the strips are drawn over the first frame, which stays above them
	if !bytes.Equal(first.Pix[:36*first.Stride], second.Pix[:36*second.Stride]) {
		t.Error("expected the part above the strip to stay the same")
	}
	if bytes.Equal(first.Pix[36*first.Stride:43*first.Stride], second.Pix[36*second.Stride:43*second.Stride]) {
		t.Error("expected the strip to change")
	}
	if _, _, _, err := animatedWebP([]byte("RIFF\x00\x00\x00\x00WEBPVP8 ")); err == nil {
		t.Error("expected a still WebP not to be animated")
	}
}
