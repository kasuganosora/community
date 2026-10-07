package account

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"html"
	"strconv"
	"time"
)

func activeAnnouncements(conn *gorm.DB, now time.Time) *gorm.DB {
	return conn.Model(&models.Announcement{}).Where("published = ?", true).
		Where("scheduled_at IS NULL OR scheduled_at <= ?", now).
		Where("starts_at IS NULL OR starts_at <= ?", now).
		Where("ends_at IS NULL OR ends_at > ?", now)
}
func ListAnnouncements(c *gin.Context) {
	conn := db.GetDB().WithContext(c.Request.Context())
	now := time.Now()
	announcements := make([]models.Announcement, 0)
	if err := activeAnnouncements(conn, now).Order("id DESC").Find(&announcements).Error; err != nil {
		c.JSON(500, gin.H{"error": "Unable to load announcements"})
		return
	}
	reads := map[uint64]time.Time{}
	if value, ok := c.Get("account"); ok {
		if account, ok := value.(*models.Account); ok && account != nil {
			var rows []models.AnnouncementDismissal
			if err := conn.Where("account_id = ?", account.Id).Find(&rows).Error; err != nil {
				c.JSON(500, gin.H{"error": "Unable to load announcement read state"})
				return
			}
			for _, r := range rows {
				reads[r.AnnouncementID] = r.ReadAt
			}
		}
	}
	result := make([]gin.H, 0, len(announcements))
	for _, a := range announcements {
		readAt, seen := reads[a.Id]
		read := seen && (a.UpdatedAt == nil || !readAt.Before(*a.UpdatedAt))
		if c.Query("with_dismissed") == "false" && read {
			continue
		}
		result = append(result, gin.H{"id": strconv.FormatUint(a.Id, 10), "content": html.EscapeString(a.Text), "text": a.Text, "published": a.Published, "all_day": a.AllDay, "scheduled_at": a.ScheduledAt, "starts_at": a.StartsAt, "ends_at": a.EndsAt, "published_at": a.PublishedAt, "updated_at": a.UpdatedAt, "read": read, "mentions": []any{}, "statuses": []any{}, "tags": []any{}, "emojis": []any{}, "reactions": []any{}})
	}
	c.JSON(200, result)
}
func DismissAnnouncement(c *gin.Context) {
	value, ok := c.Get("account")
	account, valid := value.(*models.Account)
	if !ok || !valid || account == nil {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.JSON(422, gin.H{"error": "Invalid announcement ID"})
		return
	}
	conn := db.GetDB().WithContext(c.Request.Context())
	var a models.Announcement
	if err := activeAnnouncements(conn, time.Now()).First(&a, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(404, gin.H{"error": "Announcement not found"})
		} else {
			c.JSON(500, gin.H{"error": "Unable to load announcement"})
		}
		return
	}
	readAt := time.Now()
	if a.UpdatedAt != nil {
		readAt = *a.UpdatedAt
	}
	read := models.AnnouncementDismissal{AccountID: account.Id, AnnouncementID: id, ReadAt: readAt}
	if err := conn.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "account_id"}, {Name: "announcement_id"}}, DoUpdates: clause.AssignmentColumns([]string{"read_at"})}).Create(&read).Error; err != nil {
		c.JSON(500, gin.H{"error": "Unable to mark announcement read"})
		return
	}
	c.JSON(200, gin.H{})
}

// AddAnnouncementReaction handles PUT /api/v1/announcements/:id/reactions/:name.
func AddAnnouncementReaction(c *gin.Context) {
	id := c.Param("id")

	name := c.Param("name")

	if id == "" || name == "" {
		c.AbortWithStatusJSON(400, gin.H{"error": "announcement id and reaction name required"})

		return
	}

	// In a full implementation, we'd store reactions per user
	// For simplicity, just return success
	c.JSON(501, gin.H{"error": "Announcement reactions are not supported"})
}

// RemoveAnnouncementReaction handles DELETE /api/v1/announcements/:id/reactions/:name.
func RemoveAnnouncementReaction(c *gin.Context) {
	id := c.Param("id")

	name := c.Param("name")

	if id == "" || name == "" {
		c.AbortWithStatusJSON(400, gin.H{"error": "announcement id and reaction name required"})

		return
	}

	// In a full implementation, we'd remove reactions per user
	// For simplicity, just return success
	c.JSON(501, gin.H{"error": "Announcement reactions are not supported"})
}
