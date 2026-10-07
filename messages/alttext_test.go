package messages

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/normen/whatscli/config"
	waProto "go.mau.fi/whatsmeow/binary/proto"
)

func TestWithAltText(t *testing.T) {
	for _, test := range []struct{ text, alt, expected string }{
		{"[IMAGE] look", "a dog on a beach", "[IMAGE: a dog on a beach] look"},
		{"[STICKER]", "a waving cat", "[STICKER: a waving cat]"},
		{"[IMAGE]", "", "[IMAGE]"},
		{"hello", "a dog", "hello"},
		{"[DOCUMENT] notes.pdf", "a page of text", "[DOCUMENT: a page of text] notes.pdf"},
	} {
		if actual := WithAltText(test.text, test.alt); actual != test.expected {
			t.Errorf("%q + %q: expected %q, got %q", test.text, test.alt, test.expected, actual)
		}
	}
}

func TestOnlyImagesWithoutAltTextAreDescribed(t *testing.T) {
	defer func(model string) { config.Config.General.AiModel = model }(config.Config.General.AiModel)
	config.Config.General.AiModel = "a/model"
	sm := newTestSession(&recordingUi{})
	// a queue without a worker, to see what is queued
	sm.altTexts.queue = make(chan string, 10)
	sm.altTexts.pending, sm.altTexts.failed = map[string]bool{}, map[string]bool{}
	chat := "111@s.whatsapp.net"
	sm.db.AddMessage(Message{Id: "image", ChatId: chat, Kind: MessageKindImage}, false)
	sm.db.AddMessage(Message{Id: "sticker", ChatId: chat, Kind: MessageKindSticker}, false)
	sm.db.AddMessage(Message{Id: "described", ChatId: chat, Kind: MessageKindImage, AltText: "a cat"}, false)
	sm.db.AddMessage(Message{Id: "text", ChatId: chat, Kind: MessageKindText}, false)

	sm.describeChat(chat)
	sm.describeChat(chat) // queued once
	if len(sm.altTexts.queue) != 2 || !sm.altTexts.pending["image"] || !sm.altTexts.pending["sticker"] {
		t.Errorf("expected the image and the sticker to be queued, got %v", sm.altTexts.pending)
	}
	if !sm.altTextPending(chat) {
		t.Error("expected the chat to wait for alt texts")
	}

	config.Config.General.AiModel = ""
	sm.altTexts.pending = map[string]bool{}
	sm.describeChat(chat)
	if len(sm.altTexts.pending) != 0 {
		t.Error("expected nothing to be described without a model")
	}
}

func TestAskingTheModel(t *testing.T) {
	var request struct {
		Messages []struct {
			Content []struct {
				Type     string            `json:"type"`
				ImageURL map[string]string `json:"image_url"`
			} `json:"content"`
		} `json:"messages"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&request)
		w.Write([]byte(`{"choices":[{"message":{"content":" \"A dog running on a beach.\" "}}]}`))
	}))
	defer server.Close()

	text, err := askAltText(server.URL, altTextPrompt, "image/jpeg", []byte("jpeg"))
	if err != nil || text != "A dog running on a beach" {
		t.Fatalf("expected the cleaned up answer, got %q, %v", text, err)
	}
	if url := request.Messages[0].Content[1].ImageURL["url"]; url != "data:image/jpeg;base64,anBlZw==" {
		t.Errorf("expected the image in the request, got %q", url)
	}
}

func TestPreviewsOfImagesAndStickers(t *testing.T) {
	var thumbnail bytes.Buffer
	png.Encode(&thumbnail, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	sticker := Message{RawMessage: &waProto.Message{StickerMessage: &waProto.StickerMessage{PngThumbnail: thumbnail.Bytes()}}}
	if data, mimeType := thumbnailOf(sticker); !bytes.Equal(data, thumbnail.Bytes()) || mimeType != "image/png" {
		t.Errorf("expected the preview of the sticker, got %d bytes of %s", len(data), mimeType)
	}
	image := Message{RawMessage: &waProto.Message{ImageMessage: &waProto.ImageMessage{JPEGThumbnail: []byte("jpeg")}}}
	if data, mimeType := thumbnailOf(image); string(data) != "jpeg" || mimeType != "image/jpeg" {
		t.Errorf("expected the preview of the image, got %q of %s", data, mimeType)
	}
	// e.g. an animated sticker
	if _, err := stickerAsPNG([]byte("not webp")); err == nil {
		t.Error("expected a sticker that isn't a still WebP to fail")
	}
}
