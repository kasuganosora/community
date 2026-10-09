package media

import (
	"context"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net/textproto"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/h2non/filetype"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/media"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"github.com/pkg/errors"
)

// storedMedia describes a file written by storeMediaFile.
type storedMedia struct {
	FileName      string
	FilePath      string // key below models.MediaKeyPrefix, e.g. "2026/10/09/<file>"
	Size          int64
	ThumbnailPath *string // thumbnail file name, stored next to the original
	Blurhash      string
	Width, Height *int
}

func (s *storedMedia) keys() []string {
	if s == nil || s.FilePath == "" {
		return nil
	}

	keys := []string{models.MediaKeyPrefix + s.FilePath}
	if s.ThumbnailPath != nil && *s.ThumbnailPath != "" {
		keys = append(keys, path.Join(path.Dir(keys[0]), *s.ThumbnailPath))
	}

	return keys
}

// cleanup deletes the stored objects (used when the DB write fails).
func (s *storedMedia) cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, key := range s.keys() {
		if err := storage.Default().Delete(ctx, key); err != nil {
			log.Warn("Failed to cleanup stored media %s: %v", key, err)
		}
	}
}

// storeMediaFile is the single upload pipeline shared by /api/v1/media,
// /api/v2/media and profile image uploads. The bytes are staged in a private
// temp directory (thumbnail and BlurHash generation need a file), then the
// original and its thumbnail are written through the configured storage
// backend (local uploads/ or S3) and the staging files are removed, so S3
// mode keeps no local copy.
func storeMediaFile(ctx context.Context, src io.Reader, contentType string) (*storedMedia, error) {
	now := time.Now()
	dateDir := now.Format("2006/01/02")
	fileName := generateUniqueFileName(now, extensionForContentType(contentType))

	tmpDir, err := os.MkdirTemp("", "community-media-*")
	if err != nil {
		return nil, errors.Wrap(err, "create staging directory")
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	tmpPath := filepath.Join(tmpDir, fileName)

	dst, err := os.Create(tmpPath) //nolint:gosec // G304: private temp dir
	if err != nil {
		return nil, errors.Wrap(err, "create staging file")
	}

	written, copyErr := io.Copy(dst, src)
	closeErr := dst.Close()

	if copyErr != nil {
		return nil, errors.Wrap(copyErr, "save file")
	}

	if closeErr != nil {
		return nil, errors.Wrap(closeErr, "close file")
	}

	stored := &storedMedia{FileName: fileName, FilePath: path.Join(dateDir, fileName), Size: written}

	var thumbName string

	if models.GetMediaType(contentType) == models.MediaTypeImage {
		name, bh, w, h, procErr := media.NewImageProcessor().ProcessImage(tmpPath)
		if procErr == nil {
			stored.Blurhash = bh
			stored.Width, stored.Height = &w, &h
			thumbName = name
		} else {
			log.Warn("Failed to process image: %v", procErr)
		}
	}

	key := models.MediaKeyPrefix + stored.FilePath
	if err := storage.PutFile(ctx, tmpPath, key, contentType); err != nil {
		return nil, errors.Wrap(err, "store original")
	}

	if thumbName != "" {
		thumbKey := path.Join(path.Dir(key), thumbName)
		if err := storage.PutFile(ctx, filepath.Join(tmpDir, thumbName), thumbKey, ""); err != nil {
			// The original is usable without a preview (preview_url falls
			// back to url), so a thumbnail failure does not fail the upload.
			log.Warn("Failed to store thumbnail %s: %v", thumbKey, err)
		} else {
			stored.ThumbnailPath = &thumbName
		}
	}

	return stored, nil
}

// StoreAccountImage stores an avatar or header image uploaded through the
// legacy multipart profile endpoint using the same pipeline as
// /api/v1/media, and returns the attachment URL to put in accounts.avatar /
// accounts.header (like images chosen via update_credentials).
func StoreAccountImage(ctx context.Context, accountID uint64, fh *multipart.FileHeader) (string, error) {
	// Browsers often send a generic part type; trust only the magic bytes.
	f, err := fh.Open()
	if err != nil {
		return "", errors.Wrap(err, "open image")
	}

	head := make([]byte, 8192)
	n, _ := io.ReadFull(f, head)
	_ = f.Close()

	kind, _ := filetype.Match(head[:n])
	detected := normalizeDetectedMIME(kind.MIME.Value)

	if models.GetMediaType(detected) != models.MediaTypeImage {
		return "", errors.New("unsupported image type")
	}

	typed := *fh
	typed.Header = textproto.MIMEHeader{}
	maps.Copy(typed.Header, fh.Header)
	typed.Header.Set("Content-Type", detected)

	contentType, err := validateFileUpload(&typed)
	if err != nil {
		return "", err
	}

	src, err := fh.Open()
	if err != nil {
		return "", errors.Wrap(err, "open image")
	}
	defer func() { _ = src.Close() }()

	stored, err := storeMediaFile(ctx, src, contentType)
	if err != nil {
		return "", err
	}

	record := &models.MediaAttachment{
		AccountId:     accountID,
		FileName:      stored.FileName,
		FilePath:      stored.FilePath,
		FileSize:      stored.Size,
		ContentType:   contentType,
		Type:          models.MediaTypeImage,
		Width:         stored.Width,
		Height:        stored.Height,
		ThumbnailPath: stored.ThumbnailPath,
		Blurhash:      stored.Blurhash,
	}
	if err := db.GetDB().WithContext(ctx).Create(record).Error; err != nil {
		stored.cleanup()

		return "", errors.Wrap(err, "create media record")
	}

	return fmt.Sprintf("%s/media/%d/%s", config.C.AppBaseURL, record.Id, url.PathEscape(record.FileName)), nil
}
