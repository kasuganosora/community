package mediaproc

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPNGICCInflationIsBounded(t *testing.T) {
	var zbuf bytes.Buffer
	zw, err := zlib.NewWriterLevel(&zbuf, zlib.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write(bytes.Repeat([]byte{0}, 16<<20)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if zbuf.Len() > 64<<10 {
		t.Fatalf("fixture compressed to %d bytes, want a small bomb", zbuf.Len())
	}
	payload := append([]byte("icc\x00\x00"), zbuf.Bytes()...)
	file := writeTemp(t, append(append(pngSignature(), budgetChunk("IHDR", pngIHDR(8, 8))...), budgetChunk("iCCP", payload)...))
	_, err = readICC(file)
	if err == nil {
		t.Fatal("oversized inflated profile was accepted")
	}
	alloc := allocDuring(func() { _, _ = readICC(file) })
	if alloc > 2<<20 {
		t.Fatalf("icc inflation allocated %d bytes", alloc)
	}
}

func TestPNGDeclaredChunkIsNotAllocated(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(pngSignature())
	buf.Write(budgetChunk("IHDR", pngIHDR(8, 8)))
	var hdr [8]byte
	binary.BigEndian.PutUint32(hdr[:4], 32<<20)
	copy(hdr[4:], "iCCP")
	buf.Write(hdr[:])
	buf.Write(bytes.Repeat([]byte{0}, 16))
	file := writeTemp(t, buf.Bytes())
	alloc := allocDuring(func() { _, _ = readICC(file) })
	if alloc > 1<<20 {
		t.Fatalf("declared chunk allocated %d bytes", alloc)
	}
	if _, err := readICC(file); err == nil {
		t.Fatal("truncated declared chunk was accepted")
	}
}

func TestJPEGICCAccumulationIsBounded(t *testing.T) {
	var buf bytes.Buffer
	buf.Write([]byte{0xff, 0xd8})
	for i := 1; i <= 24; i++ {
		body := append([]byte("ICC_PROFILE\x00"), byte(i), 24)
		body = append(body, bytes.Repeat([]byte{byte(i)}, 60_000)...)
		buf.Write([]byte{0xff, 0xe2, byte((len(body) + 2) >> 8), byte(len(body) + 2)})
		buf.Write(body)
	}
	buf.Write([]byte{0xff, 0xd9})
	file := writeTemp(t, buf.Bytes())
	if _, err := readICC(file); err == nil {
		t.Fatal("oversized segmented jpeg profile was accepted")
	}
	alloc := allocDuring(func() { _, _ = readICC(file) })
	if alloc > 2<<20 {
		t.Fatalf("jpeg icc allocated %d bytes", alloc)
	}
}

func TestWebPDeclaredICCIsNotAllocated(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("RIFF")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(64))
	buf.WriteString("WEBP")
	buf.WriteString("ICCP")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(32<<20))
	buf.Write(bytes.Repeat([]byte{0}, 16))
	file := writeTemp(t, buf.Bytes())
	alloc := allocDuring(func() { _, _ = readICC(file) })
	if alloc > 1<<20 {
		t.Fatalf("webp icc allocated %d bytes", alloc)
	}
	if _, err := readICC(file); err == nil {
		t.Fatal("truncated webp icc was accepted")
	}
}

func TestJPEGICCPartCountIsBounded(t *testing.T) {
	const parts = 40
	var buf bytes.Buffer
	buf.Write([]byte{0xff, 0xd8})
	for i := 1; i <= parts; i++ {
		body := append([]byte("ICC_PROFILE\x00"), byte(i), byte(parts))
		body = append(body, bytes.Repeat([]byte{0x20}, 200)...)
		buf.Write([]byte{0xff, 0xe2, byte((len(body) + 2) >> 8), byte(len(body) + 2)})
		buf.Write(body)
	}
	buf.Write([]byte{0xff, 0xd9})
	file := writeTemp(t, buf.Bytes())
	if _, err := readICC(file); err == nil {
		t.Fatal("too many jpeg icc parts were accepted")
	}
	alloc := allocDuring(func() { _, _ = readICC(file) })
	if alloc > 1<<20 {
		t.Fatalf("jpeg icc part count allocated %d bytes", alloc)
	}
}

func TestPNGChunkCountIsBounded(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(pngSignature())
	buf.Write(budgetChunk("IHDR", pngIHDR(8, 8)))
	for i := 0; i < maxPNGChunks+1; i++ {
		buf.Write(budgetChunk("tEXt", []byte("a\x00b")))
	}
	file := writeTemp(t, buf.Bytes())
	alloc := allocDuring(func() { _, _ = readICC(file) })
	if alloc > 1<<20 {
		t.Fatalf("png chunk walk allocated %d bytes", alloc)
	}
	if _, err := readICC(file); err == nil {
		t.Fatal("too many png chunks were accepted")
	}
}

func TestProfileDeclaredSizeIsRejectedBeforeOpen(t *testing.T) {
	raw := make([]byte, 132)
	binary.BigEndian.PutUint32(raw[0:], 32<<20)
	binary.BigEndian.PutUint32(raw[36:], 0x61637370)
	binary.BigEndian.PutUint32(raw[128:], 1)
	alloc := allocDuring(func() { _ = acceptICC(raw) })
	if alloc > 1<<20 {
		t.Fatalf("declared profile size allocated %d bytes", alloc)
	}
	if err := acceptICC(raw); err == nil {
		t.Fatal("declared profile size was accepted")
	}
	raw[0], raw[1], raw[2], raw[3] = 0, 0, 0, 132
	binary.BigEndian.PutUint32(raw[128:], 101)
	if err := acceptICC(raw); err == nil {
		t.Fatal("tag count above the library cap was accepted")
	}
	mpet := bytes.Repeat([]byte{0}, 152)
	binary.BigEndian.PutUint32(mpet[0:], uint32(len(mpet)))
	binary.BigEndian.PutUint32(mpet[36:], 0x61637370)
	binary.BigEndian.PutUint32(mpet[128:], 1)
	binary.BigEndian.PutUint32(mpet[136:], 144)
	binary.BigEndian.PutUint32(mpet[140:], 8)
	copy(mpet[144:], []byte{'m', 'p', 'e', 't'})
	if err := acceptICC(mpet); err == nil {
		t.Fatal("multi-process element profile was accepted")
	}
}

func TestFrameReadStopsAtBudget(t *testing.T) {
	frame, budget, err := imageFrameBudget(8, 8)
	if err != nil || frame != 8*8*4 || budget != frame*maxAnimFrames {
		t.Fatalf("frame=%d budget=%d err=%v", frame, budget, err)
	}
	if _, _, err = imageFrameBudget(maxImageEdge+1, 8); err == nil {
		t.Fatal("edge above the frame budget was accepted")
	}
	src := bytes.NewReader(bytes.Repeat([]byte{1}, budget+4096))
	alloc := allocDuring(func() {
		_, err = readFrameBytes(src, budget)
	})
	if err == nil {
		t.Fatal("frame stream past the budget was accepted")
	}
	if alloc > uint64(budget)+1<<20 {
		t.Fatalf("frame read allocated %d bytes", alloc)
	}
}

func allocDuring(fn func()) uint64 {
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

func writeTemp(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bomb.bin")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func pngSignature() []byte { return []byte{137, 80, 78, 71, 13, 10, 26, 10} }

func pngIHDR(w, h int) []byte {
	data := make([]byte, 13)
	binary.BigEndian.PutUint32(data[0:], uint32(w))
	binary.BigEndian.PutUint32(data[4:], uint32(h))
	data[8] = 8
	data[9] = 6
	return data
}

func budgetChunk(kind string, data []byte) []byte {
	body := append([]byte(kind), data...)
	out := []byte{byte(len(data) >> 24), byte(len(data) >> 16), byte(len(data) >> 8), byte(len(data))}
	out = append(out, body...)
	sum := make([]byte, 4)
	binary.BigEndian.PutUint32(sum, crc32.ChecksumIEEE(body))
	return append(out, sum...)
}
