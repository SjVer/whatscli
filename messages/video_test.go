package messages

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/normen/whatscli/ai"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

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

func TestTheFramesPromptListsTheirTimes(t *testing.T) {
	times := ai.FrameTimes(10) // 4 frames
	if prompt := framesPrompt("a video", 10, times); !bytes.Contains([]byte(prompt), []byte("4 frames, in order, from a video of 10 seconds, at 1.2s, 3.8s, 6.2s, 8.8s")) {
		t.Errorf("unexpected prompt %q", prompt)
	}
}
