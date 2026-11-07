package mediaproc

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"

	lcms "github.com/mgilbir/golittlecms"
)

// prepareSRGBImage converts an embedded RGB ICC profile to sRGB after
// FFmpeg has applied orientation. Pixels are converted before any metadata
// is stripped. No profile, and the original path is returned. A profile that
// cannot be converted is an error, so the upload is not published with the
// wrong colors.
//
// Supported profiles are RGB input, display, output, and colorspace profiles
// that LittleCMS can transform to sRGB, including alpha. Gray, CMYK, device
// links, abstracts, named color, and malformed profiles are rejected.
func prepareSRGBImage(ctx context.Context, path, work string) (string, error) {
	raw, err := readICC(path)
	if err != nil || raw == nil {
		return path, err
	}
	src, err := lcms.OpenProfileFromMem(raw)
	if err != nil {
		return "", fmt.Errorf("%w: icc profile", ErrProcess)
	}
	defer func() { _ = src.CloseProfile() }()
	if !supportedRGBProfile(src) {
		return "", fmt.Errorf("%w: unsupported icc profile", ErrProcess)
	}
	dst, err := lcms.Create_sRGBProfile()
	if err != nil {
		return "", ErrProcess
	}
	defer func() { _ = dst.CloseProfile() }()
	xform, err := lcms.CreateTransform(src, lcms.TypeRGBA8, dst, lcms.TypeRGBA8, lcms.IntentRelativeColorimetric, lcms.FlagsCopyAlpha)
	if err != nil || xform == nil {
		return "", fmt.Errorf("%w: icc transform", ErrProcess)
	}
	w, h, pix, err := decodeOrientedRGBA(ctx, path, work)
	if err != nil {
		return "", err
	}
	frame := w * h * 4
	if frame == 0 || len(pix)%frame != 0 {
		return "", ErrProcess
	}
	converted := make([]byte, frame)
	for off := 0; off < len(pix); off += frame {
		xform.DoTransform(pix[off:off+frame], converted, uint32(w*h))
		copy(pix[off:off+frame], converted)
	}
	out := filepath.Join(work, "srgb-oriented.png")
	if len(pix) == frame {
		return out, writeSRGBPNG(out, w, h, pix)
	}
	out = filepath.Join(work, "srgb-oriented.apng")
	return out, writeAPNG(out, w, h, pix)
}

func supportedRGBProfile(p *lcms.Profile) bool {
	if p == nil || p.GetColorSpace() != lcms.SigRgbData {
		return false
	}
	switch p.GetDeviceClass() {
	case lcms.SigInputClass, lcms.SigDisplayClass, lcms.SigOutputClass, lcms.SigColorSpaceClass:
		return true
	default:
		return false
	}
}

func decodeOrientedRGBA(ctx context.Context, path, work string) (int, int, []byte, error) {
	sample := filepath.Join(work, "orient-sample.png")
	cmd, err := command(ctx, "ffmpeg", append(safeIOArgs(), "-y", "-i", path, "-frames:v", "1", "-update", "1", "-f", "image2", sample))
	if err != nil {
		return 0, 0, nil, ErrProcess
	}
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err = runLimited(cmd); err != nil {
		return 0, 0, nil, ErrProcess
	}
	f, err := os.Open(sample)
	if err != nil {
		return 0, 0, nil, ErrProcess
	}
	cfg, _, err := image.DecodeConfig(f)
	_ = f.Close()
	if err != nil || cfg.Width < 1 || cfg.Height < 1 {
		return 0, 0, nil, ErrProcess
	}
	rawCmd, err := command(ctx, "ffmpeg", append(safeIOArgs(), "-vsync", "0", "-i", path, "-f", "rawvideo", "-pix_fmt", "rgba", "pipe:1"))
	if err != nil {
		return 0, 0, nil, ErrProcess
	}
	var buf bytes.Buffer
	rawCmd.Stdout = &buf
	rawCmd.Stderr = io.Discard
	if err = runLimited(rawCmd); err != nil {
		return 0, 0, nil, ErrProcess
	}
	return cfg.Width, cfg.Height, buf.Bytes(), nil
}

func writeSRGBPNG(path string, w, h int, pix []byte) error {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	copy(img.Pix, pix)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return ErrProcess
	}
	defer func() { _ = f.Close() }()
	if err := png.Encode(f, img); err != nil {
		return ErrProcess
	}
	return nil
}

func writeAPNG(path string, w, h int, pix []byte) error {
	frameBytes := w * h * 4
	frames := len(pix) / frameBytes
	if frames < 2 || len(pix) != frames*frameBytes {
		return ErrProcess
	}
	idats := make([][]byte, frames)
	var ihdr []byte
	for i := 0; i < frames; i++ {
		chunkIHDR, idat, err := pngIHDRAndIDAT(w, h, pix[i*frameBytes:(i+1)*frameBytes])
		if err != nil {
			return ErrProcess
		}
		if i == 0 {
			ihdr = chunkIHDR
		}
		idats[i] = idat
	}
	var out bytes.Buffer
	out.Write([]byte{137, 80, 78, 71, 13, 10, 26, 10})
	out.Write(ihdr)
	acts := make([]byte, 8)
	binary.BigEndian.PutUint32(acts[0:], uint32(frames))
	binary.BigEndian.PutUint32(acts[4:], 1)
	out.Write(iccPNGChunk("acTL", acts))
	seq := uint32(0)
	for i, idat := range idats {
		out.Write(iccPNGChunk("fcTL", fctl(seq, w, h)))
		seq++
		if i == 0 {
			out.Write(idat)
			continue
		}
		body := make([]byte, 4+len(idatData(idat)))
		binary.BigEndian.PutUint32(body[0:], seq)
		copy(body[4:], idatData(idat))
		seq++
		out.Write(iccPNGChunk("fdAT", body))
	}
	out.Write(iccPNGChunk("IEND", nil))
	return os.WriteFile(path, out.Bytes(), 0o600)
}

func pngIHDRAndIDAT(w, h int, pix []byte) ([]byte, []byte, error) {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	copy(img.Pix, pix)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, nil, err
	}
	raw := buf.Bytes()
	var ihdr, idat []byte
	for off := 8; off+8 <= len(raw); {
		n := int(binary.BigEndian.Uint32(raw[off:]))
		kind := string(raw[off+4 : off+8])
		chunk := raw[off : off+8+n+4]
		switch kind {
		case "IHDR":
			ihdr = append([]byte(nil), chunk...)
		case "IDAT":
			idat = append(idat, chunk...)
		}
		off += 8 + n + 4
	}
	if ihdr == nil || idat == nil {
		return nil, nil, ErrProcess
	}
	return ihdr, idat, nil
}

func idatData(chunk []byte) []byte {
	var data []byte
	for off := 0; off+8 <= len(chunk); {
		n := int(binary.BigEndian.Uint32(chunk[off:]))
		data = append(data, chunk[off+8:off+8+n]...)
		off += 8 + n + 4
	}
	return data
}

func fctl(seq uint32, w, h int) []byte {
	b := make([]byte, 26)
	binary.BigEndian.PutUint32(b[0:], seq)
	binary.BigEndian.PutUint32(b[4:], uint32(w))
	binary.BigEndian.PutUint32(b[8:], uint32(h))
	binary.BigEndian.PutUint16(b[20:], 1)
	binary.BigEndian.PutUint16(b[22:], 10)
	return b
}

func iccPNGChunk(kind string, data []byte) []byte {
	body := append([]byte(kind), data...)
	out := []byte{byte(len(data) >> 24), byte(len(data) >> 16), byte(len(data) >> 8), byte(len(data))}
	out = append(out, body...)
	sum := make([]byte, 4)
	binary.BigEndian.PutUint32(sum, crc32.ChecksumIEEE(body))
	return append(out, sum...)
}

func readICC(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, ErrCorrupt
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, 12)
	n, _ := io.ReadFull(f, head)
	switch {
	case n >= 8 && bytes.Equal(head[:8], []byte{137, 80, 78, 71, 13, 10, 26, 10}):
		if _, err = f.Seek(8, io.SeekStart); err != nil {
			return nil, ErrCorrupt
		}
		return readPNGICC(f)
	case n >= 2 && head[0] == 0xff && head[1] == 0xd8:
		if _, err = f.Seek(2, io.SeekStart); err != nil {
			return nil, ErrCorrupt
		}
		return readJPEGICC(f)
	case n >= 12 && string(head[:4]) == "RIFF" && string(head[8:12]) == "WEBP":
		if _, err = f.Seek(12, io.SeekStart); err != nil {
			return nil, ErrCorrupt
		}
		return readWebPICC(f)
	default:
		return nil, nil
	}
}

func readPNGICC(f *os.File) ([]byte, error) {
	for {
		var hdr [8]byte
		if _, err := io.ReadFull(f, hdr[:]); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return nil, nil
			}
			return nil, ErrCorrupt
		}
		size := binary.BigEndian.Uint32(hdr[:4])
		kind := string(hdr[4:])
		data := make([]byte, size)
		if _, err := io.ReadFull(f, data); err != nil {
			return nil, ErrCorrupt
		}
		crc := make([]byte, 4)
		if _, err := io.ReadFull(f, crc); err != nil {
			return nil, ErrCorrupt
		}
		if kind == "iCCP" {
			zero := bytes.IndexByte(data, 0)
			if zero < 0 || zero+2 > len(data) || data[zero+1] != 0 {
				return nil, fmt.Errorf("%w: icc profile", ErrProcess)
			}
			zr, err := zlib.NewReader(bytes.NewReader(data[zero+2:]))
			if err != nil {
				return nil, fmt.Errorf("%w: icc profile", ErrProcess)
			}
			out, err := io.ReadAll(zr)
			_ = zr.Close()
			if err != nil || len(out) < 128 {
				return nil, fmt.Errorf("%w: icc profile", ErrProcess)
			}
			return out, nil
		}
		if kind == "IEND" {
			return nil, nil
		}
	}
}

func readJPEGICC(f *os.File) ([]byte, error) {
	parts := map[byte][]byte{}
	var total byte
	for {
		var marker [2]byte
		if _, err := io.ReadFull(f, marker[:]); err != nil {
			return nil, nil
		}
		if marker[0] != 0xff {
			return joinICC(parts, total)
		}
		for marker[1] == 0xff {
			if _, err := io.ReadFull(f, marker[1:]); err != nil {
				return joinICC(parts, total)
			}
		}
		if marker[1] == 0xda || marker[1] == 0xd9 || (marker[1] >= 0xd0 && marker[1] <= 0xd8) {
			return joinICC(parts, total)
		}
		var ln [2]byte
		if _, err := io.ReadFull(f, ln[:]); err != nil {
			return nil, ErrCorrupt
		}
		n := int(binary.BigEndian.Uint16(ln[:])) - 2
		if n < 0 {
			return nil, ErrCorrupt
		}
		data := make([]byte, n)
		if _, err := io.ReadFull(f, data); err != nil {
			return nil, ErrCorrupt
		}
		if marker[1] != 0xe2 || !bytes.HasPrefix(data, []byte("ICC_PROFILE\x00")) || len(data) < 14 {
			continue
		}
		seq, count := data[12], data[13]
		if count == 0 || seq == 0 || seq > count {
			return nil, fmt.Errorf("%w: icc profile", ErrProcess)
		}
		if total == 0 {
			total = count
		} else if total != count {
			return nil, fmt.Errorf("%w: icc profile", ErrProcess)
		}
		parts[seq] = append([]byte(nil), data[14:]...)
	}
}

func joinICC(parts map[byte][]byte, total byte) ([]byte, error) {
	if total == 0 {
		return nil, nil
	}
	var out []byte
	for i := byte(1); i <= total; i++ {
		part, ok := parts[i]
		if !ok {
			return nil, fmt.Errorf("%w: icc profile", ErrProcess)
		}
		out = append(out, part...)
	}
	if len(out) < 128 {
		return nil, fmt.Errorf("%w: icc profile", ErrProcess)
	}
	return out, nil
}

func readWebPICC(f *os.File) ([]byte, error) {
	for {
		var hdr [8]byte
		if _, err := io.ReadFull(f, hdr[:]); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return nil, nil
			}
			return nil, ErrCorrupt
		}
		kind := string(hdr[:4])
		size := binary.LittleEndian.Uint32(hdr[4:])
		data := make([]byte, size)
		if _, err := io.ReadFull(f, data); err != nil {
			return nil, ErrCorrupt
		}
		if size%2 == 1 {
			if _, err := f.Seek(1, io.SeekCurrent); err != nil {
				return nil, ErrCorrupt
			}
		}
		if kind == "ICCP" {
			if len(data) < 128 {
				return nil, fmt.Errorf("%w: icc profile", ErrProcess)
			}
			return data, nil
		}
	}
}
