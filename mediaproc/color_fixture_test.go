package mediaproc

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	lcms "github.com/mgilbir/golittlecms"
)

func TestWideGamutICCConvertsPixelsAndRejectsBadProfiles(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	ctx := context.Background()
	dir := t.TempDir()
	cfg := allOn()
	limits := Limits{MaxImagePixels: 16 * 1024 * 1024, MaxVideoPixels: 8294400, MaxFrameRate: 120, MaxDuration: 300, MaxImageBytes: 10 << 20, MaxVideoBytes: 40 << 20}
	profile := displayP3Profile(t)
	src := color.NRGBA{R: 40, G: 255, B: 20, A: 180}
	other := color.NRGBA{R: 255, G: 20, B: 200, A: 90}
	want := convertPixel(t, profile, src)
	wantOther := convertPixel(t, profile, other)
	if absInt(int(want.R)-int(src.R))+absInt(int(want.G)-int(src.G))+absInt(int(want.B)-int(src.B)) < 20 {
		t.Fatalf("reference transform left the pixel unchanged: %v", want)
	}

	alpha := fillNRGBA(16, 16, src)
	if err := os.MkdirAll(filepath.Join(dir, "alpha-out"), 0o700); err != nil {
		t.Fatal(err)
	}
	alphaPath := filepath.Join(dir, "alpha-p3.png")
	if err := os.WriteFile(alphaPath, insertICCP(mustPNG(t, alpha), profile), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := ProcessFile(ctx, alphaPath, filepath.Join(dir, "alpha-out"), "image/png", cfg, limits)
	if err != nil {
		t.Fatal(err)
	}
	got := sampleRGBA(t, out.Path, 8, 8)
	assertNear(t, "transparent png", got, want, 12)
	if absInt(int(got.A)-int(src.A)) > 12 {
		t.Fatalf("alpha = %d, source %d", got.A, src.A)
	}

	orientDir := filepath.Join(dir, "orient-out")
	if err := os.MkdirAll(orientDir, 0o700); err != nil {
		t.Fatal(err)
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, fillNRGBA(8, 4, src), &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	orientPath := filepath.Join(dir, "orient-p3.jpg")
	if err := os.WriteFile(orientPath, insertJPEGICC(insertEXIF(encoded.Bytes(), 6), profile), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err = ProcessFile(ctx, orientPath, orientDir, "image/jpeg", cfg, limits)
	if err != nil {
		t.Fatal(err)
	}
	if out.Info.Height <= out.Info.Width {
		t.Fatalf("orientation was not applied before color conversion: %dx%d", out.Info.Width, out.Info.Height)
	}
	device := sampleRGBA(t, orientPath, 1, 2)
	jpegWant := convertPixel(t, profile, color.NRGBA{R: device.R, G: device.G, B: device.B, A: 255})
	if absInt(int(jpegWant.G)-int(device.G))+absInt(int(jpegWant.R)-int(device.R))+absInt(int(jpegWant.B)-int(device.B)) < 15 {
		t.Fatalf("decoded wide-gamut jpeg is already sRGB: device %v", device)
	}
	got = sampleRGBA(t, out.Path, out.Info.Width/2, out.Info.Height/2)
	assertNear(t, "oriented jpeg", got, jpegWant, 12)

	animDir := filepath.Join(dir, "anim-out")
	if err := os.MkdirAll(animDir, 0o700); err != nil {
		t.Fatal(err)
	}
	animPath := filepath.Join(dir, "anim-p3.png")
	if err := os.WriteFile(animPath, insertICCP(twoFrameAPNG(t, dir, src, other), profile), 0o600); err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareSRGBImage(ctx, animPath, animDir)
	if err != nil {
		t.Fatal(err)
	}
	frames := allRGBA(t, prepared)
	if len(frames) < 2 {
		t.Fatalf("converted animation frames = %d", len(frames))
	}
	assertNear(t, "frame 0", frames[0], want, 2)
	assertNear(t, "frame 1", frames[1], wantOther, 2)
	if frames[0].A != src.A || frames[1].A != other.A {
		t.Fatalf("animation alpha = %d,%d", frames[0].A, frames[1].A)
	}
	out, err = ProcessFile(ctx, animPath, animDir, "image/png", cfg, limits)
	if err != nil {
		t.Fatal(err)
	}
	published, err := ProbeFile(ctx, out.Path)
	if err != nil || !published.Animated {
		t.Fatalf("published animation probe %+v err %v", published, err)
	}

	if err := os.MkdirAll(filepath.Join(dir, "bad-out"), 0o700); err != nil || os.MkdirAll(filepath.Join(dir, "link-out"), 0o700) != nil {
		t.Fatal(err)
	}
	badPath := filepath.Join(dir, "bad.png")
	if err := os.WriteFile(badPath, insertICCP(mustPNG(t, fillNRGBA(8, 8, src)), []byte("this is not an icc profile")), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = ProcessFile(ctx, badPath, filepath.Join(dir, "bad-out"), "image/png", cfg, limits); err == nil {
		t.Fatal("malformed icc profile was published")
	}
	link := deviceLinkProfile(t)
	linkPath := filepath.Join(dir, "link.png")
	if err := os.WriteFile(linkPath, insertICCP(mustPNG(t, fillNRGBA(8, 8, src)), link), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = ProcessFile(ctx, linkPath, filepath.Join(dir, "link-out"), "image/png", cfg, limits); err == nil {
		t.Fatal("device-link profile was published")
	}
}

func fillNRGBA(w, h int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

func mustPNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func twoFrameAPNG(t *testing.T, dir string, a, b color.NRGBA) []byte {
	t.Helper()
	pix := append(append([]byte{}, framePix(fillNRGBA(16, 16, a))...), framePix(fillNRGBA(16, 16, b))...)
	out := filepath.Join(dir, "raw.apng")
	if err := writeAPNG(out, 16, 16, pix); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func framePix(img *image.NRGBA) []byte {
	return append([]byte{}, img.Pix...)
}

func displayP3Profile(t *testing.T) []byte {
	t.Helper()
	curve, err := lcms.BuildParametricToneCurve(4, []float64{2.4, 1.0 / 1.055, 0.055 / 1.055, 1.0 / 12.92, 0.04045})
	if err != nil {
		t.Fatal(err)
	}
	white := lcms.CIExyY{X: 0.3127, Y: 0.3290, YY: 1}
	primaries := lcms.CIExyYTRIPLE{
		Red:   lcms.CIExyY{X: 0.680, Y: 0.320, YY: 1},
		Green: lcms.CIExyY{X: 0.265, Y: 0.690, YY: 1},
		Blue:  lcms.CIExyY{X: 0.150, Y: 0.060, YY: 1},
	}
	p, err := lcms.CreateRGBProfile(&white, &primaries, []*lcms.ToneCurve{curve, curve, curve})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.CloseProfile() }()
	raw, err := p.SaveProfileToMem()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func deviceLinkProfile(t *testing.T) []byte {
	t.Helper()
	p, err := lcms.CreateInkLimitingDeviceLink(lcms.SigCmykData, 50)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.CloseProfile() }()
	raw, err := p.SaveProfileToMem()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func convertPixel(t *testing.T, profile []byte, src color.NRGBA) color.NRGBA {
	t.Helper()
	in, err := lcms.OpenProfileFromMem(profile)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.CloseProfile() }()
	out, err := lcms.Create_sRGBProfile()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = out.CloseProfile() }()
	xf, err := lcms.CreateTransform(in, lcms.TypeRGBA8, out, lcms.TypeRGBA8, lcms.IntentRelativeColorimetric, lcms.FlagsCopyAlpha)
	if err != nil {
		t.Fatal(err)
	}
	pix := []byte{src.R, src.G, src.B, src.A}
	dst := make([]byte, 4)
	xf.DoTransform(pix, dst, 1)
	return color.NRGBA{R: dst[0], G: dst[1], B: dst[2], A: dst[3]}
}

func assertNear(t *testing.T, name string, got, want color.NRGBA, tol int) {
	t.Helper()
	if absInt(int(got.R)-int(want.R)) > tol || absInt(int(got.G)-int(want.G)) > tol || absInt(int(got.B)-int(want.B)) > tol {
		t.Fatalf("%s pixel got %v want %v ±%d", name, got, want, tol)
	}
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func insertICCP(pngBytes, profile []byte) []byte {
	var zbuf bytes.Buffer
	zw := zlib.NewWriter(&zbuf)
	_, _ = zw.Write(profile)
	_ = zw.Close()
	payload := append([]byte("icc\x00\x00"), zbuf.Bytes()...)
	chunk := iccChunk("iCCP", payload)
	end := 8 + 8 + 13 + 4
	out := append([]byte{}, pngBytes[:end]...)
	out = append(out, chunk...)
	return append(out, pngBytes[end:]...)
}

func iccChunk(kind string, data []byte) []byte {
	body := append([]byte(kind), data...)
	out := []byte{byte(len(data) >> 24), byte(len(data) >> 16), byte(len(data) >> 8), byte(len(data))}
	out = append(out, body...)
	sum := make([]byte, 4)
	binary.BigEndian.PutUint32(sum, crc32.ChecksumIEEE(body))
	return append(out, sum...)
}

func insertJPEGICC(jpg, profile []byte) []byte {
	body := append([]byte("ICC_PROFILE\x00\x01\x01"), profile...)
	seg := []byte{0xff, 0xe2, byte((len(body) + 2) >> 8), byte(len(body) + 2)}
	seg = append(seg, body...)
	out := append([]byte{}, jpg[:2]...)
	out = append(out, seg...)
	return append(out, jpg[2:]...)
}

func insertEXIF(jpg []byte, orientation int) []byte {
	payload := append([]byte("Exif\x00\x00"), exifTIFF(orientation)...)
	app := []byte{0xff, 0xe1, byte((len(payload) + 2) >> 8), byte(len(payload) + 2)}
	app = append(app, payload...)
	out := append([]byte{}, jpg[:2]...)
	out = append(out, app...)
	return append(out, jpg[2:]...)
}

func sampleRGBA(t *testing.T, path string, x, y int) color.NRGBA {
	t.Helper()
	frames := decodedFrames(t, path)
	if len(frames) == 0 {
		t.Fatal("no decoded frames")
	}
	return straightNRGBA(frames[0], x, y)
}

func allRGBA(t *testing.T, path string) []color.NRGBA {
	t.Helper()
	var out []color.NRGBA
	for _, frame := range decodedFrames(t, path) {
		out = append(out, straightNRGBA(frame, 0, 0))
	}
	return out
}

func straightNRGBA(img image.Image, x, y int) color.NRGBA {
	switch p := img.(type) {
	case *image.NRGBA:
		return p.NRGBAAt(x, y)
	default:
		r, g, b, a := img.At(x, y).RGBA()
		if a == 0 {
			return color.NRGBA{}
		}
		return color.NRGBA{R: uint8((r * 0xffff / a) >> 8), G: uint8((g * 0xffff / a) >> 8), B: uint8((b * 0xffff / a) >> 8), A: uint8(a >> 8)}
	}
}

func decodedFrames(t *testing.T, path string) []image.Image {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("ffmpeg", "-hide_banner", "-y", "-vsync", "0", "-i", path, "-frames:v", "4", filepath.Join(dir, "f%03d.png"))
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("decode %s: %v\n%s", path, err, msg)
	}
	matches, err := filepath.Glob(filepath.Join(dir, "f*.png"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("frames: %v %d", err, len(matches))
	}
	var out []image.Image
	for _, name := range matches {
		f, err := os.Open(name)
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(f)
		_ = f.Close()
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, img)
	}
	return out
}
