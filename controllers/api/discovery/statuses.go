package discovery

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	service "github.com/kasuganosora/community/discovery"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
)

func feedAPIError(c *gin.Context, err error) {
	c.Header("Cache-Control", "no-store")
	switch {
	case errors.Is(err, service.ErrResetRequired):
		c.JSON(409, gin.H{"code": "reset_required", "error": "Recommendations have been updated; refresh to continue", "reset_required": true})
	case errors.Is(err, service.ErrInvalidRequest), errors.Is(err, models.ErrInvalidRecommendationPreference), errors.Is(err, models.ErrInvalidRecommendationFeedback):
		c.JSON(400, gin.H{"code": "invalid_request", "error": "Invalid request parameters"})
	default:
		_ = c.Error(err)
		c.Header("Retry-After", "3")
		c.JSON(503, gin.H{"code": "unavailable", "error": "Recommendations are temporarily unavailable, please try again later", "retryable": true})
	}
}

// ListStatuses is Community's ranked feed; it intentionally does not reuse
// Mastodon's max_id pagination contract.
func ListStatuses(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	limit := 20
	if values, exists := c.Request.URL.Query()["limit"]; exists {
		if len(values) != 1 {
			feedAPIError(c, service.ErrInvalidRequest)
			return
		}
		parsed, err := strconv.Atoi(values[0])
		if err != nil || parsed < 1 || parsed > 20 {
			feedAPIError(c, service.ErrInvalidRequest)
			return
		}
		limit = parsed
	}
	if len(c.Request.URL.Query()["cursor"]) > 1 {
		feedAPIError(c, service.ErrInvalidRequest)
		return
	}
	if len(c.Query("cursor")) > 2048 {
		feedAPIError(c, service.ErrInvalidRequest)
		return
	}
	viewer, ok := c.Get("account")
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}
	account, ok := viewer.(*models.Account)
	if !ok || account == nil {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}
	// Bound serialization as well as candidate generation. All SQL performed by
	// the serializer receives the same home keyword context and deadline.
	ctx, cancel := context.WithTimeout(models.WithFilterContext(c.Request.Context(), "home"), time.Second)
	defer cancel()
	page, err := service.StatusPage(ctx, account, c.Query("cursor"), limit)
	if err != nil {
		feedAPIError(c, err)
		return
	}
	statuses := make(models.Statuses, 0, len(page.Items))
	reasons := map[string]*service.Reason{}
	for _, item := range page.Items {
		statuses = append(statuses, item.Status)
		reasons[strconv.FormatUint(item.Status.Id, 10)] = item.Reason
	}
	serialized, err := serializer.ToStatusesContext(ctx, statuses, account)
	if err != nil {
		feedAPIError(c, err)
		return
	}
	items := make([]gin.H, 0, len(serialized))
	for _, status := range serialized {
		items = append(items, gin.H{"status": status, "reason": reasons[status.Id]})
	}
	c.JSON(200, gin.H{"items": items, "next_cursor": page.NextCursor, "generation": page.Generation, "mode": page.Mode, "algorithm": service.AlgorithmVersion, "preference": page.Preference, "preference_revision": page.PreferenceRevision, "disabled": page.Disabled})
}
