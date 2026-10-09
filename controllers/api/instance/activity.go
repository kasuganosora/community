package instance

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
)

// activityWeek mirrors one entry of Mastodon's GET /api/v1/instance/activity.
// All values are strings, week is the unix timestamp of the week's Monday.
type activityWeek struct {
	Week          string `json:"week"`
	Statuses      string `json:"statuses"`
	Logins        string `json:"logins"`
	Registrations string `json:"registrations"`
}

// Activity handles GET /api/v1/instance/activity: weekly local activity for
// the last 12 weeks (newest first). The result is always an array.
func Activity(c *gin.Context) {
	now := time.Now().UTC()
	offset := (int(now.Weekday()) + 6) % 7
	monday := time.Date(now.Year(), now.Month(), now.Day()-offset, 0, 0, 0, 0, time.UTC)
	out := make([]activityWeek, 0, 12)

	gdb := db.GetDB()

	for i := 0; i < 12; i++ {
		start := monday.AddDate(0, 0, -7*i)
		end := start.AddDate(0, 0, 7)

		var statuses, regs int64

		gdb.Model(&models.Status{}).Where("domain IS NULL AND created_at >= ? AND created_at < ?", start, end).Count(&statuses)
		gdb.Model(&models.Account{}).Where("domain IS NULL AND created_at >= ? AND created_at < ?", start, end).Count(&regs)

		out = append(out, activityWeek{
			Week:          strconv.FormatInt(start.Unix(), 10),
			Statuses:      strconv.FormatInt(statuses, 10),
			Logins:        "0",
			Registrations: strconv.FormatInt(regs, 10),
		})
	}

	c.JSON(200, out)
}
