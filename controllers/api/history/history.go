package history

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	historysvc "github.com/kasuganosora/community/history"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
)

type historyItemResponse struct {
	ViewedAt time.Time          `json:"viewed_at"`
	Cursor   string             `json:"cursor"`
	Status   *serializer.Status `json:"status"`
}

type recordHistoryRequest struct {
	StatusId string `json:"status_id"`
}

type historySettingsRequest struct {
	Paused bool `json:"paused"`
}

// List handles GET /api/v1/history.
func List(c *gin.Context) {
	account := controllers.MustGetAccount(c)
	limit := 20
	if raw := c.Query("limit"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			limit = min(v, 40)
		}
	}
	cursorMs, cursorSID := historysvc.ParseCursor(c.Query("max_cursor"))
	conn := db.GetDB().WithContext(models.WithDomainSeveritiesMemo(c.Request.Context()))

	visibleStatuses := make(models.Statuses, 0, limit)
	visibleEntries := make([]historysvc.Entry, 0, limit)

	// Scan up to 3 pages in case some historical statuses were deleted or hidden,
	// lazily pruning hard-deleted status IDs from history storage.
	for range 3 {
		needed := limit - len(visibleStatuses)
		if needed <= 0 {
			break
		}
		entries, err := historysvc.ListCursor(c.Request.Context(), account.Id, cursorMs, cursorSID, max(needed, 20))
		if err != nil {
			controllers.AbortIfError(c, err)
			return
		}
		if len(entries) == 0 {
			break
		}
		last := entries[len(entries)-1]
		cursorMs, cursorSID = last.ViewedAt, last.StatusID

		statusIDs := make([]uint64, 0, len(entries))
		entryByID := make(map[uint64]historysvc.Entry, len(entries))
		for _, e := range entries {
			statusIDs = append(statusIDs, e.StatusID)
			entryByID[e.StatusID] = e
		}

		var loaded models.Statuses
		if err := models.StatusLoadAllQuery(conn.Model(&models.Status{})).
			Where("statuses.id IN ?", statusIDs).Find(&loaded).Error; err != nil {
			controllers.AbortIfError(c, err)
			return
		}
		byID := make(map[uint64]*models.Status, len(loaded))
		for _, s := range loaded {
			byID[s.Id] = s
		}
		ordered := make(models.Statuses, 0, len(statusIDs))
		for _, sid := range statusIDs {
			if s := byID[sid]; s != nil {
				ordered = append(ordered, s)
			} else {
				_ = historysvc.Remove(c.Request.Context(), account.Id, sid)
			}
		}
		if len(ordered) == 0 {
			if len(entries) < max(needed, 20) {
				break
			}
			continue
		}

		visible, err := models.FilterVisibleStatuses(conn, account, ordered)
		if err != nil {
			controllers.AbortIfError(c, err)
			return
		}
		for _, v := range visible {
			visibleStatuses = append(visibleStatuses, v)
			visibleEntries = append(visibleEntries, entryByID[v.Id])
			if len(visibleStatuses) >= limit {
				break
			}
		}
		if len(entries) < max(needed, 20) {
			break
		}
	}

	if len(visibleStatuses) == 0 {
		c.JSON(http.StatusOK, []historyItemResponse{})
		return
	}

	serialized, err := serializer.ToStatusesContext(c.Request.Context(), visibleStatuses, account)
	if err != nil {
		controllers.AbortIfError(c, err)
		return
	}

	resp := make([]historyItemResponse, 0, len(serialized))
	for i, s := range serialized {
		entry := visibleEntries[i]
		resp = append(resp, historyItemResponse{
			ViewedAt: time.UnixMilli(entry.ViewedAt).UTC(),
			Cursor:   historysvc.FormatCursor(entry),
			Status:   s,
		})
	}
	c.JSON(http.StatusOK, resp)
}

// Record handles POST /api/v1/history.
func Record(c *gin.Context) {
	account := controllers.MustGetAccount(c)
	var req recordHistoryRequest
	if err := controllers.BindBody(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	statusID, err := strconv.ParseUint(strings.TrimSpace(req.StatusId), 10, 64)
	if err != nil || statusID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid status_id"})
		return
	}
	if err := historysvc.RecordSync(c.Request.Context(), account.Id, statusID); err != nil {
		controllers.AbortIfError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// Remove handles DELETE /api/v1/history/:status_id.
func Remove(c *gin.Context) {
	account := controllers.MustGetAccount(c)
	statusID, err := strconv.ParseUint(c.Param("status_id"), 10, 64)
	if err != nil || statusID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid status_id"})
		return
	}
	if err := historysvc.Remove(c.Request.Context(), account.Id, statusID); err != nil {
		controllers.AbortIfError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{})
}

// Clear handles DELETE /api/v1/history.
func Clear(c *gin.Context) {
	account := controllers.MustGetAccount(c)
	if err := historysvc.Clear(c.Request.Context(), account.Id); err != nil {
		controllers.AbortIfError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{})
}

// GetSettings handles GET /api/v1/history/settings.
func GetSettings(c *gin.Context) {
	account := controllers.MustGetAccount(c)
	paused, err := historysvc.IsPaused(c.Request.Context(), account.Id)
	if err != nil {
		controllers.AbortIfError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"paused": paused})
}

// UpdateSettings handles PUT /api/v1/history/settings.
func UpdateSettings(c *gin.Context) {
	account := controllers.MustGetAccount(c)
	var req historySettingsRequest
	if err := controllers.BindBody(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := historysvc.SetPaused(c.Request.Context(), account.Id, req.Paused); err != nil {
		controllers.AbortIfError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"paused": req.Paused})
}
