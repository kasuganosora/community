package avatarcache

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func jpegOf(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = byte(i)
		if i%4 == 3 {
			img.Pix[i] = 0xff
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestNormalizeSize(t *testing.T) {
	for in, want := range map[string]int{"": 400, "x": 400, "-3": 400, "1": 48, "48": 48, "49": 96, "150": 192, "9999": 400} {
		if got := NormalizeSize(in); got != want {
			t.Errorf("NormalizeSize(%q)=%d want %d", in, got, want)
		}
	}
}

func TestRenderResizesAndPicksFormat(t *testing.T) {
	out, ext, err := Render(jpegOf(t, 800, 600), 96)
	if err != nil || ext != ".jpg" {
		t.Fatalf("ext=%s err=%v", ext, err)
	}
	cfg, _, _ := image.DecodeConfig(bytes.NewReader(out))
	if cfg.Width != 96 || cfg.Height != 96 {
		t.Fatalf("got %dx%d", cfg.Width, cfg.Height)
	}
	// Never upscale.
	out, _, _ = Render(jpegOf(t, 30, 40), 400)
	if cfg, _, _ = image.DecodeConfig(bytes.NewReader(out)); cfg.Width != 30 {
		t.Fatalf("upscaled to %d", cfg.Width)
	}
	// Transparency survives as PNG.
	img := image.NewNRGBA(image.Rect(0, 0, 100, 100))
	img.Set(1, 1, color.NRGBA{255, 0, 0, 128})
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	if _, ext, err = Render(b.Bytes(), 48); err != nil || ext != ".png" {
		t.Fatalf("alpha: ext=%s err=%v", ext, err)
	}
}

func TestRenderRejectsInvalidAndHuge(t *testing.T) {
	if _, _, err := Render([]byte("<svg xmlns='http://www.w3.org/2000/svg'/>"), 48); !errors.Is(err, ErrInvalidType) {
		t.Fatalf("svg: %v", err)
	}
	if _, _, err := Render([]byte("\x89PNG\r\n\x1a\ngarbage"), 48); !errors.Is(err, ErrInvalidType) {
		t.Fatalf("broken png: %v", err)
	}
	// A tiny GIF whose header claims 20000x20000 (decompression bomb).
	var b bytes.Buffer
	_ = gif.Encode(&b, image.NewPaletted(image.Rect(0, 0, 1, 1), color.Palette{color.Black, color.White}), nil)
	bomb := b.Bytes()
	bomb[6], bomb[7], bomb[8], bomb[9] = 0x20, 0x4e, 0x20, 0x4e
	if _, _, err := Render(bomb, 48); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("bomb: %v", err)
	}
}

func TestAnimatedGIFKeptAsIs(t *testing.T) {
	p := color.Palette{color.Black, color.White}
	g := &gif.GIF{Image: []*image.Paletted{image.NewPaletted(image.Rect(0, 0, 4, 4), p), image.NewPaletted(image.Rect(0, 0, 4, 4), p)}, Delay: []int{1, 1}}
	var b bytes.Buffer
	_ = gif.EncodeAll(&b, g)
	out, ext, err := Render(b.Bytes(), 48)
	if err != nil || ext != ".gif" || !bytes.Equal(out, b.Bytes()) {
		t.Fatalf("ext=%s err=%v", ext, err)
	}
}

func TestSingleflightAndHit(t *testing.T) {
	c, _ := New(t.TempDir(), 0)
	src := jpegOf(t, 200, 200)
	var calls atomic.Int32
	fetch := func(context.Context) ([]byte, error) {
		calls.Add(1)
		time.Sleep(50 * time.Millisecond)
		return src, nil
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Get(context.Background(), "k", 48, 0, fetch); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if _, err := c.Get(context.Background(), "k", 48, 0, fetch); err != nil {
		t.Fatal(err)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("fetched %d times", n)
	}
	// Another size is a separate entry.
	if _, err := c.Get(context.Background(), "k", 96, 0, fetch); err != nil || calls.Load() != 2 {
		t.Fatalf("size 96: calls=%d err=%v", calls.Load(), err)
	}
}

func TestTTLRevalidateAndStaleOnError(t *testing.T) {
	c, _ := New(t.TempDir(), 0)
	clock := time.Now()
	c.now = func() time.Time { return clock }
	src := jpegOf(t, 64, 64)
	var calls int
	ok := func(context.Context) ([]byte, error) { calls++; return src, nil }
	fail := func(context.Context) ([]byte, error) { calls++; return nil, errors.New("down") }
	if _, err := c.Get(context.Background(), "r", 48, time.Hour, ok); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(30 * time.Minute)
	_, _ = c.Get(context.Background(), "r", 48, time.Hour, fail)
	if calls != 1 {
		t.Fatalf("fresh entry refetched")
	}
	clock = clock.Add(time.Hour)
	if e, err := c.Get(context.Background(), "r", 48, time.Hour, fail); err != nil || e == nil {
		t.Fatalf("stale entry not served on error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expired entry not revalidated")
	}
	if e, _ := c.Get(context.Background(), "r", 48, time.Hour, ok); !e.ModTime.Equal(clock) || calls != 3 {
		t.Fatalf("refresh not stored")
	}
	if _, err := c.Get(context.Background(), "missing", 48, time.Hour, fail); err == nil {
		t.Fatal("miss with failing fetch must error")
	}
}

func TestEvictLRU(t *testing.T) {
	dir := t.TempDir()
	c, _ := New(dir, 1)
	clock := time.Now().Add(-10 * time.Hour)
	c.now = func() time.Time { return clock }
	src := jpegOf(t, 100, 100)
	var paths []string
	for _, k := range []string{"a", "b", "c"} {
		e, err := c.Get(context.Background(), k, 48, 0, func(context.Context) ([]byte, error) { return src, nil })
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, e.Path)
		clock = clock.Add(2 * time.Hour)
	}
	// Access "a" now: it becomes the most recently used.
	_, _ = c.Get(context.Background(), "a", 48, 0, nil)
	info, _ := os.Stat(paths[0])
	size := info.Size()
	c.maxBytes = size*2 + size/2 // room for two; target 90% keeps two
	if _, err := c.Evict(); err != nil {
		t.Fatal(err)
	}
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	if !exists(paths[0]) || exists(paths[1]) || !exists(paths[2]) {
		t.Fatalf("want a,c kept and b evicted: a=%v b=%v c=%v", exists(paths[0]), exists(paths[1]), exists(paths[2]))
	}
}
