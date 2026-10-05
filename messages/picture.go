package messages

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	_ "image/jpeg" // pictures are JPEG
	"image/png"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/normen/whatscli/config"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

// pictureRecheck is how often a chat's picture is checked for changes
const pictureRecheck = time.Hour

// chatPictures are the pictures of chats shown on notifications
type chatPictures struct {
	lock    sync.Mutex
	checked map[string]checkedPicture
}

// forget forgets the checked pictures, e.g. of an account that was logged out
func (pictures *chatPictures) forget() {
	pictures.lock.Lock()
	defer pictures.lock.Unlock()
	pictures.checked = nil
}

// checkedPicture is the file of a chat's picture, "" if it has none, and when that was checked
type checkedPicture struct {
	path string
	at   time.Time
}

// chatPicture returns a file with the small profile or group picture of a chat,
// or "" if it has none or it is hidden. Pictures are saved next to the config,
// and downloaded again when they changed.
func (sm *SessionManager) chatPicture(chatID string) string {
	sm.pictures.lock.Lock()
	checked, ok := sm.pictures.checked[chatID]
	sm.pictures.lock.Unlock()
	if ok && time.Since(checked.at) < pictureRecheck {
		return checked.path
	}
	path, known := sm.downloadChatPicture(chatID)
	if !known {
		return path // tried again next time
	}
	sm.pictures.lock.Lock()
	if sm.pictures.checked == nil {
		sm.pictures.checked = make(map[string]checkedPicture)
	}
	sm.pictures.checked[chatID] = checkedPicture{path, time.Now()}
	sm.pictures.lock.Unlock()
	return path
}

// downloadChatPicture returns the file of a chat's picture, downloading it
// when it changed, and whether that is known: not when it failed, e.g. while
// not connected.
func (sm *SessionManager) downloadChatPicture(chatID string) (string, bool) {
	configPath := config.GetConfigFilePath()
	if configPath == "" {
		return "", true
	} else if sm.client == nil || !sm.client.IsConnected() {
		return "", false
	}
	jid, err := types.ParseJID(chatID)
	if err != nil {
		return "", true
	}
	info, err := sm.client.GetProfilePictureInfo(context.Background(), jid, &whatsmeow.GetProfilePictureParams{Preview: true})
	if errors.Is(err, whatsmeow.ErrProfilePictureNotSet) || errors.Is(err, whatsmeow.ErrProfilePictureUnauthorized) {
		return "", true // none, or hidden
	} else if err != nil {
		return "", false
	} else if info == nil || info.URL == "" {
		return "", true
	}
	dir := filepath.Join(filepath.Dir(configPath), "pictures")
	path := filepath.Join(dir, pictureFileName(jid.User, info.ID))
	if _, err = os.Stat(path); err == nil {
		return path, true
	}

	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(info.URL)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK || os.MkdirAll(dir, 0700) != nil {
		return "", false
	}
	round, err := roundPicture(data)
	if err != nil {
		return "", true // not a picture that can be shown
	}
	if err = os.WriteFile(path, round, 0600); err != nil {
		sm.logWarn("Failed to save the picture of %s: %v", chatID, err)
		return "", false
	}
	// the chat's earlier pictures
	if old, err := filepath.Glob(filepath.Join(dir, pictureFileName(jid.User, "*"))); err == nil {
		for _, file := range old {
			if file != path {
				os.Remove(file)
			}
		}
	}
	return path, true
}

// roundPicture cuts the middle of a picture out as a circle with a smooth edge,
// as a PNG, as notifications show pictures square
func roundPicture(data []byte) ([]byte, error) {
	picture, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	bounds := picture.Bounds()
	size := min(bounds.Dx(), bounds.Dy())
	left, top := bounds.Min.X+(bounds.Dx()-size)/2, bounds.Min.Y+(bounds.Dy()-size)/2
	radius := float64(size) / 2
	round := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			distance := math.Hypot(float64(x)+0.5-radius, float64(y)+0.5-radius)
			coverage := math.Max(0, math.Min(1, radius-distance))
			if coverage == 0 {
				continue
			}
			pixel := color.RGBAModel.Convert(picture.At(left+x, top+y)).(color.RGBA)
			scale := func(value uint8) uint8 { return uint8(float64(value) * coverage) }
			round.SetRGBA(x, y, color.RGBA{scale(pixel.R), scale(pixel.G), scale(pixel.B), scale(pixel.A)})
		}
	}
	var out bytes.Buffer
	if err = png.Encode(&out, round); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// pictureFileName returns the name of the file of a chat's picture, from the
// user or group part of its JID and the picture's ID
func pictureFileName(user, pictureID string) string {
	safe := func(text string) string {
		return strings.Map(func(r rune) rune {
			// * is kept for the pattern of all pictures of a chat
			if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '*' {
				return r
			}
			return '_'
		}, text)
	}
	return safe(user) + "_" + safe(pictureID) + ".png"
}
