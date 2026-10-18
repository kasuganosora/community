package conversation

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

// UnreadCap bounds every count: the web app only shows a dot (and a "40+"
// notification badge), so nothing past this is ever looked at.
const UnreadCap = 40

// UnreadCounts handles GET /api/v1/unread_counts (an extension, not Mastodon):
// the three nav badges in one request. The web app calls it when the window
// regains focus or its stream reconnects; there is no polling.
func UnreadCounts(c *gin.Context) {
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert
	conn := db.GetDB().WithContext(c.Request.Context())

	notifications, err := unreadNotifications(conn, account)
	if err == nil {
		var announcements, conversations int
		announcements, err = unreadAnnouncements(conn, account.Id, time.Now())
		if err == nil {
			conversations, err = unreadConversations(conn, account.Id)
			if err == nil {
				c.JSON(200, gin.H{"notifications": notifications, "announcements": announcements, "conversations": conversations})

				return
			}
		}
	}
	_ = c.Error(errors.Wrap(err, "unread counts"))
	c.AbortWithStatusJSON(500, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
}

// unreadNotifications counts visible notifications newer than the
// notifications marker, exactly like the notifications list would show them.
func unreadNotifications(conn *gorm.DB, account *models.Account) (int, error) {
	var lastRead uint64
	marker := &models.Marker{}
	if err := conn.Where("account_id = ?", account.Id).Limit(1).Find(marker).Error; err != nil {
		return 0, err
	}
	if item := marker.Timelines["notifications"]; item != nil {
		lastRead = item.LastReadId
	}
	rows := make(models.Notifications, 0, UnreadCap)
	if err := conn.Model(&models.Notification{}).Where("account_id = ? AND id > ?", account.Id, lastRead).
		Order("id DESC").Limit(UnreadCap).Find(&rows).Error; err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	visible, err := serializer.ToNotifications(rows, account)
	if err != nil {
		return 0, err
	}

	return len(visible), nil
}

// unreadAnnouncements counts active announcements not read since their last edit.
func unreadAnnouncements(conn *gorm.DB, accountID uint64, now time.Time) (int, error) {
	var ids []uint64
	err := conn.Model(&models.Announcement{}).
		Joins("LEFT JOIN announcement_dismissals d ON d.announcement_id = announcements.id AND d.account_id = ?", accountID).
		Where("announcements.published = ?", true).
		Where("announcements.scheduled_at IS NULL OR announcements.scheduled_at <= ?", now).
		Where("announcements.starts_at IS NULL OR announcements.starts_at <= ?", now).
		Where("announcements.ends_at IS NULL OR announcements.ends_at > ?", now).
		Where("d.account_id IS NULL OR (announcements.updated_at IS NOT NULL AND d.read_at < announcements.updated_at)").
		Limit(UnreadCap).Pluck("announcements.id", &ids).Error

	return len(ids), err
}

// unreadConversations counts conversations whose newest message visible to the
// account is past its read-through point (the conversation list's "unread").
func unreadConversations(conn *gorm.DB, accountID uint64) (int, error) {
	newer := visibleDirectStatuses(conn.Session(&gorm.Session{NewDB: true}), accountID).Select("1").
		Where("statuses.conversation_id = account_conversations.conversation_id AND statuses.id > account_conversations.last_status_id")
	var ids []uint64
	err := conn.Model(&models.AccountConversation{}).
		Where("account_conversations.account_id = ? AND EXISTS (?)", accountID, newer).
		Limit(UnreadCap).Pluck("account_conversations.id", &ids).Error

	return len(ids), err
}
