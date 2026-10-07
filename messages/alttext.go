package messages

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"strings"
	"sync"

	"github.com/normen/whatscli/ai"
	"github.com/normen/whatscli/config"
	"golang.org/x/image/webp"
)

// Alt texts: when a model is configured, see AiModel, the images, stickers
// and videos of the open chat are described in a few words by it, one at a
// time, see describeChat. The description is saved with the message.

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
	if config.Config.General.AiModel == "" || chatID == "" {
		return
	}
	sm.altTexts.lock.Lock()
	defer sm.altTexts.lock.Unlock()
	for _, msg := range sm.db.GetMessages(chatID) {
		if (msg.Kind != MessageKindImage && msg.Kind != MessageKindSticker && msg.Kind != MessageKindVideo) || msg.AltText != "" ||
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
	url, err := sm.ensureModel()
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
	if err == nil && msg.Kind == MessageKindVideo {
		var text string
		if text, err = sm.describeVideo(url, msg, data); err == nil {
			return text, nil
		}
	} else if err == nil && msg.Kind == MessageKindSticker {
		var still []byte
		if still, err = stickerAsPNG(data); err == nil {
			data, mimeType = still, "image/png"
		} else if text, animatedErr := sm.describeAnimatedSticker(url, msg, data); animatedErr == nil {
			return text, nil
		} else {
			err = fmt.Errorf("%v, and as an animated sticker: %v", err, animatedErr)
		}
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
	return askAltText(url, altTextPrompt, mimeType, data)
}

// describeVideo asks the model for the alt text of a video from its frames
func (sm *SessionManager) describeVideo(url string, msg Message, video []byte) (string, error) {
	ffmpeg, err := sm.ensureFFmpeg()
	if err != nil {
		return "", err
	}
	seconds := int(msg.RawMessage.GetVideoMessage().GetSeconds())
	if seconds <= 0 {
		seconds = 8 // not known, 4 frames
	}
	times := ai.FrameTimes(float64(seconds))
	frames, err := ai.VideoFrames(ffmpeg, TempFolder(), video, times)
	if err != nil {
		return "", err
	}
	sm.logDebug("Describing video %s of %d seconds by %d frames", msg.Id, seconds, len(frames))
	return askAltText(url, framesPrompt("a video", float64(seconds), times), "image/jpeg", frames...)
}

// describeAnimatedSticker asks the model for the alt text of an animated
// sticker from its frames
func (sm *SessionManager) describeAnimatedSticker(url string, msg Message, sticker []byte) (string, error) {
	var times []float64
	pictures, length, err := ai.WebPFrames(sticker, func(length float64) []float64 {
		times = ai.FrameTimes(length)
		return times
	})
	if err != nil {
		return "", err
	}
	frames := make([][]byte, len(pictures))
	for i, picture := range pictures {
		var out bytes.Buffer
		if err = png.Encode(&out, picture); err != nil {
			return "", err
		}
		frames[i] = out.Bytes()
	}
	sm.logDebug("Describing animated sticker %s of %.1f seconds by %d frames", msg.Id, length, len(frames))
	return askAltText(url, framesPrompt("an animated sticker", length, times[:len(frames)]), "image/png", frames...)
}

// thumbnailOf returns the small preview in an image or sticker message, and its type
func thumbnailOf(msg Message) ([]byte, string) {
	if thumbnail := msg.RawMessage.GetImageMessage().GetJPEGThumbnail(); len(thumbnail) > 0 {
		return thumbnail, "image/jpeg"
	} else if thumbnail = msg.RawMessage.GetVideoMessage().GetJPEGThumbnail(); len(thumbnail) > 0 {
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

// askAltText asks the model at url for the alt text of images, like the
// frames of a video, which it gives without quotes and a period
func askAltText(url, prompt, mimeType string, images ...[]byte) (string, error) {
	text, err := ai.Ask(url, prompt, 40, mimeType, images...)
	if err != nil {
		return "", err
	}
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
