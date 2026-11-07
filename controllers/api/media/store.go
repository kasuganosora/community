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
	"strings"
	"time"

	"github.com/h2non/filetype"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/media"
	"github.com/kasuganosora/community/mediaproc"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/pkg/errors"
)

// storedMedia describes a file written by storeMediaFile.
type storedMedia struct {
	FileName      string
	FilePath      string // key below models.MediaKeyPrefix, e.g. "2026/10/09/<file>"
	Size          int64
	ThumbnailPath *string // thumbnail file name, stored next to the original
	Blurhash      string
	ContentType   string
	Duration      *float64
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

// storeMediaFile validates and, when the saved policy says so, re-encodes a
// local image or audio upload before anything is public. Failure deletes the
// staging directory and returns an error; the original is not stored.
// Video is accepted by enqueueLocal instead.
func storeMediaFile(ctx context.Context, src io.Reader, contentType string) (*storedMedia, error) {
	dir, staged, _, err := stagePrivate(src, contentType)
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	cfg := systemsettings.LoadMediaProcessing()
	limits := mediaproc.DefaultLimits(systemsettings.MediaImageSizeLimit(), systemsettings.MediaVideoSizeLimit())
	out, err := mediaproc.ProcessFile(ctx, staged, dir, contentType, cfg, limits)
	if err != nil {
		return nil, err
	}
	return publishProcessed(ctx, dir, out)
}

func publishProcessed(ctx context.Context, dir string, out mediaproc.Output) (*storedMedia, error) {
	now := time.Now()
	dateDir := now.Format("2006/01/02")
	fileName := generateUniqueFileName(now, extensionForContentType(out.MIME))
	stored := &storedMedia{FileName: fileName, FilePath: path.Join(dateDir, fileName), Size: out.OutputBytes, ContentType: out.MIME}
	if out.Info.Duration > 0 {
		d := out.Info.Duration
		stored.Duration = &d
	}
	key := models.MediaKeyPrefix + stored.FilePath
	if err := storage.PutFile(ctx, out.Path, key, out.MIME); err != nil {
		return nil, errors.Wrap(err, "store original")
	}
	name, blur, w, h, perr := previewLocal(ctx, out.Path, out.MIME, dir)
	if perr != nil {
		_ = storage.Default().Delete(ctx, key)
		return nil, perr
	}
	if w > 0 {
		stored.Width, stored.Height = &w, &h
	}
	stored.Blurhash = blur
	if name != "" {
		thumbKey := path.Join(path.Dir(key), name)
		if err := storage.PutFile(ctx, filepath.Join(filepath.Dir(out.Path), name), thumbKey, "image/jpeg"); err != nil {
			_ = storage.Default().Delete(ctx, key)
			return nil, errors.Wrap(err, "store thumbnail")
		}
		stored.ThumbnailPath = &name
	}
	return stored, nil
}

func previewLocal(ctx context.Context, pathName, mime, work string) (string, string, int, int, error) {
	if models.GetMediaType(mime) == models.MediaTypeAudio {
		return "", "", 0, 0, nil
	}
	source := pathName
	if models.GetMediaType(mime) == models.MediaTypeVideo || strings.HasSuffix(strings.ToLower(pathName), ".webp") {
		frame := filepath.Join(work, "frame.jpg")
		if err := mediaproc.ExtractFrame(ctx, pathName, frame); err != nil {
			if models.GetMediaType(mime) == models.MediaTypeVideo {
				return "", "", 0, 0, err
			}
		} else {
			source = frame
		}
	}
	name, blur, w, h, err := media.NewImageProcessor().ProcessImage(source)
	if err != nil || name == "" {
		return "", "", 0, 0, errors.New("media processing failed")
	}
	return name, blur, w, h, nil
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
		ContentType:   stored.ContentType,
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
