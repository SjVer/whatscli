package main

import (
	"bytes"
	"image/png"
	"testing"
)

func TestNotificationIconIsAPNG(t *testing.T) {
	icon, err := png.Decode(bytes.NewReader(notificationIcon))
	if err != nil {
		t.Fatal(err)
	}
	if icon.Bounds().Dx() == 0 {
		t.Fatal("expected an icon")
	}
}
