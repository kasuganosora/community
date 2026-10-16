package homeserver

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/storage"
	"gorm.io/gorm"
)

const webGroupUploadTTL = 24 * time.Hour
const webGroupImageMaxPixels = 16_000_000

// WebGroupUpload is an upload lease, not a second attachment/message database.
// Room events remain authoritative. Used leases are retained until expiry for
// safe retries; their media must never be removed by the orphan sweeper.
type WebGroupUpload struct {
	ID          string    `gorm:"primaryKey;size:64"`
	RoomID      string    `gorm:"size:255;not null;index"`
	UserID      string    `gorm:"size:255;not null;index"`
	MediaID     string    `gorm:"size:64;not null;uniqueIndex"`
	ServerName  string    `gorm:"size:255;not null"`
	UsedEventID string    `gorm:"size:255;not null;default:'';index:idx_web_upload_expiry,priority:1"`
	ExpiresAt   time.Time `gorm:"not null;index:idx_web_upload_expiry,priority:2"`
	CreatedAt   time.Time
}

func (WebGroupUpload) TableName() string { return "matrix_web_group_uploads" }

var webGroupUploadSlots = make(chan struct{}, 4)
var webGroupCleanupOnce sync.Once

func webGroupUploadMaxBytes() int64 {
	if n, err := strconv.ParseInt(os.Getenv("MATRIX_WEB_GROUP_UPLOAD_MAX_BYTES"), 10, 64); err == nil && n > 0 && n <= matrixMediaLimit {
		return n
	}
	return matrixMediaLimit
}

func registerWebGroupMediaRoutes(group *gin.RouterGroup) {
	group.POST("/groups/:id/uploads", handleWebGroupUpload)
	group.GET("/groups/:id/avatar", handleWebGroupAvatar)
	group.GET("/groups/:id/members/:memberID/avatar", handleWebGroupMemberAvatar)
	group.GET("/groups/:id/messages/:messageID/media/:mediaID", handleWebGroupMedia)
	webGroupCleanupOnce.Do(func() {
		go func() {
			// No per-user timers or workers. SQL claims make concurrent nodes safe.
			for range time.NewTicker(5 * time.Minute).C {
				hs := GetHomeServer()
				if hs == nil || db.GetDB() == nil {
					continue
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				if err := cleanupWebGroupUploads(ctx, hs, time.Now()); err != nil {
					log.Warn("web group upload cleanup failed: %v", err)
				}
				cancel()
			}
		}()
	})
}

func handleWebGroupUpload(c *gin.Context) {
	hs, room, user, ok := webGroupAccess(c, true)
	if !ok {
		return
	}
	if err := webGroupCanUpload(hs, room, user); err != nil {
		webGroupError(c, err)
		return
	}
	select {
	case webGroupUploadSlots <- struct{}{}:
		defer func() { <-webGroupUploadSlots }()
	default:
		c.Header("Retry-After", "3")
		webGroupFailure(c, 429, "UPLOAD_BUSY", "Another upload is in progress, please try again later")
		return
	}
	limit := webGroupUploadMaxBytes()
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit+(64<<10))
	mr, err := c.Request.MultipartReader()
	if err != nil {
		webGroupFailure(c, 400, "INVALID_UPLOAD", "Choose a file to upload")
		return
	}
	part, err := mr.NextPart()
	if err != nil || part.FormName() != "file" || part.FileName() == "" {
		webGroupFailure(c, 400, "INVALID_UPLOAD", "Each upload needs one \"file\" field")
		return
	}
	defer part.Close()
	staged, err := os.CreateTemp("", "community-group-upload-*")
	if err != nil {
		webGroupFailure(c, 503, "UPLOAD_UNAVAILABLE", "Uploads are temporarily unavailable")
		return
	}
	defer os.Remove(staged.Name())
	defer staged.Close()
	size, err := io.Copy(staged, io.LimitReader(part, limit+1))
	if size > limit {
		webGroupFailure(c, 413, "UPLOAD_TOO_LARGE", "The file exceeds the upload size limit")
		return
	}
	if err != nil || size == 0 || c.Request.Context().Err() != nil {
		webGroupFailure(c, 400, "UPLOAD_INCOMPLETE", "The file is empty or the upload was interrupted, please retry")
		return
	}
	if _, err = mr.NextPart(); err != io.EOF {
		webGroupFailure(c, 400, "INVALID_UPLOAD", "Each upload request may contain only one file")
		return
	}
	contentType, err := inspectWebGroupUpload(staged)
	if err != nil {
		webGroupFailure(c, 400, "INVALID_IMAGE", "Invalid image format or size over the limit (16 megapixels)")
		return
	}
	// State may have changed while bytes were arriving. Do not publish an
	// upload lease after leaving a room or learning that it is encrypted.
	hs, room, user, ok = webGroupAccess(c, true)
	if !ok {
		return
	}
	if err := webGroupCanUpload(hs, room, user); err != nil {
		webGroupError(c, err)
		return
	}
	name := webGroupFilename(part.FileName())
	media, err := NewMediaService(hs).UploadMedia(staged, &multipart.FileHeader{Filename: name, Size: size}, contentType, user)
	if err != nil {
		log.Warn("web group media storage failed: %v", err)
		webGroupFailure(c, 503, "UPLOAD_UNAVAILABLE", "Failed to store the file, please retry")
		return
	}
	upload := WebGroupUpload{ID: uuid.NewString(), RoomID: room.RoomID, UserID: user, MediaID: media.MediaID,
		ServerName: hs.Config.ServerName, ExpiresAt: time.Now().Add(webGroupUploadTTL)}
	if err := db.GetDB().WithContext(c.Request.Context()).Create(&upload).Error; err != nil {
		_ = NewMediaService(hs).DeleteMedia(upload.ServerName, upload.MediaID)
		webGroupFailure(c, 503, "UPLOAD_UNAVAILABLE", "Cannot save the upload result, please retry")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": upload.ID, "name": media.FileName, "type": media.ContentType, "size": media.FileSize})
}

func webGroupCanUpload(hs *HomeServer, room *MatrixRoom, user string) *MatrixError {
	if err := webGroupPlaintext(hs, room); err != nil {
		return err
	}
	if !hs.Event.canUserSendEvent(room.RoomID, user, EventTypeRoomMessage) && !hs.Event.canUserSendStateEvent(room.RoomID, user, EventTypeRoomAvatar) {
		return ErrForbidden
	}
	return nil
}

func webGroupFilename(value string) string {
	value = filepath.Base(strings.ReplaceAll(value, "\\", "/"))
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '\u202e' || r == '\u202d' || r == '\u2066' || r == '\u2067' || r == '\u2068' || r == '\u2069' {
			return -1
		}
		return r
	}, value)
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > 180 {
		runes = runes[:180]
	}
	value = string(runes)
	if value == "" || value == "." || value == ".." {
		return "attachment"
	}
	return value
}

// Only raster formats with bounded, inspectable dimensions can be inline
// images. SVG/HTML, audio and video stay ordinary downloadable attachments.
func webGroupRasterType(typ string) bool {
	switch strings.Split(typ, ";")[0] {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	}
	return false
}

func inspectWebGroupUpload(file *os.File) (string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	prefix := make([]byte, 512)
	n, err := file.Read(prefix)
	if err != nil && err != io.EOF {
		return "", err
	}
	typ := http.DetectContentType(prefix[:n])
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	if webGroupRasterType(typ) {
		cfg, _, err := image.DecodeConfig(file)
		if err != nil || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > webGroupImageMaxPixels {
			return "", errors.New("invalid or oversized image")
		}
	} else if strings.HasPrefix(typ, "image/") {
		typ = "application/octet-stream"
	}
	_, err = file.Seek(0, io.SeekStart)
	return typ, err
}

// Resolving a lease also extends it before any event write. The conditional
// update races safely with the orphan sweeper; expired/deleting leases fail.
func webGroupUploadContent(conn *gorm.DB, roomID, userID, uploadID string) (map[string]any, *MatrixError) {
	if uploadID == "" || len(uploadID) > 64 {
		return nil, &MatrixError{ErrCode: "M_INVALID_PARAM", Error: "Invalid attachment reference"}
	}
	var upload WebGroupUpload
	if err := conn.Where("id = ? AND room_id = ? AND user_id = ?", uploadID, roomID, userID).First(&upload).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrInternal
		}
		return nil, &MatrixError{ErrCode: "M_NOT_FOUND", Error: "The attachment does not exist or you may not use it"}
	}
	if upload.UsedEventID == "deleting" || !upload.ExpiresAt.After(time.Now()) {
		return nil, &MatrixError{ErrCode: "M_NOT_FOUND", Error: "The attachment has expired, please upload it again"}
	}
	if upload.UsedEventID == "" {
		result := conn.Model(&WebGroupUpload{}).Where("id = ? AND used_event_id = '' AND expires_at > ?", upload.ID, time.Now()).Update("expires_at", time.Now().Add(webGroupUploadTTL))
		if result.Error != nil {
			return nil, ErrInternal
		}
		if result.RowsAffected == 0 {
			return nil, &MatrixError{ErrCode: "M_NOT_FOUND", Error: "The attachment has expired, please upload it again"}
		}
	}
	var media MatrixMedia
	if err := conn.Where("media_id = ? AND server_name = ?", upload.MediaID, upload.ServerName).First(&media).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrInternal
		}
		return nil, &MatrixError{ErrCode: "M_NOT_FOUND", Error: "The attachment file is unavailable, please upload it again"}
	}
	info := map[string]any{"mimetype": media.ContentType, "size": media.FileSize}
	msgtype := "m.file"
	if webGroupRasterType(media.ContentType) && media.Width != nil && media.Height != nil {
		msgtype = "m.image"
		info["w"], info["h"] = *media.Width, *media.Height
	}
	return map[string]any{"msgtype": msgtype, "body": media.FileName, "filename": media.FileName,
		"url": "mxc://" + media.ServerName + "/" + media.MediaID, "info": info}, nil
}

func webGroupMarkUploadUsed(conn *gorm.DB, event *MatrixEvent, uploadID string) error {
	if event == nil || event.EventID == "" {
		return errors.New("missing upload event")
	}
	// Reusing one's own attachment in the same room is safe; keep the first
	// event as proof of use instead of changing it with every retry.
	result := conn.Model(&WebGroupUpload{}).Where("id = ? AND room_id = ? AND user_id = ? AND used_event_id = ''", uploadID, event.RoomID, event.Sender).Update("used_event_id", event.EventID)
	if result.Error != nil || result.RowsAffected > 0 {
		return result.Error
	}
	var upload WebGroupUpload
	if err := conn.Where("id = ? AND room_id = ? AND user_id = ? AND used_event_id <> '' AND used_event_id <> 'deleting'", uploadID, event.RoomID, event.Sender).First(&upload).Error; err != nil {
		return err
	}
	return nil
}

func cleanupWebGroupUploads(ctx context.Context, hs *HomeServer, now time.Time) error {
	conn := db.GetDB().WithContext(ctx)
	// Finished leases expire without touching media; message transaction IDs
	// own delivery idempotency. Keep this auxiliary table bounded over time.
	var finished []string
	if err := conn.Model(&WebGroupUpload{}).Where("used_event_id NOT IN ? AND expires_at <= ?", []string{"", "deleting"}, now).Limit(128).Pluck("id", &finished).Error; err != nil {
		return err
	}
	if len(finished) != 0 {
		if err := conn.Where("id IN ? AND used_event_id NOT IN ? AND expires_at <= ?", finished, []string{"", "deleting"}, now).Delete(&WebGroupUpload{}).Error; err != nil {
			return err
		}
	}
	var uploads []WebGroupUpload
	if err := conn.Where("used_event_id IN ? AND expires_at <= ?", []string{"", "deleting"}, now).Order("expires_at").Limit(32).Find(&uploads).Error; err != nil {
		return err
	}
	for _, upload := range uploads {
		claim := conn.Model(&WebGroupUpload{}).Where("id = ? AND used_event_id IN ? AND expires_at <= ?", upload.ID, []string{"", "deleting"}, now).Updates(map[string]any{"used_event_id": "deleting", "expires_at": now.Add(time.Hour)})
		if claim.Error != nil {
			return claim.Error
		}
		if claim.RowsAffected == 0 {
			continue
		}
		// Covers crash-after-event-before-mark and use from native clients.
		// A false positive is intentionally safe: it retains media.
		var count int64
		err := conn.Model(&MatrixEvent{}).Where("content LIKE ?", "%"+upload.MediaID+"%").Count(&count).Error
		if err != nil || count != 0 {
			_ = conn.Model(&WebGroupUpload{}).Where("id = ? AND used_event_id = 'deleting'", upload.ID).Update("used_event_id", "referenced").Error
			if err != nil {
				return err
			}
			continue
		}
		if err := deleteWebGroupOrphanMedia(conn, upload); err != nil {
			// Retain a retryable lease if the backing store/record is unavailable.
			_ = conn.Model(&WebGroupUpload{}).Where("id = ?", upload.ID).Updates(map[string]any{"used_event_id": "", "expires_at": now.Add(time.Hour)}).Error
			return fmt.Errorf("remove expired upload: %w", err)
		}
		if err := conn.Delete(&WebGroupUpload{}, "id = ? AND used_event_id = 'deleting'", upload.ID).Error; err != nil {
			return err
		}
	}
	return nil
}

// Keep metadata until storage deletion succeeds, so transient object storage
// failures remain retryable rather than silently orphaning the original file.
func deleteWebGroupOrphanMedia(conn *gorm.DB, upload WebGroupUpload) error {
	var media MatrixMedia
	err := conn.Where("server_name = ? AND media_id = ?", upload.ServerName, upload.MediaID).First(&media).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var thumbs []MatrixThumbnail
	if err := conn.Where("media_id = ?", upload.MediaID).Find(&thumbs).Error; err != nil {
		return err
	}
	refs := []string{media.FilePath}
	for _, thumb := range thumbs {
		if thumb.FilePath != "" && thumb.FilePath != media.FilePath {
			refs = append(refs, thumb.FilePath)
		}
	}
	for _, ref := range refs {
		if filepath.IsAbs(ref) {
			err = os.Remove(ref)
		} else {
			key, _ := storage.NormalizeKey(ref)
			err = storage.Delete(key)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, storage.ErrNotFound) {
			return err
		}
	}
	return conn.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("media_id = ?", upload.MediaID).Delete(&MatrixThumbnail{}).Error; err != nil {
			return err
		}
		return tx.Delete(&media).Error
	})
}
