package media

import (
	"bytes"
	"github.com/stretchr/testify/require"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestThumbnailOnlyPreservesOriginalAndThumbnailBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.png")
	var source bytes.Buffer
	require.NoError(t, png.Encode(&source, image.NewRGBA(image.Rect(0, 0, 256, 128))))
	require.NoError(t, os.WriteFile(path, source.Bytes(), 0600))
	p := NewImageProcessor()
	name, hash, w, h, err := p.ProcessImage(path)
	require.NoError(t, err)
	require.NotEmpty(t, hash)
	previous, err := os.ReadFile(filepath.Join(filepath.Dir(path), name))
	require.NoError(t, err)
	name2, w2, h2, err := p.ProcessImageThumbnail(path)
	require.NoError(t, err)
	require.Equal(t, name, name2)
	require.Equal(t, w, w2)
	require.Equal(t, h, h2)
	current, err := os.ReadFile(filepath.Join(filepath.Dir(path), name2))
	require.NoError(t, err)
	require.Equal(t, previous, current)
	original, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, source.Bytes(), original)
	invalid := filepath.Join(t.TempDir(), "bad.png")
	require.NoError(t, os.WriteFile(invalid, []byte("not an image"), 0600))
	_, _, _, err = p.ProcessImageThumbnail(invalid)
	require.Error(t, err)
}
