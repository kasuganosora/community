package admin

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type managedFile struct {
	ID            string  `json:"id"`
	AccountID     string  `json:"account_id"`
	StatusID      *uint64 `json:"status_id"`
	Name          string  `json:"name"`
	Size          int64   `json:"size"`
	ContentType   string  `json:"content_type"`
	URL           string  `json:"url"`
	Remote        bool    `json:"remote"`
	DeleteBlocked string  `json:"delete_blocked"`
}

func managedMediaURL(m models.MediaAttachment) string {
	return "/media/" + strconv.FormatUint(m.Id, 10) + "/" + url.PathEscape(m.FileName)
}
func fileReferenceReason(tx *gorm.DB, m models.MediaAttachment) (string, error) {
	if m.StatusId != nil {
		return "文件仍被帖子引用，请先处理原帖子；不会连带删除帖子或私信。", nil
	}
	var count int64
	if err := tx.Model(&models.MediaAttachment{}).Where("file_path = ? AND id <> ?", m.FilePath, m.Id).Count(&count).Error; err != nil {
		return "", err
	}
	if count > 0 {
		return "文件与其他附件共享存储，无法单独删除。", nil
	}
	// Legacy profile uploads can point at the attachment endpoint.
	suffix := "%/media/" + strconv.FormatUint(m.Id, 10) + "/%"
	if err := tx.Model(&models.Account{}).Where("avatar LIKE ? OR header LIKE ?", suffix, suffix).Count(&count).Error; err != nil {
		return "", err
	}
	if count > 0 {
		return "文件仍用作头像或背景图。", nil
	}
	return "", nil
}
func ListManagedFiles(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	limit := 30
	query := db.GetDB().Model(&models.MediaAttachment{})
	if q := strings.TrimSpace(c.Query("q")); q != "" {
		query = query.Where("file_name LIKE ?", "%"+q+"%")
	}
	if owner := c.Query("account_id"); owner != "" {
		id, e := strconv.ParseUint(owner, 10, 64)
		if e != nil {
			c.Status(422)
			return
		}
		query = query.Where("account_id = ?", id)
	}
	if kind := c.Query("type"); kind != "" {
		query = query.Where("type = ?", kind)
	}
	if c.Query("attached") == "true" {
		query = query.Where("status_id IS NOT NULL")
	} else if c.Query("attached") == "false" {
		query = query.Where("status_id IS NULL")
	}
	var total int64
	var bytes struct{ Size int64 }
	if err := query.Count(&total).Error; err != nil {
		assetError(c, err)
		return
	}
	if err := query.Select("COALESCE(SUM(file_size),0) AS size").Scan(&bytes).Error; err != nil {
		assetError(c, err)
		return
	}
	var rows []models.MediaAttachment
	if err := query.Select("*").Order("id DESC").Offset((page - 1) * limit).Limit(limit).Find(&rows).Error; err != nil {
		assetError(c, err)
		return
	}
	out := make([]managedFile, 0, len(rows))
	for _, m := range rows {
		reason, err := fileReferenceReason(db.GetDB(), m)
		if err != nil {
			assetError(c, err)
			return
		}
		out = append(out, managedFile{ID: strconv.FormatUint(m.Id, 10), AccountID: strconv.FormatUint(m.AccountId, 10), StatusID: m.StatusId, Name: m.FileName, Size: m.FileSize, ContentType: m.ContentType, URL: managedMediaURL(m), Remote: m.RemoteUrl != "", DeleteBlocked: reason})
	}
	c.JSON(200, gin.H{"items": out, "total": total, "bytes": bytes.Size, "page": page, "limit": limit})
}

var errFileReferenced = errors.New("file still referenced")

func DeleteManagedFile(c *gin.Context) {
	id, ok := assetID(c)
	if !ok {
		return
	}
	var blocked string
	var row models.MediaAttachment
	err := db.GetDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, id).Error; err != nil {
			return err
		}
		var err error
		blocked, err = fileReferenceReason(tx, row)
		if err != nil {
			return err
		}
		if blocked != "" {
			return errFileReferenced
		}
		return tx.Delete(&row).Error
	})
	if errors.Is(err, errFileReferenced) {
		c.JSON(409, gin.H{"error": blocked})
		return
	}
	if err != nil {
		assetError(c, err)
		return
	}
	// Cleanup is restricted to the uploaded-media root and never follows a path
	// supplied in this request. Shared or attached files never reach this point.
	remove := func(rel string) {
		clean := filepath.Clean(filepath.FromSlash(rel))
		if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return
		}
		_ = os.Remove(filepath.Join("uploads", "media", clean))
	}
	remove(row.FilePath)
	if row.ThumbnailPath != nil {
		remove(filepath.Join(filepath.Dir(row.FilePath), *row.ThumbnailPath))
	}
	c.Status(204)
}
