package media

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/mediaproc"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
	"gorm.io/gorm"
)

// stagePrivate copies an upload into uploads/.private-staging. That directory
// is not served by /media and is never given a public object URL.
func stagePrivate(src io.Reader, contentType string) (string, string, int64, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", "", 0, err
	}
	dir := filepath.Join("uploads", ".private-staging", hex.EncodeToString(id[:]))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", 0, errors.New("temporary storage is full")
	}
	limit := systemsettings.MediaVideoSizeLimit()
	if models.GetMediaType(contentType) == models.MediaTypeImage {
		limit = systemsettings.MediaImageSizeLimit()
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(dir, &stat); err != nil {
		_ = os.RemoveAll(dir)
		return "", "", 0, errors.New("temporary storage is full")
	}
	avail := int64(stat.Bavail) * int64(stat.Bsize)
	need := limit*3 + 64<<20
	if need < 256<<20 {
		need = 256 << 20
	}
	if avail < need {
		_ = os.RemoveAll(dir)
		return "", "", 0, errors.New("temporary storage is full")
	}
	staged := filepath.Join(dir, "input"+extensionForContentType(contentType))
	dst, err := os.OpenFile(staged, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", "", 0, errors.New("temporary storage is full")
	}
	written, copyErr := io.Copy(dst, io.LimitReader(src, limit+1))
	closeErr := dst.Close()
	if copyErr != nil || closeErr != nil || written > limit {
		_ = os.RemoveAll(dir)
		if written > limit {
			return "", "", 0, errors.New("file size too large")
		}
		return "", "", 0, errors.New("temporary storage is full")
	}
	return dir, staged, written, nil
}

func kickProcessing() {
	select {
	case asyncUploadSem <- struct{}{}:
		go func() {
			defer func() { <-asyncUploadSem }()
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
			defer cancel()
			_ = mediaproc.ProcessNext(ctx, db.GetDB())
		}()
	default:
	}
}

// acceptAsync stages any local upload as a durable task and returns 202.
// syncWait is used by POST /api/v1/media for video so Mastodon v1 clients
// still receive the finished attachment.
func acceptAsync(c *gin.Context, accountID uint64, src io.Reader, contentType, description string, focusX, focusY float64, syncWait bool) {
	if syncWait {
		select {
		case asyncUploadSem <- struct{}{}:
			<-asyncUploadSem
		default:
			c.JSON(503, gin.H{"error": "Server is busy processing uploads, please retry later"})
			return
		}
	}
	dir, staged, written, err := stagePrivate(src, contentType)
	if err != nil {
		writeMediaError(c, err)
		return
	}
	cfg := systemsettings.LoadMediaProcessing()
	plan := mediaproc.Decide(contentType, cfg)
	if plan.Action == mediaproc.ActionReject {
		_ = os.RemoveAll(dir)
		c.JSON(400, gin.H{"error": "unsupported media type"})
		return
	}
	outMIME := contentType
	if plan.Action == mediaproc.ActionEncode {
		outMIME = plan.OutputMIME
	}
	now := time.Now()
	name := generateUniqueFileName(now, extensionForContentType(outMIME))
	rel := path.Join(now.Format("2006/01/02"), name)
	row := &models.MediaAttachment{
		AccountId: accountID, FileName: "processing", FilePath: "", FileSize: written,
		ContentType: contentType, Type: models.MediaTypeUnknown, Description: description,
		FocusX: focusX, FocusY: focusY, ProcessState: models.ProcessQueued,
	}
	if err = db.GetDB().Create(row).Error; err != nil {
		_ = os.RemoveAll(dir)
		_ = c.Error(errors.Wrap(err, "Failed to create media record"))
		c.JSON(500, gin.H{"error": "Internal server error"})
		return
	}
	kind := "audio"
	switch models.GetMediaType(contentType) {
	case models.MediaTypeImage:
		kind = "image"
	case models.MediaTypeVideo:
		kind = "video"
	}
	if err = mediaproc.Enqueue(c.Request.Context(), db.GetDB(), row.Id, accountID, kind, contentType, staged, models.MediaKeyPrefix+rel, written); err != nil {
		_ = os.RemoveAll(dir)
		_ = db.GetDB().Delete(&models.MediaAttachment{}, row.Id)
		writeMediaError(c, err)
		return
	}
	kickProcessing()
	if !syncWait {
		c.JSON(202, gin.H{"id": strconv.FormatUint(row.Id, 10), "type": "unknown", "url": "", "preview_url": "", "description": description, "blurhash": ""})
		return
	}
	ready, err := waitReady(c.Request.Context(), row.Id)
	if err != nil {
		writeMediaError(c, err)
		return
	}
	response, err := serializer.ToMediaAttachment(ready)
	if err != nil {
		_ = c.Error(err)
		c.JSON(500, gin.H{"error": "Internal server error"})
		return
	}
	c.JSON(200, response)
}

func waitReady(ctx context.Context, id uint64) (*models.MediaAttachment, error) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		var row models.MediaAttachment
		if err := db.GetDB().WithContext(ctx).First(&row, id).Error; err != nil {
			return nil, err
		}
		switch row.ProcessState {
		case models.ProcessReady:
			return &row, nil
		case models.ProcessFailed, models.ProcessCancelled:
			if row.ProcessError != "" {
				return nil, errors.New(row.ProcessError)
			}
			return nil, mediaproc.ErrProcess
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func writeMediaError(c *gin.Context, err error) {
	if err == nil {
		return
	}
	switch {
	case err.Error() == "upload queue is full" || err.Error() == "temporary storage is full" || err.Error() == "Server is busy processing uploads, please retry later":
		c.JSON(503, gin.H{"error": err.Error()})
	case errors.Is(err, mediaproc.ErrCorrupt) || errors.Is(err, mediaproc.ErrPixels) || errors.Is(err, mediaproc.ErrDuration) || errors.Is(err, mediaproc.ErrRate) || errors.Is(err, mediaproc.ErrTooLarge) || err.Error() == "file size too large" || err.Error() == "unsupported media type":
		c.JSON(400, gin.H{"error": err.Error()})
	case errors.Is(err, mediaproc.ErrProcess) || err.Error() == mediaproc.ErrProcess.Error():
		c.JSON(422, gin.H{"error": "media processing failed"})
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(404, gin.H{"error": "Not found"})
	default:
		c.JSON(500, gin.H{"error": "Internal server error"})
	}
}
