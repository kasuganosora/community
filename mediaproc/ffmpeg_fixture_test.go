package mediaproc

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kasuganosora/community/systemsettings"
)

func TestFixtureTranscodeAndReject(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe is not installed")
	}
	ctx := context.Background()
	dir := t.TempDir()
	cfg := allOn()
	limits := Limits{MaxImagePixels: 16 * 1024 * 1024, MaxVideoPixels: 8294400, MaxFrameRate: 120, MaxDuration: 300, MaxImageBytes: 10 << 20, MaxVideoBytes: 40 << 20}

	jpegPath := filepath.Join(dir, "tagged.jpg")
	run(t, "ffmpeg", "-hide_banner", "-y", "-f", "lavfi", "-i", "color=c=red:s=16x16", "-frames:v", "1",
		"-metadata", "comment=SECRETCOMMENT", "-metadata", "make=CameraX", "-metadata", "location=+10.000+20.000", jpegPath)
	out, err := ProcessFile(ctx, jpegPath, dir, "image/jpeg", cfg, limits)
	if err != nil {
		t.Fatal(err)
	}
	if out.MIME != "image/webp" || out.KeptOriginal {
		t.Fatalf("jpeg output: %+v", out)
	}
	body, _ := os.ReadFile(out.Path)
	if bytes.Contains(body, []byte("SECRETCOMMENT")) || bytes.Contains(body, []byte("CameraX")) {
		t.Fatal("generated webp kept source metadata text")
	}
	t.Logf("jpeg bytes in=%d out=%d pix=%s alpha=%v", out.InputBytes, out.OutputBytes, out.Info.PixFmt, out.Info.HasAlpha)

	alphaPath := filepath.Join(dir, "alpha.png")
	writePNG(t, alphaPath, 16, 8, color.NRGBA{R: 255, G: 0, B: 0, A: 128})
	alphaDir := filepath.Join(dir, "alpha")
	if err = os.Mkdir(alphaDir, 0o700); err != nil {
		t.Fatal(err)
	}
	out, err = ProcessFile(ctx, alphaPath, alphaDir, "image/png", cfg, limits)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Info.HasAlpha {
		t.Fatalf("alpha not preserved: %+v", out.Info)
	}

	gifPath := filepath.Join(dir, "anim.gif")
	writeGIF(t, gifPath)
	out, err = ProcessFile(ctx, gifPath, dir, "image/gif", cfg, limits)
	if err != nil {
		t.Fatal(err)
	}
	probed, err := ProbeFile(ctx, out.Path)
	if err != nil || !probed.Animated {
		t.Fatalf("animation probe %+v err %v", probed, err)
	}

	webmSrc := filepath.Join(dir, "clip.mp4")
	run(t, "ffmpeg", "-hide_banner", "-y", "-f", "lavfi", "-i", "color=c=blue:s=64x64:d=0.4", "-f", "lavfi", "-i", "sine=f=440:d=0.4",
		"-metadata", "location=+10.000+20.000", "-metadata", "make=PhoneX", "-shortest", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", webmSrc)
	videoDir := filepath.Join(dir, "video")
	if err = os.Mkdir(videoDir, 0o700); err != nil {
		t.Fatal(err)
	}
	out, err = ProcessFile(ctx, webmSrc, videoDir, "video/mp4", cfg, limits)
	if err != nil {
		t.Fatal(err)
	}
	if out.MIME != "video/webm" || !out.Info.HasAudio {
		t.Fatalf("video output %+v", out)
	}
	t.Logf("video bytes in=%d out=%d duration=%.3f", out.InputBytes, out.OutputBytes, out.Info.Duration)

	sameDir := filepath.Join(dir, "same")
	_ = os.Mkdir(sameDir, 0o700)
	tagged := filepath.Join(dir, "tagged.webp")
	srcWebP := outPath(t, ctx, dir, alphaPath, "image/png", cfg, limits)
	run(t, "ffmpeg", "-hide_banner", "-y", "-i", srcWebP, "-metadata", "comment=SECRETCOMMENT", "-c:v", "libwebp", "-quality", "80", tagged)
	sameCfg := systemsettings.MediaProcessing{ImageForceReencode: true, ImageQuality: 80}
	again, err := ProcessFile(ctx, tagged, sameDir, "image/webp", sameCfg, limits)
	if err != nil {
		t.Fatal(err)
	}
	next, _ := os.ReadFile(again.Path)
	if again.KeptOriginal || again.MIME != "image/webp" || bytes.Contains(next, []byte("SECRETCOMMENT")) {
		t.Fatalf("same-format webp was kept or still tagged (kept=%v mime=%s)", again.KeptOriginal, again.MIME)
	}
	t.Logf("same-format webp bytes in=%d out=%d", again.InputBytes, again.OutputBytes)

	orient := filepath.Join(dir, "orient.jpg")
	if err = os.WriteFile(orient, jpegOrientation(8, 4, 6), 0o600); err != nil {
		t.Fatal(err)
	}
	orientDir := filepath.Join(dir, "orient-out")
	if err = os.Mkdir(orientDir, 0o700); err != nil {
		t.Fatal(err)
	}
	out, err = ProcessFile(ctx, orient, orientDir, "image/jpeg", cfg, limits)
	if err != nil {
		t.Fatal(err)
	}
	if out.Info.Height <= out.Info.Width {
		t.Fatalf("orientation was not applied: %dx%d", out.Info.Width, out.Info.Height)
	}

	privacyDir := filepath.Join(dir, "privacy")
	requireDir(t, privacyDir)
	privacySrc := filepath.Join(privacyDir, "tagged.jpg")
	run(t, "ffmpeg", "-hide_banner", "-y", "-f", "lavfi", "-i", "color=c=red:s=16x16", "-frames:v", "1",
		"-metadata", "comment=SECRETCOMMENT", "-metadata", "artist=HiddenAuthor", "-metadata", "xyzzy=ARBITRARYMETA", privacySrc)
	privacyOnly := systemsettings.MediaProcessing{StripPrivacy: true, ImageQuality: 80}
	out, err = ProcessFile(ctx, privacySrc, privacyDir, "image/jpeg", privacyOnly, limits)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = os.ReadFile(out.Path)
	for _, marker := range []string{"SECRETCOMMENT", "HiddenAuthor", "ARBITRARYMETA"} {
		if bytes.Contains(body, []byte(marker)) {
			t.Fatalf("privacy rewrite kept %s", marker)
		}
	}
	for key := range out.Info.Tags {
		if !allowedMetadata(key, out.Info.Tags[key]) {
			t.Fatalf("privacy output kept tag %s", key)
		}
	}

	half, _ := os.ReadFile(jpegPath)
	bad := filepath.Join(dir, "half.jpg")
	_ = os.WriteFile(bad, half[:len(half)/2], 0o600)
	if _, err = ProcessFile(ctx, bad, dir, "image/jpeg", cfg, limits); err == nil {
		t.Fatal("truncated jpeg was accepted")
	}
	bomb := filepath.Join(dir, "bomb.png")
	_ = os.WriteFile(bomb, pngHeader(100000, 100000), 0o600)
	if _, err = ProcessFile(ctx, bomb, dir, "image/png", cfg, limits); err == nil {
		t.Fatal("oversized header was accepted")
	}
}

func TestFixtureOriginalWebMAudioPrivacyColorAndProgress(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil || exec.Command("ffprobe", "-version").Run() != nil {
		t.Skip("ffmpeg or ffprobe is not installed")
	}
	ctx := context.Background()
	dir := t.TempDir()
	cfg := allOn()
	limits := Limits{MaxImagePixels: 16 * 1024 * 1024, MaxVideoPixels: 8294400, MaxFrameRate: 120, MaxDuration: 300, MaxImageBytes: 10 << 20, MaxVideoBytes: 40 << 20}
	src := filepath.Join(dir, "orig.webm")
	run(t, "ffmpeg", "-hide_banner", "-y", "-f", "lavfi", "-i", "color=c=green:s=64x64:d=1", "-f", "lavfi", "-i", "sine=f=440:d=1",
		"-shortest", "-c:v", "libvpx-vp9", "-b:v", "200k", "-c:a", "libopus",
		"-metadata", "comment=SECRETCOMMENT", "-metadata", "location=+10.000+20.000", "-metadata", "make=PhoneX", src)
	original, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	var progress []int
	out, err := ProcessFile(WithProgress(ctx, func(p int) { progress = append(progress, p) }), src, dir, "video/webm", cfg, limits)
	if err != nil {
		t.Fatal(err)
	}
	if out.KeptOriginal || out.MIME != "video/webm" || !out.Info.HasAudio {
		t.Fatalf("webm output: %+v", out)
	}
	body, _ := os.ReadFile(out.Path)
	if bytes.Equal(body, original) {
		t.Fatal("original webm was copied instead of re-encoded")
	}
	for _, marker := range []string{"SECRETCOMMENT", "+10.000+20.000", "PhoneX"} {
		if bytes.Contains(body, []byte(marker)) {
			t.Fatalf("webm kept %s", marker)
		}
	}
	args := BuildEncodeArgs(src, out.Path, Plan{Action: ActionEncode, OutputMIME: "video/webm"}, Probe{HasAudio: true, ColorPrimaries: "bt709"}, cfg)
	if slicesContains(args, "copy") {
		t.Fatalf("stream copy in webm args: %v", args)
	}
	mid := false
	for _, p := range progress {
		if p > 0 && p < 100 {
			mid = true
		}
	}
	if !mid {
		t.Fatalf("ffmpeg progress did not move: %v", progress)
	}
	thumb, _, _, _, err := defaultPreview(ctx, out.Path, out.MIME, dir)
	if err != nil || thumb == "" {
		t.Fatalf("thumbnail: %s %v", thumb, err)
	}
	poster, err := os.ReadFile(filepath.Join(dir, thumb))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"SECRETCOMMENT", "+10.000+20.000", "PhoneX"} {
		if bytes.Contains(poster, []byte(marker)) {
			t.Fatalf("thumbnail kept %s", marker)
		}
	}
	wide := filepath.Join(dir, "wide.mp4")
	run(t, "ffmpeg", "-hide_banner", "-y", "-f", "lavfi", "-i", "color=c=red:s=64x64:d=0.3", "-c:v", "libx264", "-pix_fmt", "yuv420p",
		"-color_primaries", "bt2020", "-color_trc", "bt2020-10", "-colorspace", "bt2020nc", "-an", wide)
	converted, err := ProcessFile(ctx, wide, dir, "video/mp4", cfg, limits)
	if err != nil {
		t.Fatal(err)
	}
	probe, err := ProbeFile(ctx, converted.Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ToLower(probe.ColorPrimaries) != "bt709" {
		t.Fatalf("wide-gamut video primaries = %q", probe.ColorPrimaries)
	}
}

func slicesContains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func outPath(t *testing.T, ctx context.Context, dir, src, mime string, cfg systemsettings.MediaProcessing, limits Limits) string {
	t.Helper()
	work := filepath.Join(dir, "seed-"+filepath.Base(src))
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	out, err := ProcessFile(ctx, src, work, mime, cfg, limits)
	if err != nil {
		t.Fatal(err)
	}
	return out.Path
}

func requireDir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, name string, args ...string) {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

func writePNG(t *testing.T, path string, w, h int, c color.Color) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeGIF(t *testing.T, path string) {
	t.Helper()
	pal := color.Palette{color.Black, color.White, color.RGBA{255, 0, 0, 255}}
	a := image.NewPaletted(image.Rect(0, 0, 8, 8), pal)
	b := image.NewPaletted(image.Rect(0, 0, 8, 8), pal)
	for i := range 8 {
		a.SetColorIndex(i, i, 1)
		b.SetColorIndex(i, i, 2)
	}
	g := &gif.GIF{Image: []*image.Paletted{a, b}, Delay: []int{10, 10}}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func jpegOrientation(w, h, orientation int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90})
	raw := buf.Bytes()
	payload := append([]byte("Exif\x00\x00"), exifTIFF(orientation)...)
	app := []byte{0xff, 0xe1, byte(len(payload)+2) >> 8, byte(len(payload) + 2)}
	app = append(app, payload...)
	out := append([]byte{}, raw[:2]...)
	out = append(out, app...)
	out = append(out, raw[2:]...)
	return out
}

func exifTIFF(orientation int) []byte {
	tiff := []byte{
		'I', 'I', 0x2a, 0x00, 0x08, 0x00, 0x00, 0x00,
		0x01, 0x00,
		0x12, 0x01, 0x03, 0x00, 0x01, 0x00, 0x00, 0x00,
		byte(orientation), 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00,
	}
	return tiff
}

func pngHeader(w, h int) []byte {
	// Signature plus an IHDR that declares a huge canvas and no image data.
	sig := []byte{137, 80, 78, 71, 13, 10, 26, 10}
	ihdr := make([]byte, 13)
	ihdr[0] = byte(w >> 24)
	ihdr[1] = byte(w >> 16)
	ihdr[2] = byte(w >> 8)
	ihdr[3] = byte(w)
	ihdr[4] = byte(h >> 24)
	ihdr[5] = byte(h >> 16)
	ihdr[6] = byte(h >> 8)
	ihdr[7] = byte(h)
	ihdr[8] = 8
	ihdr[9] = 2
	chunk := pngChunk("IHDR", ihdr)
	return append(append(sig, chunk...), pngChunk("IEND", nil)...)
}

func pngChunk(kind string, data []byte) []byte {
	n := len(data)
	out := []byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}
	out = append(out, kind...)
	out = append(out, data...)
	// CRC is wrong on purpose; decoders must still refuse the dimensions
	// before trusting the rest. A correct CRC is not required to reject.
	return append(out, 0, 0, 0, 0)
}
