package avatarcache

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"os"
	"testing"
)

func TestRenderStaticTakesFirstWebPFrame(t *testing.T) {
	for _, name := range []string{"testdata/anim2.webp", "testdata/anim2-lossy.webp"} {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if !isAnimatedWebP(src) {
			t.Fatalf("%s: fixture not animated", name)
		}
		out, ext, err := RenderStatic(src, 48)
		if err != nil || (ext != ".jpg" && ext != ".png") {
			t.Fatalf("%s: ext=%q err=%v", name, ext, err)
		}
		img, _, err := image.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatal(err)
		}
		r, g, b, _ := img.At(img.Bounds().Dx()/2, img.Bounds().Dy()/2).RGBA()
		if r>>8 < 200 || g>>8 > 60 || b>>8 > 60 {
			t.Fatalf("%s: first frame should be red, got %d,%d,%d", name, r>>8, g>>8, b>>8)
		}
		// The animated rendition still keeps the original.
		if anim, ext, _ := Render(src, 48); ext != ".webp" || !bytes.Equal(anim, src) {
			t.Fatalf("%s: animated rendition changed", name)
		}
	}
}

func TestRenderStaticTakesFirstGIFFrame(t *testing.T) {
	pal := color.Palette{color.RGBA{0, 200, 0, 255}, color.RGBA{0, 0, 200, 255}}
	g := &gif.GIF{}
	for i := range 2 {
		f := image.NewPaletted(image.Rect(0, 0, 20, 20), pal)
		for p := range f.Pix {
			f.Pix[p] = uint8(i)
		}
		g.Image = append(g.Image, f)
		g.Delay = append(g.Delay, 10)
	}
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, g); err != nil {
		t.Fatal(err)
	}
	out, ext, err := RenderStatic(b.Bytes(), 48)
	if err != nil || ext != ".jpg" {
		t.Fatalf("ext=%q err=%v", ext, err)
	}
	img, _, _ := image.Decode(bytes.NewReader(out))
	if _, gg, _, _ := img.At(10, 10).RGBA(); gg>>8 < 150 {
		t.Fatal("first frame should be green")
	}
}

func TestRenderStaticFallsBackForBrokenAnimation(t *testing.T) {
	src := animatedWebP(0x02) // frames are not decodable
	out, ext, err := RenderStatic(src, 48)
	if err != nil || ext != ".webp" || !bytes.Equal(out, src) {
		t.Fatalf("ext=%q err=%v", ext, err)
	}
}
