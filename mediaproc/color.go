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

const (
	// maxICCBytes caps both the compressed ICC chunk and the decompressed
	// profile. The check happens before either buffer is allocated. Pixel
	// dimensions do not raise this budget.
	maxICCBytes = 1 << 20
	// maxMetadataScan caps the non-pixel metadata walked while looking for a
	// profile. IDAT and WebP frame payloads are skipped, not accumulated.
	maxMetadataScan = 32 << 20
	maxPNGChunks    = 4096
	maxWebPChunks   = 4096
	maxJPEGICCParts = 32
	maxAnimFrames   = 256
	maxImageEdge    = 16384
	maxImagePixels  = 32_000_000
	// maxDecodedImageBytes caps the oriented RGBA buffer in this process.
	// Frame count and dimensions are applied before the read, so a small
	// canvas cannot justify an unbounded stream.
	maxDecodedImageBytes = 32 << 20
	maxICCTags           = 100
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
	// LittleCMS is only handed a buffer that already fits maxICCBytes, whose
	// header size and tag directory fit that buffer, and whose tag count is at
	// most maxICCTags. Its own readers then cap CLUT tables to the profile
	// size and curve entry counts to a uint16 (at most 0x7FFF). A profile that
	// fails this check is rejected, not stripped.
	if err = acceptICC(raw); err != nil {
		return "", err
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
	frame, budget, err := imageFrameBudget(cfg.Width, cfg.Height)
	if err != nil {
		return 0, 0, nil, err
	}
	rawCmd, err := command(ctx, "ffmpeg", append(safeIOArgs(), "-vsync", "0", "-i", path, "-f", "rawvideo", "-pix_fmt", "rgba", "pipe:1"))
	if err != nil {
		return 0, 0, nil, ErrProcess
	}
	stdout, err := rawCmd.StdoutPipe()
	if err != nil {
		return 0, 0, nil, ErrProcess
	}
	rawCmd.Stderr = io.Discard
	if err = rawCmd.Start(); err != nil {
		return 0, 0, nil, ErrProcess
	}
	pix, rerr := readFrameBytes(stdout, budget)
	if rerr != nil || len(pix)%frame != 0 || len(pix)/frame == 0 || len(pix)/frame > maxAnimFrames {
		if rawCmd.Process != nil {
			_ = rawCmd.Process.Kill()
		}
		_ = rawCmd.Wait()
		return 0, 0, nil, ErrProcess
	}
	if err = rawCmd.Wait(); err != nil {
		return 0, 0, nil, ErrProcess
	}
	return cfg.Width, cfg.Height, pix, nil
}

func imageFrameBudget(w, h int) (frame, budget int, err error) {
	if w < 1 || h < 1 || w > maxImageEdge || h > maxImageEdge || int64(w)*int64(h) > maxImagePixels {
		return 0, 0, ErrProcess
	}
	frame = w * h * 4
	if frame <= 0 || frame > maxDecodedImageBytes {
		return 0, 0, ErrProcess
	}
	frames := maxDecodedImageBytes / frame
	if frames > maxAnimFrames {
		frames = maxAnimFrames
	}
	if frames < 1 {
		return 0, 0, ErrProcess
	}
	return frame, frame * frames, nil
}

// readFrameBytes reads at most budget bytes. One extra byte is enough to
// reject a longer stream, and the extra byte is not retained.
func readFrameBytes(r io.Reader, budget int) ([]byte, error) {
	if budget < 1 {
		return nil, ErrProcess
	}
	buf := make([]byte, budget+1)
	n, err := io.ReadFull(r, buf)
	if n > budget {
		return nil, ErrProcess
	}
	if err == io.ErrUnexpectedEOF || err == io.EOF {
		if n == 0 {
			return nil, ErrProcess
		}
		return buf[:n], nil
	}
	if err != nil {
		return nil, ErrProcess
	}
	return nil, ErrProcess
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
		if n < 0 || off+8+n+4 > len(raw) {
			return nil, nil, ErrProcess
		}
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
	chunks := 0
	var meta int64
	for {
		chunks++
		if chunks > maxPNGChunks {
			return nil, fmt.Errorf("%w: icc profile", ErrProcess)
		}
		left, err := fileLeft(f)
		if err != nil || left < 8 {
			return nil, nil
		}
		var hdr [8]byte
		if _, err = io.ReadFull(f, hdr[:]); err != nil {
			return nil, ErrCorrupt
		}
		size := int64(binary.BigEndian.Uint32(hdr[:4]))
		kind := string(hdr[4:])
		left -= 8
		if size < 0 || size+4 > left {
			return nil, ErrCorrupt
		}
		if kind == "IDAT" || kind == "fdAT" {
			if err = skipFile(f, size+4); err != nil {
				return nil, ErrCorrupt
			}
			continue
		}
		if meta+size > maxMetadataScan {
			return nil, fmt.Errorf("%w: icc profile", ErrProcess)
		}
		meta += size
		if kind != "iCCP" {
			if kind == "IEND" {
				return nil, nil
			}
			if err = skipFile(f, size+4); err != nil {
				return nil, ErrCorrupt
			}
			continue
		}
		if size > maxICCBytes {
			return nil, fmt.Errorf("%w: icc profile", ErrProcess)
		}
		data := make([]byte, size)
		if _, err = io.ReadFull(f, data); err != nil {
			return nil, ErrCorrupt
		}
		if _, err = io.ReadFull(f, make([]byte, 4)); err != nil {
			return nil, ErrCorrupt
		}
		return inflateICC(data)
	}
}

func inflateICC(data []byte) ([]byte, error) {
	zero := bytes.IndexByte(data, 0)
	if zero < 0 || zero+2 > len(data) || data[zero+1] != 0 {
		return nil, fmt.Errorf("%w: icc profile", ErrProcess)
	}
	zr, err := zlib.NewReader(bytes.NewReader(data[zero+2:]))
	if err != nil {
		return nil, fmt.Errorf("%w: icc profile", ErrProcess)
	}
	defer func() { _ = zr.Close() }()
	out, err := readFrameBytes(zr, maxICCBytes)
	if err != nil || len(out) < 128 {
		return nil, fmt.Errorf("%w: icc profile", ErrProcess)
	}
	if err = acceptICC(out); err != nil {
		return nil, err
	}
	return out, nil
}

func readJPEGICC(f *os.File) ([]byte, error) {
	parts := map[byte][]byte{}
	var total byte
	var got int
	var acc int
	for {
		left, err := fileLeft(f)
		if err != nil || left < 2 {
			return joinICC(parts, total)
		}
		var marker [2]byte
		if _, err = io.ReadFull(f, marker[:]); err != nil {
			return nil, ErrCorrupt
		}
		if marker[0] != 0xff {
			return joinICC(parts, total)
		}
		for marker[1] == 0xff {
			if _, err = io.ReadFull(f, marker[1:]); err != nil {
				return joinICC(parts, total)
			}
		}
		if marker[1] == 0xda || marker[1] == 0xd9 || (marker[1] >= 0xd0 && marker[1] <= 0xd8) {
			return joinICC(parts, total)
		}
		left, err = fileLeft(f)
		if err != nil || left < 2 {
			return nil, ErrCorrupt
		}
		var ln [2]byte
		if _, err = io.ReadFull(f, ln[:]); err != nil {
			return nil, ErrCorrupt
		}
		n := int(binary.BigEndian.Uint16(ln[:])) - 2
		left -= 2
		if n < 0 || int64(n) > left {
			return nil, ErrCorrupt
		}
		if marker[1] != 0xe2 {
			if err = skipFile(f, int64(n)); err != nil {
				return nil, ErrCorrupt
			}
			continue
		}
		head := make([]byte, 14)
		if n < len(head) {
			if err = skipFile(f, int64(n)); err != nil {
				return nil, ErrCorrupt
			}
			continue
		}
		if _, err = io.ReadFull(f, head); err != nil {
			return nil, ErrCorrupt
		}
		rest := n - len(head)
		if !bytes.Equal(head[:12], []byte("ICC_PROFILE\x00")) {
			if err = skipFile(f, int64(rest)); err != nil {
				return nil, ErrCorrupt
			}
			continue
		}
		seq, count := head[12], head[13]
		if count == 0 || seq == 0 || seq > count || int(count) > maxJPEGICCParts {
			return nil, fmt.Errorf("%w: icc profile", ErrProcess)
		}
		if total == 0 {
			total = count
		} else if total != count {
			return nil, fmt.Errorf("%w: icc profile", ErrProcess)
		}
		if _, ok := parts[seq]; ok || got >= maxJPEGICCParts || acc > maxICCBytes-rest {
			return nil, fmt.Errorf("%w: icc profile", ErrProcess)
		}
		body := make([]byte, rest)
		if _, err = io.ReadFull(f, body); err != nil {
			return nil, ErrCorrupt
		}
		parts[seq] = body
		got++
		acc += rest
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
		if acc := len(out) + len(part); acc > maxICCBytes {
			return nil, fmt.Errorf("%w: icc profile", ErrProcess)
		}
		out = append(out, part...)
	}
	if len(out) < 128 || len(out) > maxICCBytes {
		return nil, fmt.Errorf("%w: icc profile", ErrProcess)
	}
	if err := acceptICC(out); err != nil {
		return nil, err
	}
	return out, nil
}

func readWebPICC(f *os.File) ([]byte, error) {
	chunks := 0
	var meta int64
	for {
		chunks++
		if chunks > maxWebPChunks {
			return nil, fmt.Errorf("%w: icc profile", ErrProcess)
		}
		left, err := fileLeft(f)
		if err != nil || left < 8 {
			return nil, nil
		}
		var hdr [8]byte
		if _, err = io.ReadFull(f, hdr[:]); err != nil {
			return nil, ErrCorrupt
		}
		kind := string(hdr[:4])
		size := int64(binary.LittleEndian.Uint32(hdr[4:]))
		pad := size & 1
		left -= 8
		if size < 0 || size+pad > left {
			return nil, ErrCorrupt
		}
		if kind != "ICCP" {
			if err = skipFile(f, size+pad); err != nil {
				return nil, ErrCorrupt
			}
			continue
		}
		if meta+size > maxMetadataScan || size > maxICCBytes || size < 128 {
			return nil, fmt.Errorf("%w: icc profile", ErrProcess)
		}
		meta += size
		data := make([]byte, size)
		if _, err = io.ReadFull(f, data); err != nil {
			return nil, ErrCorrupt
		}
		if pad == 1 {
			if err = skipFile(f, 1); err != nil {
				return nil, ErrCorrupt
			}
		}
		if err = acceptICC(data); err != nil {
			return nil, err
		}
		return data, nil
	}
}

func fileLeft(f *os.File) (int64, error) {
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	off, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	if info.Size() < off {
		return 0, ErrCorrupt
	}
	return info.Size() - off, nil
}

func skipFile(f *os.File, n int64) error {
	if n < 0 {
		return ErrCorrupt
	}
	_, err := f.Seek(n, io.SeekCurrent)
	return err
}

// acceptICC rejects a profile whose declared header size, tag count, or tag
// spans do not fit the bytes actually in hand. LittleCMS is not called.
func acceptICC(raw []byte) error {
	if len(raw) < 132 || len(raw) > maxICCBytes {
		return fmt.Errorf("%w: icc profile", ErrProcess)
	}
	size := binary.BigEndian.Uint32(raw[:4])
	if size < 132 || int(size) > len(raw) {
		return fmt.Errorf("%w: icc profile", ErrProcess)
	}
	if binary.BigEndian.Uint32(raw[36:40]) != 0x61637370 {
		return fmt.Errorf("%w: icc profile", ErrProcess)
	}
	tags := binary.BigEndian.Uint32(raw[128:132])
	if tags > maxICCTags {
		return fmt.Errorf("%w: icc profile", ErrProcess)
	}
	dir := 132 + int(tags)*12
	if dir > int(size) {
		return fmt.Errorf("%w: icc profile", ErrProcess)
	}
	for i := 0; i < int(tags); i++ {
		off := 132 + i*12
		tagOff := binary.BigEndian.Uint32(raw[off+4 : off+8])
		tagSize := binary.BigEndian.Uint32(raw[off+8 : off+12])
		if tagOff == 0 || tagSize == 0 {
			continue
		}
		if uint64(tagOff)+uint64(tagSize) > uint64(size) || uint64(tagOff)+uint64(tagSize) < uint64(tagOff) {
			return fmt.Errorf("%w: icc profile", ErrProcess)
		}
		// Multi-process elements carry a uint16 segment count that LittleCMS
		// allocates before it knows the bytes are present. Reject the profile
		// instead of opening it. Parametric and lut RGB profiles do not use
		// this tag.
		if tagSize >= 4 && int(tagOff)+4 <= len(raw) && binary.BigEndian.Uint32(raw[tagOff:tagOff+4]) == 0x6D706574 {
			return fmt.Errorf("%w: icc profile", ErrProcess)
		}
	}
	return nil
}
