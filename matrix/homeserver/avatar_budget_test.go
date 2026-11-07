package homeserver

import (
	"encoding/binary"
	"hash/crc32"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDecodeAvatarImageRejectsHugeCanvasBeforeDecode(t *testing.T) {
	_, _, err := decodeAvatarImage(pngCanvas(100000, 100000))
	require.EqualError(t, err, "unsupported avatar image")
}

func pngCanvas(w, h int) []byte {
	data := make([]byte, 13)
	binary.BigEndian.PutUint32(data[0:], uint32(w))
	binary.BigEndian.PutUint32(data[4:], uint32(h))
	data[8] = 8
	data[9] = 2
	return append(append([]byte{137, 80, 78, 71, 13, 10, 26, 10}, pngChunk("IHDR", data)...), pngChunk("IEND", nil)...)
}

func pngChunk(kind string, data []byte) []byte {
	body := append([]byte(kind), data...)
	out := make([]byte, 4)
	binary.BigEndian.PutUint32(out, uint32(len(data)))
	out = append(out, body...)
	sum := make([]byte, 4)
	binary.BigEndian.PutUint32(sum, crc32.ChecksumIEEE(body))
	return append(out, sum...)
}
