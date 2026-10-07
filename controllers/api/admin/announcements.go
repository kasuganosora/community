package admin

import (
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type announcementInput struct {
	Content     string     `json:"content"`
	AllDay      bool       `json:"all_day"`
	ScheduledAt *time.Time `json:"scheduled_at"`
	StartsAt    *time.Time `json:"starts_at"`
	EndsAt      *time.Time `json:"ends_at"`
}

func adminAnnouncement(a models.Announcement) gin.H {
	return gin.H{"id": strconv.FormatUint(a.Id, 10), "content": a.Text, "published": a.Published, "all_day": a.AllDay, "scheduled_at": a.ScheduledAt, "starts_at": a.StartsAt, "ends_at": a.EndsAt, "published_at": a.PublishedAt, "created_at": a.CreatedAt, "updated_at": a.UpdatedAt}
}
func announcementID(c *gin.Context) (uint64, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.JSON(422, gin.H{"error": "Invalid announcement ID"})
		return 0, false
	}
	return id, true
}
func announcementError(c *gin.Context, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(404, gin.H{"error": "Announcement not found"})
	} else {
		c.JSON(500, gin.H{"error": "Unable to save announcement"})
	}
}
func ListAdminAnnouncements(c *gin.Context) {
	q := db.GetDB().WithContext(c.Request.Context()).Model(&models.Announcement{})
	if p := c.Query("published"); p != "" {
		if p != "true" && p != "false" {
			c.JSON(422, gin.H{"error": "Invalid published filter"})
			return
		}
		q = q.Where("published = ?", p == "true")
	}
	if cursor := c.Query("max_id"); cursor != "" {
		id, err := strconv.ParseUint(cursor, 10, 64)
		if err != nil {
			c.JSON(422, gin.H{"error": "Invalid cursor"})
			return
		}
		q = q.Where("id < ?", id)
	}
	var rows []models.Announcement
	if err := q.Order("id DESC").Limit(40).Find(&rows).Error; err != nil {
		announcementError(c, err)
		return
	}
	result := make([]gin.H, 0, len(rows))
	for _, a := range rows {
		result = append(result, adminAnnouncement(a))
	}
	c.JSON(200, result)
}
func GetAdminAnnouncement(c *gin.Context) {
	id, ok := announcementID(c)
	if !ok {
		return
	}
	var a models.Announcement
	if err := db.GetDB().WithContext(c.Request.Context()).First(&a, id).Error; err != nil {
		announcementError(c, err)
		return
	}
	c.JSON(200, adminAnnouncement(a))
}
func SaveAdminAnnouncement(c *gin.Context) {
	var input announcementInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(422, gin.H{"error": "Invalid announcement; times must be RFC3339 or null"})
		return
	}
	input.Content = strings.TrimSpace(input.Content)
	if input.Content == "" || utf8.RuneCountInString(input.Content) > 10000 {
		c.JSON(422, gin.H{"error": "Content must contain 1–10000 characters"})
		return
	}
	if input.StartsAt != nil && input.EndsAt != nil && !input.EndsAt.After(*input.StartsAt) {
		c.JSON(422, gin.H{"error": "End must be after start"})
		return
	}
	if input.ScheduledAt != nil && input.EndsAt != nil && !input.EndsAt.After(*input.ScheduledAt) {
		c.JSON(422, gin.H{"error": "End must be after scheduled publication"})
		return
	}
	var a models.Announcement
	conn := db.GetDB().WithContext(c.Request.Context())
	if c.Param("id") != "" {
		id, ok := announcementID(c)
		if !ok {
			return
		}
		if err := conn.First(&a, id).Error; err != nil {
			announcementError(c, err)
			return
		}
	}
	a.Text = input.Content
	a.AllDay = input.AllDay
	a.ScheduledAt = input.ScheduledAt
	a.StartsAt = input.StartsAt
	a.EndsAt = input.EndsAt
	var saveErr error
	if a.Id == 0 {
		saveErr = conn.Create(&a).Error
	} else {
		saveErr = conn.Model(&a).Updates(map[string]any{"text": a.Text, "all_day": a.AllDay, "scheduled_at": a.ScheduledAt, "starts_at": a.StartsAt, "ends_at": a.EndsAt}).Error
	}
	if saveErr != nil {
		announcementError(c, saveErr)
		return
	}
	c.JSON(200, adminAnnouncement(a))
}
func PublishAdminAnnouncement(c *gin.Context)   { setAnnouncementPublished(c, true) }
func UnpublishAdminAnnouncement(c *gin.Context) { setAnnouncementPublished(c, false) }
func setAnnouncementPublished(c *gin.Context, published bool) {
	id, ok := announcementID(c)
	if !ok {
		return
	}
	conn := db.GetDB().WithContext(c.Request.Context())
	var a models.Announcement
	if err := conn.First(&a, id).Error; err != nil {
		announcementError(c, err)
		return
	}
	if published {
		a.Publish()
	} else {
		a.UnPublish()
	}
	if err := conn.Model(&a).Updates(map[string]any{"published": a.Published, "published_at": a.PublishedAt}).Error; err != nil {
		announcementError(c, err)
		return
	}
	c.JSON(200, adminAnnouncement(a))
}
func DeleteAdminAnnouncement(c *gin.Context) {
	id, ok := announcementID(c)
	if !ok {
		return
	}
	err := db.GetDB().WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		var a models.Announcement
		if err := tx.First(&a, id).Error; err != nil {
			return err
		}
		if err := tx.Where("announcement_id = ?", id).Delete(&models.AnnouncementDismissal{}).Error; err != nil {
			return err
		}
		return tx.Delete(&a).Error
	})
	if err != nil {
		announcementError(c, err)
		return
	}
	c.JSON(200, gin.H{})
}
