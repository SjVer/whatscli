package messages

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/draw"

	"golang.org/x/image/webp"
)

// Animated stickers are animated WebP, which neither golang.org/x/image/webp
// nor ffmpeg decode. Their frames are still WebP images in ANMF chunks, often
// only the part that changed, which are drawn on the canvas like a player does.

// webpFrame is a frame of an animated WebP
type webpFrame struct {
	x, y     int
	duration int // in milliseconds
	blend    bool
	dispose  bool
	// the still WebP of the frame
	data []byte
}

// animatedWebP returns the size of the canvas and the frames of an animated
// WebP, or an error if it isn't one
func animatedWebP(data []byte) (int, int, []webpFrame, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return 0, 0, nil, errors.New("not a WebP")
	}
	width, height := 0, 0
	var frames []webpFrame
	for chunk := data[12:]; len(chunk) >= 8; {
		size := int(binary.LittleEndian.Uint32(chunk[4:8]))
		if 8+size > len(chunk) {
			return 0, 0, nil, errors.New("a WebP chunk is cut off")
		}
		payload := chunk[8 : 8+size]
		switch string(chunk[:4]) {
		case "VP8X":
			if size >= 10 {
				width, height = uint24(payload[4:])+1, uint24(payload[7:])+1
			}
		case "ANMF":
			if size >= 16 {
				frames = append(frames, webpFrame{
					x: uint24(payload[0:]) * 2, y: uint24(payload[3:]) * 2,
					duration: uint24(payload[12:]),
					blend:    payload[15]&2 == 0,
					dispose:  payload[15]&1 != 0,
					data:     stillWebP(uint24(payload[6:])+1, uint24(payload[9:])+1, payload[16:]),
				})
			}
		}
		chunk = chunk[min(len(chunk), 8+size+size%2):] // chunks are padded to even sizes
	}
	if len(frames) == 0 || width == 0 {
		return 0, 0, nil, errors.New("not an animated WebP")
	}
	return width, height, frames, nil
}

// stillWebP makes a still WebP of the chunks of a frame of the size: its image,
// and with alpha, the alpha, which needs a VP8X chunk before it
func stillWebP(width, height int, chunks []byte) []byte {
	body := []byte("WEBP")
	if bytes.HasPrefix(chunks, []byte("ALPH")) {
		vp8x := make([]byte, 18)
		copy(vp8x, "VP8X")
		binary.LittleEndian.PutUint32(vp8x[4:], 10)
		vp8x[8] = 0x10 // has alpha
		putUint24(vp8x[12:], width-1)
		putUint24(vp8x[15:], height-1)
		body = append(body, vp8x...)
	}
	body = append(body, chunks...)
	riff := append([]byte("RIFF"), 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(riff[4:], uint32(len(body)))
	return append(riff, body...)
}

// webpFrames returns the frames of an animated WebP shown at the times that
// times returns for its length, in seconds, drawn on its canvas, and the length
func webpFrames(data []byte, times func(length float64) []float64) ([]image.Image, float64, error) {
	width, height, frames, err := animatedWebP(data)
	if err != nil {
		return nil, 0, err
	}
	length := 0
	for _, frame := range frames {
		length += frame.duration
	}
	at := times(float64(length) / 1000)
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	var shown []image.Image
	start := 0
	for _, frame := range frames {
		picture, err := webp.Decode(bytes.NewReader(frame.data))
		if err != nil {
			return nil, 0, err
		}
		area := picture.Bounds().Sub(picture.Bounds().Min).Add(image.Pt(frame.x, frame.y))
		operator := draw.Src
		if frame.blend {
			operator = draw.Over
		}
		draw.Draw(canvas, area, picture, picture.Bounds().Min, operator)
		// the times while this frame is shown
		for len(at) > 0 && at[0]*1000 < float64(start+frame.duration) {
			shown = append(shown, image.Image(cloneRGBA(canvas)))
			at = at[1:]
		}
		start += frame.duration
		if frame.dispose {
			draw.Draw(canvas, area, image.Transparent, image.Point{}, draw.Src)
		}
	}
	if len(shown) == 0 {
		shown = append(shown, canvas)
	}
	return shown, float64(length) / 1000, nil
}

func cloneRGBA(source *image.RGBA) *image.RGBA {
	clone := image.NewRGBA(source.Bounds())
	copy(clone.Pix, source.Pix)
	return clone
}

func uint24(b []byte) int {
	return int(b[0]) | int(b[1])<<8 | int(b[2])<<16
}

func putUint24(b []byte, value int) {
	b[0], b[1], b[2] = byte(value), byte(value>>8), byte(value>>16)
}
