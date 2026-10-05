package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png" // the clipboard has PNG images
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/normen/whatscli/messages"
	"golang.design/x/clipboard"
)

// Pasting images: the paste image key attaches the image on the clipboard to
// the message that is typed, which is sent as its caption. Terminals only
// paste text, so whatscli reads the clipboard itself.

// pastedImage is the file of the image attached from the clipboard, or ""
var pastedImage string

// imageNotice is the key of the notice of the attached image
const imageNotice = "image"

// clipboardReady is whether the clipboard can be read, see readClipboardImage
var clipboardReady = clipboard.Init() == nil

// readClipboard returns the text on the clipboard
func readClipboard() (string, error) {
	if !clipboardReady {
		return "", errors.New("the clipboard can't be read")
	}
	data, err := clipboard.Read(context.Background(), clipboard.FmtText)
	return string(data), err
}

// writeClipboard puts text on the clipboard
func writeClipboard(text string) error {
	if !clipboardReady {
		return errors.New("the clipboard can't be written")
	}
	_, err := clipboard.Write(context.Background(), clipboard.FmtText, []byte(text))
	return err
}

// readClipboardImage returns the image on the clipboard, or an image file that
// was copied, e.g. in the file manager, or nil if there is none
func readClipboardImage() ([]byte, error) {
	if !clipboardReady {
		return nil, errors.New("the clipboard can't be read")
	}
	ctx := context.Background()
	data, err := clipboard.Read(ctx, clipboard.FmtImage)
	if err == nil {
		return data, nil
	}
	if files, filesErr := clipboard.ReadFiles(ctx); filesErr == nil {
		for _, file := range files {
			if ext := strings.ToLower(filepath.Ext(file)); ext == ".jpg" || ext == ".jpeg" || ext == ".png" {
				return os.ReadFile(file)
			}
		}
	}
	if errors.Is(err, clipboard.ErrNoData) {
		return nil, nil
	}
	return nil, err
}

// attaches the image on the clipboard to the message that is typed
func handlePasteImage(ev *tcell.EventKey) *tcell.EventKey {
	if currentReceiver.Id == "" {
		PrintHint("open a chat to paste an image into")
		return nil
	}
	data, err := readClipboardImage()
	if err != nil {
		PrintErrorMsg("failed to read the clipboard:", err)
		return nil
	} else if data == nil {
		PrintHint("there is no image on the clipboard")
		return nil
	}
	path, err := saveAsJPEG(data)
	if err != nil {
		PrintErrorMsg("failed to paste the image:", err)
		return nil
	}
	pastedImage = path
	showNotice(currentReceiver.Id, imageNotice, "Image from the clipboard attached, Enter sends it with the text as its caption (Esc to remove)")
	app.SetFocus(textInput)
	return nil
}

// removePastedImage removes the attached image, e.g. when it was sent
func removePastedImage() {
	if pastedImage != "" {
		pastedImage = ""
		showNotice(currentReceiver.Id, imageNotice, "")
	}
}

// saveAsJPEG saves an image as JPEG, as the phone sends photos, in the
// temporary folder of whatscli, whose old files are removed
func saveAsJPEG(data []byte) (string, error) {
	picture, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	// JPEG has no transparency, which is white like the background of a chat
	flat := image.NewRGBA(picture.Bounds())
	draw.Draw(flat, flat.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(flat, flat.Bounds(), picture, picture.Bounds().Min, draw.Over)
	var out bytes.Buffer
	if err = jpeg.Encode(&out, flat, &jpeg.Options{Quality: 90}); err != nil {
		return "", err
	}
	dir := messages.TempFolder()
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("clipboard-%d.jpg", time.Now().UnixNano()))
	return path, os.WriteFile(path, out.Bytes(), 0600)
}

// sendPastedImage sends the attached image with the text as its caption
func sendPastedImage(caption string) {
	sendCommand(messages.Command{Name: "pasteimage", Params: []string{currentReceiver.Id, pastedImage, caption}})
	removePastedImage()
}
