package messages

import (
	"bytes"
	"context"
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

// NotificationIcon is the PNG icon of the app on notifications, set by main
var NotificationIcon []byte

// pictureRecheck is how often a chat's picture is checked for changes
const pictureRecheck = time.Hour

// chatPictures are the pictures of chats shown on notifications
type chatPictures struct {
	lock    sync.Mutex
	checked map[string]checkedPicture
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
	path := sm.downloadChatPicture(chatID)
	sm.pictures.lock.Lock()
	if sm.pictures.checked == nil {
		sm.pictures.checked = make(map[string]checkedPicture)
	}
	sm.pictures.checked[chatID] = checkedPicture{path, time.Now()}
	sm.pictures.lock.Unlock()
	return path
}

func (sm *SessionManager) downloadChatPicture(chatID string) string {
	configPath := config.GetConfigFilePath()
	if sm.client == nil || !sm.client.IsConnected() || configPath == "" {
		return ""
	}
	jid, err := types.ParseJID(chatID)
	if err != nil {
		return ""
	}
	info, err := sm.client.GetProfilePictureInfo(context.Background(), jid, &whatsmeow.GetProfilePictureParams{Preview: true})
	if err != nil || info == nil || info.URL == "" {
		return ""
	}
	dir := filepath.Join(filepath.Dir(configPath), "pictures")
	path := filepath.Join(dir, pictureFileName(jid.User, info.ID))
	if _, err = os.Stat(path); err == nil {
		return path
	}

	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(info.URL)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK || os.MkdirAll(dir, 0700) != nil {
		return ""
	}
	round, err := roundPicture(data)
	if err != nil {
		return ""
	}
	// the chat's earlier pictures
	if old, err := filepath.Glob(filepath.Join(dir, strings.TrimSuffix(pictureFileName(jid.User, "*"), ".png")+"*")); err == nil {
		for _, file := range old {
			os.Remove(file)
		}
	}
	if os.WriteFile(path, round, 0600) != nil {
		return ""
	}
	return path
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
			if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '*' {
				return r
			}
			return '_'
		}, text)
	}
	return safe(user) + "_" + safe(pictureID) + ".png"
}
