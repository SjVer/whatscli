package messages

import (
	"context"
	"errors"
	"mime"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/normen/whatscli/config"
	"go.mau.fi/whatsmeow"
)

func (sm *SessionManager) downloadMessage(msg Message, open bool) (string, error) {
	client, err := sm.connectedClient()
	if err != nil {
		return "", err
	}

	downloadable, err := downloadableFromMessage(msg)
	if err != nil {
		return "", err
	}

	baseDir := config.Config.General.DownloadPath
	if open { // to look at, not to keep
		baseDir = previewDir()
	}
	if err = os.MkdirAll(baseDir, 0o755); err != nil {
		return "", err
	}

	fileName := downloadFileName(msg)
	fullPath := filepath.Join(baseDir, fileName)
	if _, err = os.Stat(fullPath); err == nil {
		return fullPath, nil
	}

	data, err := client.Download(context.Background(), downloadable)
	if err != nil {
		return "", err
	}
	if err = os.WriteFile(fullPath, data, 0o644); err != nil {
		return "", err
	}
	return fullPath, nil
}

func downloadableFromMessage(msg Message) (whatsmeow.DownloadableMessage, error) {
	if msg.RawMessage == nil {
		return nil, errors.New("This is not a downloadable message")
	}
	switch msg.Kind {
	case MessageKindImage:
		if media := msg.RawMessage.GetImageMessage(); media != nil {
			return media, nil
		}
	case MessageKindVideo:
		if media := msg.RawMessage.GetVideoMessage(); media != nil {
			return media, nil
		}
	case MessageKindAudio:
		if media := msg.RawMessage.GetAudioMessage(); media != nil {
			return media, nil
		}
	case MessageKindDocument:
		if media := msg.RawMessage.GetDocumentMessage(); media != nil {
			return media, nil
		}
	case MessageKindSticker:
		if media := msg.RawMessage.GetStickerMessage(); media != nil {
			return media, nil
		}
	}
	return nil, errors.New("This is not a downloadable message")
}

func downloadFileName(msg Message) string {
	if msg.FileName != "" {
		safeName := path.Base(strings.ReplaceAll(msg.FileName, "\\", "/"))
		if safeName != "" && safeName != "." && safeName != ".." {
			return safeName
		}
	}
	return msg.Id + fileExtension(msg.MimeType)
}

// commonExtensions are the extensions of files that are better known than the
// first one the system knows. JPEG stays .jfif on Windows, which opens in the
// Photos app when .jpg has no default app.
var commonExtensions = map[string]string{
	"audio/mpeg": ".mp3",
	"audio/ogg":  ".ogg",
	"video/mp4":  ".mp4",
}

// fileExtension returns the extension of a file of the MIME type, or ""
func fileExtension(mimeType string) string {
	mediaType, _, err := mime.ParseMediaType(mimeType)
	if err != nil {
		return ""
	} else if ext, ok := commonExtensions[mediaType]; ok {
		return ext
	} else if exts, err := mime.ExtensionsByType(mediaType); err == nil && len(exts) > 0 {
		return exts[0]
	}
	return ""
}
