package datatransfer

import (
	"errors"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ImportedMediaAlias preserves a formerly public source media URL. It never
// makes a private archive or a remote URL into a public redirect target.
type ImportedMediaAlias struct {
	ID                uint64 `gorm:"primaryKey"`
	URLHash           string `gorm:"size:64;not null;uniqueIndex"`
	OriginalURL       string `gorm:"type:text;not null" json:"-"`
	MediaAttachmentID uint64 `gorm:"not null;index"`
	CreatedAt         time.Time
}

func (ImportedMediaAlias) TableName() string { return "data_transfer_media_aliases" }

func mediaAliasOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") || (u.Path != "" && u.Path != "/") {
		return "", errors.New("media alias requires a canonical HTTP origin")
	}
	return strings.ToLower(u.Scheme + "://" + u.Host), nil
}

// MediaAliasURL canonicalizes only the two old public-media namespaces. It
// rejects traversal and encoded separators, even when a proxy would normalize
// them, so an alias can never claim an API, OAuth or Matrix endpoint.
func MediaAliasURL(baseURL, original string) (string, error) {
	origin, err := mediaAliasOrigin(baseURL)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(original)
	if err != nil || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(original, "#") || strings.ToLower(u.Scheme+"://"+u.Host) != origin {
		return "", errors.New("media alias must use the target origin without credentials, query or fragment")
	}
	p := u.Path
	escaped := strings.ToLower(u.EscapedPath())
	if (!strings.HasPrefix(p, "/system/") && !strings.HasPrefix(p, "/files/")) || p == "/system/" || p == "/files/" || path.Clean(p) != p || strings.Contains(p, "\\") || strings.Contains(escaped, "%2f") || strings.Contains(escaped, "%5c") || strings.IndexFunc(p, unicode.IsControl) >= 0 {
		return "", errors.New("invalid original public media path")
	}
	return origin + (&url.URL{Path: p}).EscapedPath(), nil
}

func mediaAliasTarget(media *models.MediaAttachment) (string, error) {
	name := media.FileName
	if media.Id == 0 || media.FilePath == "" || name == "" || name == "processing" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", errors.New("media alias requires a stored local media file")
	}
	return "/media/" + strconv.FormatUint(media.Id, 10) + "/" + url.PathEscape(name), nil
}

// RecordMediaAlias must run inside the importing transaction, after resolving
// the target media receipt, including when that receipt already existed.
// Only a trusted site import may install aliases; personal imports ignore the
// original URL metadata and cannot modify the instance's public namespaces.
func RecordMediaAlias(tx *gorm.DB, options Options, media Media, targetID uint64) error {
	if options.Scope != "site" || media.OriginalURL == "" {
		return nil
	}
	original, err := MediaAliasURL(options.TargetBaseURL, media.OriginalURL)
	if err != nil {
		return err
	}
	var target models.MediaAttachment
	if err = tx.First(&target, "id = ?", targetID).Error; err != nil {
		return errors.New("media alias target is unavailable")
	}
	if _, err = mediaAliasTarget(&target); err != nil {
		return err
	}
	alias := ImportedMediaAlias{URLHash: digestBytes([]byte(original)), OriginalURL: original, MediaAttachmentID: targetID}
	result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&alias)
	if result.Error != nil {
		return errors.New("could not save original media URL")
	}
	if result.RowsAffected != 0 {
		return nil
	}
	var existing ImportedMediaAlias
	if err = tx.Where("url_hash = ?", alias.URLHash).First(&existing).Error; err != nil {
		return errors.New("could not resolve original media URL")
	}
	if existing.OriginalURL != original || existing.MediaAttachmentID != targetID {
		return errors.New("original media URL already maps to different media")
	}
	return nil
}

// ResolveMediaAlias returns only a local /media path, never a source-provided
// location. Request Host and forwarded headers must not determine baseURL.
func ResolveMediaAlias(tx *gorm.DB, baseURL, requestPath string) (string, error) {
	origin, err := mediaAliasOrigin(baseURL)
	if err != nil {
		return "", err
	}
	original, err := MediaAliasURL(origin, origin+(&url.URL{Path: requestPath}).EscapedPath())
	if err != nil {
		return "", err
	}
	var alias ImportedMediaAlias
	if err = tx.Where("url_hash = ?", digestBytes([]byte(original))).First(&alias).Error; err != nil {
		return "", err
	}
	if alias.OriginalURL != original {
		return "", errors.New("media alias mismatch")
	}
	var target models.MediaAttachment
	if err = tx.First(&target, "id = ?", alias.MediaAttachmentID).Error; err != nil {
		return "", err
	}
	return mediaAliasTarget(&target)
}
