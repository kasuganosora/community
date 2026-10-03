package transfer

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	data "github.com/kasuganosora/community/datatransfer"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/htmlsafe"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

type archivedMedia struct {
	SourceID    string `json:"source_id"`
	ID          string `json:"id,omitempty"`
	Available   bool   `json:"available"`
	Name        string `json:"name,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Size        int64  `json:"size,omitempty"`
	Description string `json:"description,omitempty"`
}
type archivedPost struct {
	ID             string          `json:"id"`
	SourceID       string          `json:"source_id"`
	Text           string          `json:"text"`
	HTML           string          `json:"html"`
	Spoiler        string          `json:"spoiler"`
	CreatedAt      time.Time       `json:"created_at"`
	SourceURI      string          `json:"source_uri"`
	AuthorID       string          `json:"author_id"`
	PrivateArchive bool            `json:"private_archive"`
	Media          []archivedMedia `json:"media"`
}

func archiveCursor(c *gin.Context, name string) (uint64, bool) {
	value := c.Query(name)
	if value == "" {
		return 0, true
	}
	n, e := strconv.ParseUint(value, 10, 64)
	if e != nil || n == 0 {
		c.JSON(422, gin.H{"error": "invalid archive cursor"})
		return 0, false
	}
	return n, true
}
func archiveDTO(conn *gorm.DB, row *data.StoredPreservedRecord, owner uint64) (*archivedPost, error) {
	var p data.Post
	if e := json.Unmarshal(row.Data, &p); e != nil || p.ID == "" || p.AuthorID == "" {
		return nil, nil
	}
	safeURI := ""
	if u, e := url.Parse(p.URI); e == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil {
		safeURI = u.String()
	}
	dto := &archivedPost{ID: strconv.FormatUint(row.ID, 10), SourceID: row.SourceID, Text: p.Text, HTML: htmlsafe.Sanitize(p.HTML), Spoiler: htmlsafe.StripTags(p.SpoilerText), CreatedAt: p.CreatedAt, SourceURI: safeURI, AuthorID: p.AuthorID, PrivateArchive: true, Media: []archivedMedia{}}
	// Receipts establish namespace identity; ownership independently prevents a
	// corrupt or foreign receipt from exposing another account's private upload.
	seen := map[string]bool{}
	for _, id := range p.MediaIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		m := archivedMedia{SourceID: id}
		var receipt data.ImportReceipt
		e := conn.Where("namespace = ? AND kind = ? AND source_id = ? AND archived = ?", row.Namespace, "media", id, false).First(&receipt).Error
		if e != nil && e != gorm.ErrRecordNotFound {
			return nil, e
		}
		if e == nil && receipt.TargetID > 0 {
			var target models.MediaAttachment
			e = conn.Where("id = ? AND account_id = ?", receipt.TargetID, owner).First(&target).Error
			if e != nil && e != gorm.ErrRecordNotFound {
				return nil, e
			}
			if e == nil {
				m.ID = strconv.FormatUint(target.Id, 10)
				m.Available = target.FilePath != ""
				m.Name = target.FileName
				m.ContentType = target.ContentType
				m.Size = target.FileSize
				m.Description = target.Description
			}
		}
		dto.Media = append(dto.Media, m)
	}
	return dto, nil
}

// ArchivedPosts is an authenticated private reading view, never a timeline or
// federation endpoint. Source actor IDs are labels, not local author identities.
func ArchivedPosts(c *gin.Context) {
	u, a, ok := principal(c, "personal")
	if !ok {
		return
	}
	limit := 20
	if value := c.Query("limit"); value != "" {
		n, e := strconv.Atoi(value)
		if e != nil || n < 1 || n > 50 {
			c.JSON(422, gin.H{"error": "limit must be between 1 and 50"})
			return
		}
		limit = n
	}
	maxID, ok := archiveCursor(c, "max_id")
	if !ok {
		return
	}
	sinceID, ok := archiveCursor(c, "since_id")
	if !ok {
		return
	}
	conn := db.GetDB().WithContext(c.Request.Context())
	q := conn.Where("owner_user_id = ? AND owner_account_id = ? AND scope = ? AND kind = ?", u.Id, a.Id, "personal", "post")
	if maxID > 0 {
		q = q.Where("id < ?", maxID)
	}
	if sinceID > 0 {
		q = q.Where("id > ?", sinceID)
	}
	var rows []data.StoredPreservedRecord
	if e := q.Order("id DESC").Limit(limit + 1).Find(&rows).Error; e != nil {
		c.JSON(500, gin.H{"error": "could not read private archive"})
		return
	}
	var next any
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	items := []*archivedPost{}
	for i := range rows {
		dto, e := archiveDTO(conn, &rows[i], a.Id)
		if e != nil {
			c.JSON(500, gin.H{"error": "could not read archived media"})
			return
		}
		if dto != nil {
			items = append(items, dto)
		}
	}
	if hasMore && len(rows) > 0 {
		next = strconv.FormatUint(rows[len(rows)-1].ID, 10)
	}
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.JSON(200, gin.H{"items": items, "next_max_id": next})
}
func ArchivedPost(c *gin.Context) {
	u, a, ok := principal(c, "personal")
	if !ok {
		return
	}
	id, e := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if e != nil || id == 0 {
		c.JSON(404, gin.H{"error": "archived post not found"})
		return
	}
	conn := db.GetDB().WithContext(c.Request.Context())
	var row data.StoredPreservedRecord
	if e = conn.Where("id = ? AND owner_user_id = ? AND owner_account_id = ? AND scope = ? AND kind = ?", id, u.Id, a.Id, "personal", "post").First(&row).Error; e != nil {
		if e == gorm.ErrRecordNotFound {
			c.JSON(404, gin.H{"error": "archived post not found"})
		} else {
			c.JSON(500, gin.H{"error": "could not read private archive"})
		}
		return
	}
	dto, e := archiveDTO(conn, &row, a.Id)
	if e != nil {
		c.JSON(500, gin.H{"error": "could not read archived media"})
		return
	}
	if dto == nil {
		c.JSON(404, gin.H{"error": "archived post not found"})
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.JSON(200, dto)
}
