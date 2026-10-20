package avatarcache

import (
	"bytes"
	"encoding/binary"
	"image"
	"testing"

	_ "golang.org/x/image/webp"
)

func webpChunk(id string, payload []byte) []byte {
	b := append([]byte(id), 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(b[4:], uint32(len(payload)))
	b = append(b, payload...)
	if len(payload)%2 == 1 {
		b = append(b, 0)
	}
	return b
}

// animatedWebP builds a minimal extended WebP whose VP8X header announces a
// 400x400 canvas; the frame data is not decodable, as with real animations
// the Go decoder rejects.
func animatedWebP(flags byte) []byte {
	vp8x := make([]byte, 10)
	vp8x[0] = flags
	w, h := 400-1, 400-1
	vp8x[4], vp8x[5], vp8x[6] = byte(w), byte(w>>8), byte(w>>16)
	vp8x[7], vp8x[8], vp8x[9] = byte(h), byte(h>>8), byte(h>>16)
	body := []byte("WEBP")
	body = append(body, webpChunk("VP8X", vp8x)...)
	body = append(body, webpChunk("ANIM", make([]byte, 6))...)
	body = append(body, webpChunk("ANMF", make([]byte, 40))...)
	out := append([]byte("RIFF"), 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(out[4:], uint32(len(body)))
	return append(out, body...)
}

func TestRenderPassesAnimatedWebPThrough(t *testing.T) {
	for name, flags := range map[string]byte{"animation flag": 0x02, "ANIM chunk only": 0x00} {
		src := animatedWebP(flags)
		if _, _, err := image.DecodeConfig(bytes.NewReader(src)); err != nil {
			t.Fatalf("%s: fixture should have readable dimensions: %v", name, err)
		}
		out, ext, err := Render(src, 96)
		if err != nil || ext != ".webp" || !bytes.Equal(out, src) {
			t.Fatalf("%s: ext=%q err=%v same=%v", name, ext, err, bytes.Equal(out, src))
		}
	}
}

func TestIsAnimatedWebP(t *testing.T) {
	if isAnimatedWebP([]byte("RIFF\x00\x00\x00\x00WEBPVP8 ")) || isAnimatedWebP(nil) {
		t.Fatal("plain/short webp reported animated")
	}
	if !isAnimatedWebP(animatedWebP(0x02)) {
		t.Fatal("animated webp not detected")
	}
}
