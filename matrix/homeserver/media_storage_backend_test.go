package homeserver

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"testing"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/storage"
	"github.com/kasuganosora/community/storage/storagetest"
	"github.com/stretchr/testify/require"
)

func TestMatrixMediaUsesObjectStorage(t *testing.T) {
	for name, mk := range map[string]func(t *testing.T) storage.Backend{
		"fake": func(*testing.T) storage.Backend { return storagetest.NewMemory() },
		"s3": func(t *testing.T) storage.Backend {
			env := storagetest.S3FromEnv(t)
			return storage.NewS3Backend(env.Config(env.NewBucket(t)))
		},
	} {
		t.Run(name, func(t *testing.T) {
			setupMinimalTestDB(t)
			require.NoError(t, db.GetDB().AutoMigrate(&MatrixMedia{}, &MatrixThumbnail{}, &WebGroupUpload{}, &AsyncMediaInfo{}))
			t.Chdir(t.TempDir())
			b := mk(t)
			defer storage.SetDefault(b)()

			var buf bytes.Buffer
			require.NoError(t, png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 8, 8))))
			payload := buf.Bytes()

			svc := NewMediaService(&HomeServer{Config: &HSConfig{ServerName: "local.test"}})
			m, err := svc.UploadMedia(bytes.NewReader(payload), &multipart.FileHeader{Filename: "p.png", Size: int64(len(payload))}, "image/png", "@alice:local.test")
			require.NoError(t, err)
			require.NotContains(t, m.FilePath, "://")
			_, err = b.Stat(context.Background(), m.FilePath)
			require.NoError(t, err)

			_, rc, err := svc.GetMedia("local.test", m.MediaID)
			require.NoError(t, err)
			got, _ := io.ReadAll(rc)
			_ = rc.Close()
			require.Equal(t, payload, got)

			th, rc, err := svc.GetThumbnail("local.test", m.MediaID, 32, 32, "scale")
			require.NoError(t, err)
			_ = rc.Close()
			_, err = b.Stat(context.Background(), th.FilePath)
			require.NoError(t, err, "thumbnail stored in the bucket")

			require.NoError(t, svc.DeleteMedia("local.test", m.MediaID))
			_, err = b.Stat(context.Background(), m.FilePath)
			require.ErrorIs(t, err, storage.ErrNotFound)
			_, err = b.Stat(context.Background(), th.FilePath)
			require.ErrorIs(t, err, storage.ErrNotFound)
		})
	}
}
