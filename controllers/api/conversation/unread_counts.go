package conversation

import (
	"strconv"
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

// unreadNotificationScanLimit bounds raw rows inspected while hidden
// notifications are discarded. It matches one notifications page so a long
// run of blocked or incomplete rows cannot read the whole inbox.
var unreadNotificationScanLimit = 1000

// SetUnreadNotificationScanLimitForTest lowers the raw-row ceiling. The
// returned function restores the previous ceiling.
func SetUnreadNotificationScanLimitForTest(limit int) func() {
	previous := unreadNotificationScanLimit
	unreadNotificationScanLimit = limit
	return func() { unreadNotificationScanLimit = previous }
}

// UnreadCounts handles GET /api/v1/unread_counts (an extension, not Mastodon):
// the three nav badges in one request. The web app calls it when the window
// regains focus or its stream reconnects; there is no polling.
func UnreadCounts(c *gin.Context) {
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert
	conn := db.GetDB().WithContext(c.Request.Context())

	notifications, err := unreadNotifications(conn, account, UnreadCap, nil, nil)
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

func UnreadNotificationCount(c *gin.Context) {
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert
	limit := 100
	if raw, ok := c.Request.URL.Query()["limit"]; ok {
		requested, _ := strconv.Atoi(raw[0])
		limit = min(max(requested, -1000), 1000)
		if limit < 0 {
			limit = -limit
		}
	}
	count, err := unreadNotifications(db.GetDB().WithContext(c.Request.Context()), account, limit,
		c.QueryArray("types[]"), c.QueryArray("exclude_types[]"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(200, gin.H{"count": count})
}

// unreadNotifications counts visible notifications newer than the
// notifications marker, exactly like the notifications list would show them.
func unreadNotifications(conn *gorm.DB, account *models.Account, limit int, include, exclude []string) (int, error) {
	var lastRead uint64
	marker := &models.Marker{}
	if err := conn.Where("account_id = ?", account.Id).Limit(1).Find(marker).Error; err != nil {
		return 0, err
	}
	if item := marker.Timelines["notifications"]; item != nil {
		lastRead = item.LastReadId
	}
	base := models.NotificationInbox(conn, account.Id).Where("id > ?", lastRead)
	base = models.FilterNotificationTypes(base, include, exclude)
	var count int
	var cursor uint64
	scanned := 0
	for count < limit && scanned < unreadNotificationScanLimit {
		batchSize := min(100, max(20, limit-count))
		if remain := unreadNotificationScanLimit - scanned; remain < batchSize {
			batchSize = remain
		}
		if batchSize <= 0 {
			break
		}
		query := base.Session(&gorm.Session{})
		if cursor != 0 {
			query = query.Where("id < ?", cursor)
		}
		var rows models.Notifications
		if err := query.Order("id DESC").Limit(batchSize).Find(&rows).Error; err != nil {
			return 0, err
		}
		if len(rows) == 0 {
			break
		}
		visible, err := serializer.ToNotifications(rows, account)
		if err != nil {
			return 0, err
		}
		count += len(visible)
		scanned += len(rows)
		cursor = rows[len(rows)-1].Id
		if len(rows) < batchSize {
			break
		}
	}
	return min(count, limit), nil
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
