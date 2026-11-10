package media

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/mediaproc"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/pkg/errors"
)

// ErrProfileImage means the caller did not name an image this account can use
// as an avatar or header. The profile row must be left unchanged.
var ErrProfileImage = errors.New("profile image is not an owned ready image")

// ProfileImagePair resolves a local media URL to the canonical original and a
// genuinely still preview. A still upload uses the processed image for both.
// An animated upload keeps that file as the original and uses its JPEG
// thumbnail for the static field. The animated file URL is never returned as
// the static URL.
func ProfileImagePair(ctx context.Context, accountID uint64, raw string) (string, string, error) {
	id, name, err := parseProfileMediaURL(raw)
	if err != nil {
		return "", "", ErrProfileImage
	}
	var att models.MediaAttachment
	if err = db.GetDB().WithContext(ctx).First(&att, id).Error; err != nil {
		return "", "", ErrProfileImage
	}
	if att.AccountId != accountID || att.FileName != name || att.Type != models.MediaTypeImage || !att.ProcessPublishable() || att.DetachedAt != nil || att.FilePath == "" {
		return "", "", ErrProfileImage
	}
	animated, err := storedImageAnimated(ctx, &att)
	if err != nil {
		return "", "", err
	}
	base := strings.TrimRight(systemsettings.GetBaseURL(), "/")
	primary := fmt.Sprintf("%s/media/%d/%s", base, att.Id, att.FileName)
	if !animated {
		return primary, primary, nil
	}
	thumb := att.ThumbPath()
	if thumb == "" || thumb == att.FileName || strings.Contains(thumb, "/") || strings.Contains(thumb, "\\") {
		return "", "", ErrProfileImage
	}
	if err = storedObjectExists(ctx, att.ThumbnailStorageKey()); err != nil {
		return "", "", ErrProfileImage
	}
	static := fmt.Sprintf("%s/media/%d/thumbnail/%s", base, att.Id, thumb)
	if static == primary {
		return "", "", ErrProfileImage
	}
	return primary, static, nil
}

func parseProfileMediaURL(raw string) (uint64, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2048 || strings.ContainsRune(raw, 0) {
		return 0, "", ErrProfileImage
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil {
		return 0, "", ErrProfileImage
	}
	cleaned := path.Clean("/" + strings.TrimPrefix(parsed.Path, "/"))
	if !strings.HasPrefix(cleaned, "/media/") {
		return 0, "", ErrProfileImage
	}
	parts := strings.Split(strings.TrimPrefix(cleaned, "/media/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || parts[1] == "." || parts[1] == ".." {
		return 0, "", ErrProfileImage
	}
	id, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil || id == 0 {
		return 0, "", ErrProfileImage
	}
	name, err := url.PathUnescape(parts[1])
	if err != nil || name == "" || strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.ContainsRune(name, 0) {
		return 0, "", ErrProfileImage
	}
	return id, name, nil
}

func storedImageAnimated(ctx context.Context, att *models.MediaAttachment) (bool, error) {
	probePath, cleanup, err := probePath(ctx, att.StorageKey())
	if err != nil {
		return false, err
	}
	defer cleanup()
	probe, err := mediaproc.ProbeFile(ctx, probePath)
	if err != nil {
		return false, ErrProfileImage
	}
	return probe.Animated, nil
}

func storedObjectExists(ctx context.Context, key string) error {
	if key == "" {
		return ErrProfileImage
	}
	rc, _, err := storage.Open(ctx, key)
	if err != nil {
		return ErrProfileImage
	}
	defer func() { _ = rc.Close() }()
	var buf [1]byte
	if _, err = rc.Read(buf[:]); err != nil {
		return ErrProfileImage
	}
	return nil
}

func probePath(ctx context.Context, key string) (string, func(), error) {
	if key == "" {
		return "", nil, ErrProfileImage
	}
	if local, ok := storage.Default().(*storage.LocalBackend); ok {
		filePath, err := local.Path(key)
		if err != nil {
			return "", nil, ErrProfileImage
		}
		return filePath, func() {}, nil
	}
	rc, info, err := storage.Open(ctx, key)
	if err != nil {
		return "", nil, ErrProfileImage
	}
	defer func() { _ = rc.Close() }()
	limit := systemsettings.MediaImageSizeLimit()
	if info.Size > limit && limit > 0 {
		return "", nil, ErrProfileImage
	}
	tmp, err := os.CreateTemp("", "profile-image-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.Remove(tmp.Name()) }
	written, copyErr := io.Copy(tmp, io.LimitReader(rc, limit+1))
	closeErr := tmp.Close()
	if copyErr != nil || closeErr != nil || (limit > 0 && written > limit) {
		cleanup()
		return "", nil, ErrProfileImage
	}
	return tmp.Name(), cleanup, nil
}
