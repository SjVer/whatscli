package messages

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func TestRoundPicture(t *testing.T) {
	// a red picture, wider than high
	picture := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 40; x++ {
			picture.Set(x, y, color.RGBA{255, 0, 0, 255})
		}
	}
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, picture, nil); err != nil {
		t.Fatal(err)
	}
	data, err := roundPicture(jpg.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	round, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if bounds := round.Bounds(); bounds.Dx() != 20 || bounds.Dy() != 20 {
		t.Fatalf("expected the middle as a square, got %v", bounds)
	}
	if _, _, _, a := round.At(0, 0).RGBA(); a != 0 {
		t.Error("expected the corners to be transparent")
	}
	if r, _, _, a := round.At(10, 10).RGBA(); a != 0xffff || r < 0xf000 {
		t.Error("expected the middle to be the picture")
	}
}

func TestChatPictureFiles(t *testing.T) {
	if name := pictureFileName("123456789", "1710000000"); name != "123456789_1710000000.png" {
		t.Errorf("unexpected file name %q", name)
	}
	if name := pictureFileName("../evil", "1/2"); strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		t.Errorf("expected a safe file name, got %q", name)
	}
	// without a config there is nowhere to save pictures, so there is none, which is remembered
	sm := &SessionManager{}
	if path := sm.chatPicture("123@s.whatsapp.net"); path != "" {
		t.Errorf("expected no picture, got %q", path)
	}
	if _, ok := sm.pictures.checked["123@s.whatsapp.net"]; !ok {
		t.Error("expected the result to be remembered")
	}
}
