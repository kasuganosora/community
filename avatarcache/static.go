package avatarcache

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/draw"
	"image/gif"
)

// RenderStatic is Render for a still rendition: the first frame of an
// animated GIF or WebP, scaled like any other avatar. When no frame can be
// decoded it falls back to Render (the original is served).
func RenderStatic(src []byte, size int) ([]byte, string, error) {
	switch sniff(src) {
	case "image/webp":
		if isAnimatedWebP(src) {
			if still := firstWebPFrame(src); still != nil {
				if img, _, err := image.Decode(bytes.NewReader(still)); err == nil {
					return encodeScaled(img, size)
				}
			}
		}
	case "image/gif":
		if g, err := gif.DecodeAll(bytes.NewReader(src)); err == nil && len(g.Image) > 1 {
			canvas := image.NewRGBA(image.Rect(0, 0, g.Config.Width, g.Config.Height))
			draw.Draw(canvas, g.Image[0].Bounds(), g.Image[0], g.Image[0].Bounds().Min, draw.Over)
			return encodeScaled(canvas, size)
		}
	}
	return Render(src, size)
}

// firstWebPFrame rebuilds the first ANMF frame of an animated WebP as a
// still WebP file (VP8L, or VP8 with an optional ALPH chunk), which the
// standard decoder reads. It returns nil when the file has no usable frame.
func firstWebPFrame(src []byte) []byte {
	if len(src) < 12 || string(src[0:4]) != "RIFF" || string(src[8:12]) != "WEBP" {
		return nil
	}
	for off := 12; off+8 <= len(src); {
		id := string(src[off : off+4])
		n := int(binary.LittleEndian.Uint32(src[off+4 : off+8]))
		if n < 0 || off+8+n > len(src) {
			return nil
		}
		payload := src[off+8 : off+8+n]
		if id == "ANMF" {
			return stillFromFrame(payload)
		}
		off += 8 + n + n&1
	}
	return nil
}

func stillFromFrame(frame []byte) []byte {
	if len(frame) < 16 {
		return nil
	}
	w := 1 + (int(frame[6]) | int(frame[7])<<8 | int(frame[8])<<16)
	h := 1 + (int(frame[9]) | int(frame[10])<<8 | int(frame[11])<<16)
	var alph, bits []byte
	var bitsID string
	data := frame[16:]
	for off := 0; off+8 <= len(data); {
		id := string(data[off : off+4])
		n := int(binary.LittleEndian.Uint32(data[off+4 : off+8]))
		if n < 0 || off+8+n > len(data) {
			return nil
		}
		chunk := data[off : off+8+n]
		switch id {
		case "ALPH":
			alph = chunk
		case "VP8 ", "VP8L":
			bits, bitsID = chunk, id
		}
		off += 8 + n + n&1
	}
	if bits == nil {
		return nil
	}
	pad := func(c []byte) []byte {
		if len(c)%2 == 1 {
			return append(append([]byte{}, c...), 0)
		}
		return c
	}
	body := []byte("WEBP")
	if alph != nil && bitsID == "VP8 " {
		vp8x := make([]byte, 18)
		copy(vp8x, "VP8X")
		binary.LittleEndian.PutUint32(vp8x[4:], 10)
		vp8x[8] = 0x10 // alpha
		vp8x[12], vp8x[13], vp8x[14] = byte(w-1), byte((w-1)>>8), byte((w-1)>>16)
		vp8x[15], vp8x[16], vp8x[17] = byte(h-1), byte((h-1)>>8), byte((h-1)>>16)
		body = append(body, vp8x...)
		body = append(body, pad(alph)...)
	}
	body = append(body, pad(bits)...)
	out := append([]byte("RIFF"), 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(out[4:], uint32(len(body)))
	return append(out, body...)
}
