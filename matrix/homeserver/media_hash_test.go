package homeserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/png"
	"io"
	"testing"

	"github.com/kasuganosora/community/db"
	"github.com/stretchr/testify/require"
)

func TestAsyncImageHashIsTheStoredOutput(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&AsyncMediaInfo{}, &MatrixMedia{}))
	t.Chdir(t.TempDir())
	service := NewAsyncMediaService(&HomeServer{Config: &HSConfig{ServerName: "local.test"}})
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 8, 8))))
	payload := buf.Bytes()
	pending, err := service.CreateAsyncUpload("@alice:local.test", "p.png", "image/png", int64(len(payload)))
	require.NoError(t, err)
	_, err = service.CompleteAsyncUpload(context.Background(), pending.MediaID, "@alice:local.test", bytes.NewReader(payload), "image/png")
	require.NoError(t, err)
	_, reader, err := NewMediaService(service.hs).GetMedia("local.test", pending.MediaID)
	require.NoError(t, err)
	got, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.True(t, bytes.HasPrefix(got, []byte("RIFF")))
	var stored AsyncMediaInfo
	require.NoError(t, db.GetDB().Where("media_id = ?", pending.MediaID).First(&stored).Error)
	storedSum := sha256.Sum256(got)
	originalSum := sha256.Sum256(payload)
	require.Equal(t, hex.EncodeToString(storedSum[:]), stored.Sha256Hash)
	require.NotEqual(t, hex.EncodeToString(originalSum[:]), stored.Sha256Hash)
}
