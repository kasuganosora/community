package timelines

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/controllers/api/common"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"gorm.io/gorm"
	"strconv"
)

// maxNotificationScanRows bounds how many raw notification rows one page may
// inspect after serializer filtering. Tests lower it.
var maxNotificationScanRows = 1000

type NotificationTimelineRequest struct {
	ExcludeTypes []string `form:"exclude_types[]"`
	Types        []string `form:"types[]"`
	MaxId        uint64   `form:"max_id"`
	MinId        uint64   `form:"min_id"`
	SinceId      uint64   `form:"since_id"`
	Limit        int      `form:"limit"`
	AccountId    string   `form:"account_id"`
	Expand       string   `form:"expand_accounts"`
}

func NotificationTimeline(c *gin.Context) {
	notificationTimeline(c, false)
}

func GroupedNotificationTimeline(c *gin.Context) {
	notificationTimeline(c, true)
}

func notificationTimeline(c *gin.Context, grouped bool) {
	req := NotificationTimelineRequest{}
	err := c.ShouldBindQuery(&req)
	controllers.AbortIf(c, err != nil, 422, gin.H{"error": err}) //nolint:goconst
	if grouped && c.Request.URL.Query().Has("expand_accounts") && req.Expand != "full" && req.Expand != "partial_avatars" {
		c.JSON(400, gin.H{"error": "expand_accounts must be full or partial_avatars"})
		return
	}
	partialAvatars := req.Expand == "partial_avatars"

	account := controllers.MustGetAccount(c)

	query := models.NotificationInbox(db.GetDB().WithContext(c.Request.Context()), account.Id)
	query = models.FilterNotificationTypes(query, req.Types, req.ExcludeTypes)

	if req.AccountId != "" {
		filterAccountId, err := strconv.ParseUint(req.AccountId, 10, 64)
		controllers.AbortIf(c, err != nil, 422, gin.H{"error": "account_id is invalid"})

		query = query.Where("from_account_id = ?", filterAccountId)
	}

	if req.MaxId > 0 {
		query = query.Where("id < ?", req.MaxId)
	}

	if req.MinId > 0 {
		query = query.Where("id > ?", req.MinId)
	}

	if req.SinceId > 0 {
		query = query.Where("id > ?", req.SinceId)
	}

	if req.Limit > 100 {
		req.Limit = 100
	}

	if req.Limit <= 0 {
		req.Limit = 20
		if grouped {
			req.Limit = 40
		}
	}

	// Authorization can hide an entire batch after a block or recipient change.
	// Keep scanning older rows (or immediately newer rows when paging upward with
	// min_id) until the visible page is full, history ends, or the scan bound
	// is reached. The bound stops one filtered account from reading the whole
	// notification table. A capped empty page ends this response; older rows
	// remain addressable by an explicit max_id/min_id from the client.
	maxNotificationRows := maxNotificationScanRows
	forwardMin := req.MinId > 0 && req.MaxId == 0
	order := "id DESC"
	if forwardMin {
		order = "id ASC"
	}
	ret := make([]*serializer.Notification, 0, req.Limit)
	var cursor uint64
	scanned := 0
	exhausted := false
	for len(ret) < req.Limit && scanned < maxNotificationRows {
		batchQuery := query.Session(&gorm.Session{})
		if cursor != 0 {
			if forwardMin {
				batchQuery = batchQuery.Where("id > ?", cursor)
			} else {
				batchQuery = batchQuery.Where("id < ?", cursor)
			}
		}
		batchSize := req.Limit
		if batchSize < 20 {
			batchSize = 20
		}
		if remain := maxNotificationRows - scanned; remain < batchSize {
			batchSize = remain
		}
		nfs := make(models.Notifications, 0, batchSize)
		err = batchQuery.Order(order).Limit(batchSize).Find(&nfs).Error
		controllers.AbortIfError(c, err)
		if len(nfs) == 0 {
			exhausted = true
			break
		}
		visible, err := serializer.ToNotifications(nfs, account)
		controllers.AbortIfError(c, err)
		byID := make(map[uint64]*serializer.Notification, len(visible))
		for _, item := range visible {
			id, parseErr := strconv.ParseUint(item.Id, 10, 64)
			if parseErr == nil {
				byID[id] = item
			}
		}
		filled := false
		for _, row := range nfs {
			if scanned >= maxNotificationRows {
				break
			}
			scanned++
			cursor = row.Id
			if item := byID[row.Id]; item != nil && len(ret) < req.Limit {
				ret = append(ret, item)
				if len(ret) == req.Limit {
					filled = true
					break
				}
			}
		}
		if !filled && len(nfs) < batchSize {
			exhausted = true
			break
		}
		if filled {
			break
		}
	}
	if forwardMin && len(ret) > 1 {
		for i, j := 0, len(ret)-1; i < j; i, j = i+1, j-1 {
			ret[i], ret[j] = ret[j], ret[i]
		}
	}
	// Continue from the last raw row inspected, including a window that was
	// entirely filtered. Using only the last visible id repeats that window.
	// A non-empty page keeps Mastodon's next link even at the end of history.
	if cursor != 0 && (len(ret) > 0 || !exhausted) {
		if forwardMin {
			common.SetAscPageLinks(c, cursor, true)
		} else {
			first := cursor
			if len(ret) > 0 {
				if id, parseErr := strconv.ParseUint(ret[0].Id, 10, 64); parseErr == nil {
					first = id
				}
			}
			common.SetPageLinksUint(c, first, cursor, true)
		}
	}
	writeNotifications(c, ret, grouped, partialAvatars)
}

func writeNotifications(c *gin.Context, notifications []*serializer.Notification, grouped, partialAvatars bool) {
	if grouped {
		c.JSON(200, serializer.ToNotificationGroups(notifications, partialAvatars))
		return
	}
	c.JSON(200, notifications)
}
