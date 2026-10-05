package messages

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/normen/whatscli/config"
	"golang.org/x/image/webp"
)

// Alt texts: when a model is configured, see AltTextModel, the images and
// stickers of the open chat are described in a few words by it, one at a
// time, see describeChat. The description is saved with the message.

// altTextNotice is the key of the notices about the model, on the main screen
const altTextNotice = "alttext"

// altTextPrompt asks the model for the alt text
const altTextPrompt = "Describe this image for someone who can't see it, in at most 10 words, without a preamble."

// altTexts are the messages that wait for a description
type altTexts struct {
	lock    sync.Mutex
	pending map[string]bool
	// the ones that couldn't be described, which aren't tried again until whatscli restarts
	failed map[string]bool
	queue  chan string
}

// describeChat has the images and stickers of a chat that have no alt text
// described, if a model is configured. It is cheap to call again, as the ones
// that are described or wait for it are skipped.
func (sm *SessionManager) describeChat(chatID string) {
	if config.Config.General.AltTextModel == "" || chatID == "" {
		return
	}
	sm.altTexts.lock.Lock()
	defer sm.altTexts.lock.Unlock()
	for _, msg := range sm.db.GetMessages(chatID) {
		if (msg.Kind != MessageKindImage && msg.Kind != MessageKindSticker) || msg.AltText != "" ||
			sm.altTexts.pending[msg.Id] || sm.altTexts.failed[msg.Id] {
			continue
		}
		if sm.altTexts.queue == nil {
			sm.altTexts.pending = make(map[string]bool)
			sm.altTexts.failed = make(map[string]bool)
			sm.altTexts.queue = make(chan string, 1000)
			go sm.describeQueued()
		}
		select {
		case sm.altTexts.queue <- msg.Id:
			sm.altTexts.pending[msg.Id] = true
		default: // full, tried again when the chat is shown next
		}
	}
}

// describeQueued describes the queued messages, one at a time
func (sm *SessionManager) describeQueued() {
	for id := range sm.altTexts.queue {
		text, err := sm.describeMessage(id)
		sm.altTexts.lock.Lock()
		delete(sm.altTexts.pending, id)
		if err != nil && err != errNotConnected {
			sm.altTexts.failed[id] = true
		}
		sm.altTexts.lock.Unlock()
		if err != nil {
			sm.logWarn("Failed to describe message %s: %v", id, err)
		} else if chatID, ok := sm.db.SetAltText(id, text); ok {
			sm.showIfOpen(chatID)
		}
	}
}

// altTextPending returns whether messages of a chat wait for a description
func (sm *SessionManager) altTextPending(chatID string) bool {
	sm.altTexts.lock.Lock()
	defer sm.altTexts.lock.Unlock()
	for id := range sm.altTexts.pending {
		if msg, ok := sm.db.GetMessage(id); ok && msg.ChatId == chatID {
			return true
		}
	}
	return false
}

// describeMessage downloads the image of a message and asks the model for its alt text
func (sm *SessionManager) describeMessage(id string) (string, error) {
	msg, ok := sm.db.GetMessage(id)
	if !ok {
		return "", errors.New("the message isn't loaded")
	}
	url, err := sm.ensureServer()
	if err != nil {
		return "", err
	}
	client, err := sm.connectedClient()
	if err != nil {
		return "", err
	}
	downloadable, err := downloadableFromMessage(msg)
	if err != nil {
		return "", err
	}
	mimeType := msg.MimeType
	data, err := client.Download(context.Background(), downloadable)
	if err == nil && msg.Kind == MessageKindSticker {
		data, err = stickerAsPNG(data)
		mimeType = "image/png"
	}
	if err != nil {
		// e.g. older media, which expire on the server, or animated stickers,
		// some of which have a preview in the message
		thumbnail, thumbnailType := thumbnailOf(msg)
		if len(thumbnail) == 0 {
			return "", err
		}
		sm.logDebug("Describing message %s by its preview: %v", id, err)
		data, mimeType = thumbnail, thumbnailType
	}
	return askAltText(url, mimeType, data)
}

// thumbnailOf returns the small preview in an image or sticker message, and its type
func thumbnailOf(msg Message) ([]byte, string) {
	if thumbnail := msg.RawMessage.GetImageMessage().GetJPEGThumbnail(); len(thumbnail) > 0 {
		return thumbnail, "image/jpeg"
	}
	return msg.RawMessage.GetStickerMessage().GetPngThumbnail(), "image/png"
}

// stickerAsPNG returns a sticker as PNG, as the model doesn't read WebP. It
// fails for animated stickers.
func stickerAsPNG(data []byte) ([]byte, error) {
	picture, err := webp.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	err = png.Encode(&out, picture)
	return out.Bytes(), err
}

// askAltText asks the model at url for the alt text of an image, the way the
// OpenAI chat API is asked, which llama-server answers like
func askAltText(url, mimeType string, data []byte) (string, error) {
	request, _ := json.Marshal(map[string]any{
		"max_tokens": 40,
		"messages": []map[string]any{{
			"role": "user",
			"content": []map[string]any{
				{"type": "text", "text": altTextPrompt},
				{"type": "image_url", "image_url": map[string]string{"url": "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)}},
			},
		}},
	})
	client := http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Post(url+"/v1/chat/completions", "application/json", bytes.NewReader(request))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the model answered %s", resp.Status)
	}
	var answer struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&answer); err != nil {
		return "", err
	}
	if len(answer.Choices) == 0 {
		return "", errors.New("the model gave no answer")
	}
	text := strings.TrimSpace(answer.Choices[0].Message.Content)
	text = strings.TrimSuffix(strings.Trim(text, `"'`), ".")
	if text == "" {
		return "", errors.New("the model gave an empty answer")
	}
	return text, nil
}

// WithAltText puts the alt text of an image or sticker into the label of its
// text: "[IMAGE] caption" becomes "[IMAGE: a dog on a beach] caption"
func WithAltText(text, alt string) string {
	label, rest := SplitLabel(text)
	if alt == "" || label == "" {
		return text
	}
	return strings.TrimSuffix(label, "]") + ": " + alt + "]" + rest
}

// SplitLabel returns the label at the start of the text of media, like
// "[IMAGE]", and the rest, or "" and the text without one
func SplitLabel(text string) (string, string) {
	if end := strings.Index(text, "]"); strings.HasPrefix(text, "[") && end >= 0 {
		return text[:end+1], text[end+1:]
	}
	return "", text
}
