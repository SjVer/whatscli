package messages

import (
	"bytes"
	"encoding/json"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

func TestFrameCountGrowsWithTheLength(t *testing.T) {
	for seconds, expected := range map[int]int{0: 2, 1: 2, 3: 2, 4: 3, 7: 3, 8: 4, 15: 4, 16: 5, 31: 5, 32: 6, 64: 7, 127: 7, 128: 8, 3600: 8} {
		if count := frameCount(seconds); count != expected {
			t.Errorf("%d seconds: expected %d frames, got %d", seconds, expected, count)
		}
	}
}

func TestFramesAreInTheMiddleOfEqualParts(t *testing.T) {
	times := frameTimes(10) // 4 frames
	for i, expected := range []float64{1.25, 3.75, 6.25, 8.75} {
		if times[i] != expected {
			t.Errorf("frame %d: expected %v, got %v", i, expected, times[i])
		}
	}
	if prompt := framesPrompt("a video", 10, times); !bytes.Contains([]byte(prompt), []byte("4 frames, in order, from a video of 10 seconds, at 1.2s, 3.8s, 6.2s, 8.8s")) {
		t.Errorf("unexpected prompt %q", prompt)
	}
}

func TestAllFramesAreSentToTheModel(t *testing.T) {
	var request struct {
		Messages []struct {
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&request)
		w.Write([]byte(`{"choices":[{"message":{"content":"a cat falls off a table"}}]}`))
	}))
	defer server.Close()
	text, err := askAltText(server.URL, "frames", "image/jpeg", []byte("1"), []byte("2"), []byte("3"))
	if err != nil || text != "a cat falls off a table" {
		t.Fatalf("unexpected answer %q, %v", text, err)
	}
	// the prompt, then one part per frame
	if parts := request.Messages[0].Content; len(parts) != 4 || parts[0]["text"] != "frames" {
		t.Errorf("expected the prompt and 3 frames, got %v", parts)
	}
}

func TestGIFsHaveTheirOwnLabel(t *testing.T) {
	sm := newTestSession(&recordingUi{})
	alice := types.NewJID("111", types.DefaultUserServer)
	for id, gif := range map[string]bool{"gif": true, "video": false} {
		sm.eventHandler.Handle(&events.Message{
			Info:    types.MessageInfo{MessageSource: types.MessageSource{Chat: alice, Sender: alice}, ID: id, Timestamp: time.Now()},
			Message: &waProto.Message{VideoMessage: &waProto.VideoMessage{GifPlayback: proto.Bool(gif), Caption: proto.String("lol")}},
		})
	}
	for id, expected := range map[string]string{"gif": "[GIF] lol", "video": "[VIDEO] lol"} {
		if msg, _ := sm.db.GetMessage(id); msg.Text != expected || msg.Kind != MessageKindVideo {
			t.Errorf("%s: expected %q, got %q", id, expected, msg.Text)
		}
	}
}

func TestFFmpegFromThePathIsUsed(t *testing.T) {
	defer func(look func(string) (string, error)) { lookPath = look }(lookPath)
	lookPath = func(string) (string, error) { return "/usr/bin/ffmpeg", nil }
	sm := newTestSession(&recordingUi{})
	if path, err := sm.ensureFFmpeg(); path != "/usr/bin/ffmpeg" || err != nil {
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
	frames, err := videoFrames(ffmpeg, video, frameTimes(4))
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
	frames, length, err := webpFrames(data, frameTimes)
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
