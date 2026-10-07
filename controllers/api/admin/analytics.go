package admin

import (
	"context"
	"database/sql/driver"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// analyticsDate normalizes SQL DATE values before daily bucket lookup. MySQL
// parseTime=true returns time.Time; SQLite returns text. A DATE represents a
// calendar date, so preserve its calendar fields rather than shifting zones.
type analyticsDate string

func (d *analyticsDate) Scan(value any) error {
	var text string
	switch v := value.(type) {
	case time.Time:
		text = v.Format("2006-01-02")
	case string:
		text = v
	case []byte:
		text = string(v)
	default:
		return fmt.Errorf("unsupported analytics date type %T", value)
	}
	parsed, err := time.Parse("2006-01-02", text)
	if err != nil {
		return fmt.Errorf("invalid analytics date")
	}
	*d = analyticsDate(parsed.Format("2006-01-02"))
	return nil
}
func (d analyticsDate) Value() (driver.Value, error) { return string(d), nil }
func (analyticsDate) GormDataType() string           { return "string" }

type analyticsDay struct {
	Date            analyticsDate `json:"date"`
	LocalPosts      int64         `json:"local_posts"`
	RemotePosts     int64         `json:"remote_posts"`
	PostingAccounts int64         `json:"posting_accounts"`
}
type analyticsCohort struct {
	Date     string `json:"date"`
	Size     int64  `json:"size"`
	Retained int64  `json:"retained"`
}
type analyticsDomain struct {
	Domain string `json:"domain"`
	Count  int64  `json:"count"`
}
type analyticsResponse struct {
	GeneratedAt time.Time                    `json:"generated_at"`
	Timezone    string                       `json:"timezone"`
	Days        int                          `json:"days"`
	Totals      map[string]int64             `json:"totals"`
	Daily       []analyticsDay               `json:"daily"`
	Cohorts     []analyticsCohort            `json:"cohorts"`
	Federation  map[string][]analyticsDomain `json:"federation"`
	Online      gin.H                        `json:"online"`
}

// GetAnalytics serves independently computed aggregates. Failure is never a zero dataset.
func GetAnalytics(c *gin.Context) {
	days := 30
	switch c.DefaultQuery("days", "30") {
	case "7":
		days = 7
	case "30":
	default:
		c.JSON(400, gin.H{"error": "days must be 7 or 30"})
		return
	}
	// Bound database work, including cancellation when an administrator leaves the page.
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	result, err := loadAnalytics(db.GetDB().WithContext(ctx), time.Now().UTC(), days)
	if err != nil {
		c.JSON(500, gin.H{"error": "Analytics temporarily unavailable"})
		return
	}
	c.JSON(200, result)
}

func loadAnalytics(conn *gorm.DB, now time.Time, days int) (*analyticsResponse, error) {
	now = now.UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	start := today.AddDate(0, 0, -179)
	out := &analyticsResponse{GeneratedAt: now, Timezone: "UTC", Days: days, Totals: map[string]int64{}, Daily: make([]analyticsDay, 180), Cohorts: make([]analyticsCohort, 0, 12), Federation: map[string][]analyticsDomain{}, Online: gin.H{"available": false, "reason": "Historical online/session presence is not recorded; posting activity is not online or login activity."}}
	for _, item := range []struct {
		key   string
		query *gorm.DB
	}{
		{"local_accounts", conn.Model(&models.Account{}).Where("domain IS NULL")},
		{"remote_accounts", conn.Model(&models.Account{}).Where("domain IS NOT NULL AND domain <> ''")},
		{"local_posts", publishedStats(conn).Where("domain IS NULL")},
		{"remote_posts", publishedStats(conn).Where("domain IS NOT NULL AND domain <> ''")},
		{"instances", conn.Model(&models.Account{}).Where("domain IS NOT NULL AND domain <> ''").Distinct("domain")},
		{"local_emojis", conn.Model(&models.Emoji{}).Where("domain = ''")},
		{"remote_emojis", conn.Model(&models.Emoji{}).Where("domain <> ''")},
	} {
		var count int64
		if err := item.query.Count(&count).Error; err != nil {
			return nil, err
		}
		out.Totals[item.key] = count
	}
	// The raw timestamp range remains sargable; DATE only groups the bounded result.
	var daily []analyticsDay
	err := publishedStats(conn).Where("statuses.created_at >= ? AND statuses.created_at <= ?", start, now).
		Select("DATE(statuses.created_at) AS date, SUM(CASE WHEN statuses.domain IS NULL THEN 1 ELSE 0 END) AS local_posts, SUM(CASE WHEN statuses.domain IS NOT NULL AND statuses.domain <> '' THEN 1 ELSE 0 END) AS remote_posts, COUNT(DISTINCT CASE WHEN statuses.domain IS NULL THEN statuses.account_id ELSE NULL END) AS posting_accounts").Group("DATE(statuses.created_at)").Scan(&daily).Error
	if err != nil {
		return nil, err
	}
	byDate := make(map[string]analyticsDay, len(daily))
	for _, row := range daily {
		byDate[string(row.Date)] = row
	}
	for i := range out.Daily {
		date := start.AddDate(0, 0, i).Format("2006-01-02")
		out.Daily[i] = byDate[date]
		out.Daily[i].Date = analyticsDate(date)
	}
	// Twelve fully observed UTC Monday cohorts: a signup week followed by a
	// complete seven-day observation window. No current activity proxy is used.
	monday := today.AddDate(0, 0, -(int(today.Weekday())+6)%7)
	for i := 0; i < 12; i++ {
		begin := monday.AddDate(0, 0, -(13-i)*7)
		end := begin.AddDate(0, 0, 7)
		observedEnd := end.AddDate(0, 0, 7)
		row := analyticsCohort{Date: begin.Format("2006-01-02")}
		cohort := conn.Model(&models.Account{}).Where("accounts.domain IS NULL AND accounts.created_at >= ? AND accounts.created_at < ?", begin, end)
		if err := cohort.Count(&row.Size).Error; err != nil {
			return nil, err
		}
		if row.Size > 0 {
			err := cohort.Where("EXISTS (SELECT 1 FROM statuses s WHERE s.account_id = accounts.id AND s.domain IS NULL AND s.deleted_at IS NULL AND s.scheduled_at IS NULL AND s.created_at >= ? AND s.created_at < ?)", end, observedEnd).Count(&row.Retained).Error
			if err != nil {
				return nil, err
			}
		}
		out.Cohorts = append(out.Cohorts, row)
	}
	for _, direction := range []string{"outgoing", "incoming"} {
		localID, remoteID := "f.account_id", "f.follow_id"
		if direction == "incoming" {
			localID, remoteID = remoteID, localID
		}
		rows := make([]analyticsDomain, 0)
		err := conn.Table("follows AS f").Joins("JOIN accounts AS local_account ON local_account.id = "+localID).
			Joins("JOIN accounts AS remote_account ON remote_account.id = "+remoteID).
			Where("f.status = ? AND local_account.domain IS NULL AND local_account.deleted_at IS NULL AND remote_account.domain IS NOT NULL AND remote_account.domain <> '' AND remote_account.deleted_at IS NULL", models.FollowStatusFollowed).
			Select("remote_account.domain AS domain, COUNT(*) AS count").Group("remote_account.domain").Order("count DESC").Order("remote_account.domain ASC").Limit(20).Scan(&rows).Error
		if err != nil {
			return nil, err
		}
		out.Federation[direction] = rows
	}
	return out, nil
}
