package main

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"testing"

	"github.com/normen/whatscli/messages"
)

func TestPastedImagesAreSavedAsJPEG(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)
	t.Setenv("TMPDIR", tmp)
	// a transparent picture with a red dot
	picture := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	picture.Set(1, 1, color.NRGBA{255, 0, 0, 255})
	var data bytes.Buffer
	png.Encode(&data, picture)

	path, err := saveAsJPEG(data.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	saved, err := jpeg.Decode(file)
	if err != nil {
		t.Fatalf("expected a JPEG in %s: %v", messages.TempFolder(), err)
	}
	if r, g, b, _ := saved.At(3, 3).RGBA(); r < 0xf000 || g < 0xf000 || b < 0xf000 {
		t.Errorf("expected the transparent part to be white, got %v %v %v", r, g, b)
	}
}

func TestPastingWithAnImageSendsIt(t *testing.T) {
	defer func() { sessionManager, pastedImage, notices = nil, "", map[string][]notice{} }()
	sessionManager = &messages.SessionManager{CommandChannel: make(chan messages.Command, 10)}
	currentReceiver = messages.Chat{Id: "alice"}
	pastedImage = "clipboard.jpg"
	sendMessage("look :joy:")
	command := <-sessionManager.CommandChannel
	if command.Name != "pasteimage" || command.Params[1] != "clipboard.jpg" || command.Params[2] != "look 😂" || pastedImage != "" {
		t.Errorf("expected the image to be sent with the caption, got %+v", command)
	}
}
