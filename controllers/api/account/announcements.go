package account

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"strconv"
)

// Announcement represents an announcement.
type Announcement struct {
	Id          uint64     `gorm:"primary_key;auto_increment" json:"id"`
	Text        string     `gorm:"type:text;not null"         json:"text"`
	Published   bool       `gorm:"not null;default:false"     json:"published"`
	AllDay      bool       `gorm:"not null;default:false"     json:"all_day"`
	ScheduledAt *time.Time `json:"scheduled_at"`
	StartsAt    *time.Time `json:"starts_at"`
	EndsAt      *time.Time `json:"ends_at"`
	PublishedAt *time.Time `json:"published_at"`
	UpdatedAt   *time.Time `gorm:"not null;autoUpdateTime"    json:"updated_at"`
	CreatedAt   *time.Time `gorm:"not null;autoCreateTime"    json:"created_at"`
}

func (a *Announcement) TableName() string {
	return "announcements"
}

// ListAnnouncements handles GET /api/v1/announcements.
func ListAnnouncements(c *gin.Context) {
	// Get announcements
	query := db.GetDB().Table("announcements").
		Where("published = ?", true).
		Where("scheduled_at IS NULL OR scheduled_at <= ?", time.Now()).
		Order("created_at DESC")

	announcements := make([]Announcement, 0)
	err := query.Find(&announcements).Error
	if err != nil {
		// If table doesn't exist, return empty array
		c.JSON(200, []any{})

		return
	}

	// Build response
	result := make([]gin.H, 0, len(announcements))

	for _, a := range announcements {
		var scheduledAt, startsAt, endsAt, publishedAt any
		if a.ScheduledAt != nil {
			scheduledAt = a.ScheduledAt.Format(time.RFC3339)
		}

		if a.StartsAt != nil {
			startsAt = a.StartsAt.Format(time.RFC3339)
		}

		if a.EndsAt != nil {
			endsAt = a.EndsAt.Format(time.RFC3339)
		}

		if a.PublishedAt != nil {
			publishedAt = a.PublishedAt.Format(time.RFC3339)
		}

		result = append(result, gin.H{
			"id":           strconv.FormatUint(a.Id, 10),
			"content":      a.Text,
			"published":    a.Published,
			"all_day":      a.AllDay,
			"scheduled_at": scheduledAt,
			"starts_at":    startsAt,
			"ends_at":      endsAt,
			"published_at": publishedAt,
			"updated_at":   a.UpdatedAt.Format(time.RFC3339),
			"read":         false,
			"mentions":     []any{},
			"statuses":     []any{},
			"tags":         []any{},
			"emojis":       []any{},
			"reactions":    []any{},
		})
	}

	c.JSON(200, result)
}

// DismissAnnouncement handles POST /api/v1/announcements/:id/dismiss.
func DismissAnnouncement(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.AbortWithStatusJSON(400, gin.H{"error": "announcement id required"}) //nolint:goconst

		return
	}

	// In a full implementation, we'd store dismissed announcements per user
	// For simplicity, just return success
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
	c.JSON(200, gin.H{})
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
	c.JSON(200, gin.H{})
}
