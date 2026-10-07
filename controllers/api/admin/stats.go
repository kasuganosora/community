package admin

import (
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

func statsKeys(c *gin.Context) []string {
	keys := c.QueryArray("keys[]")
	if len(keys) == 0 {
		keys = c.QueryArray("keys")
	}
	return keys
}

// GetDimensions reports stored data, not inferred account languages or client versions.
func GetDimensions(c *gin.Context) {
	conn := db.GetDB().WithContext(c.Request.Context())
	result := make([]gin.H, 0)
	for _, key := range statsKeys(c) {
		value, err := getStatsDimension(conn, key)
		if err != nil {
			c.JSON(500, gin.H{"error": "Unable to load statistics"})
			return
		}
		result = append(result, value)
	}
	c.JSON(200, result)
}

// publishedStats excludes scheduled and deleted posts (Status.DeletedAt is not gorm.DeletedAt).
func publishedStats(conn *gorm.DB) *gorm.DB {
	return conn.Model(&models.Status{}).Where("statuses.deleted_at IS NULL AND statuses.scheduled_at IS NULL")
}

func getStatsDimension(conn *gorm.DB, key string) (gin.H, error) {
	type dimensionRow struct {
		Key           string
		Language      string
		Domain        string
		ApplicationId uint64
		Name          string
		HumanKey      string
		Count         int64
	}
	var rows []dimensionRow
	var query *gorm.DB
	data := make([]gin.H, 0)
	switch key {
	case "languages":
		// Post language is stored on statuses; unknown is not assumed to be English.
		query = publishedStats(conn).Where("statuses.domain IS NULL AND statuses.reblog_id IS NULL").
			Select("language, COUNT(*) AS count").
			Group("language")
	case "sources":
		query = publishedStats(conn).Joins("LEFT JOIN applications ON applications.id = statuses.application_id").
			Where("statuses.domain IS NULL AND statuses.application_id IS NOT NULL").
			Select("application_id, name, COUNT(*) AS count").Group("application_id, name")
	case "servers":
		query = conn.Model(&models.Account{}).Where("domain IS NOT NULL AND domain <> ''").Select("domain, COUNT(*) AS count").Group("domain")
	case "space_usage":
		// These are record counts, not estimated storage bytes.
		for _, item := range []struct {
			key   string
			query *gorm.DB
		}{
			{"statuses", publishedStats(conn)}, {"accounts", conn.Model(&models.Account{})}, {"media_attachments", conn.Model(&models.Attachment{})},
		} {
			var count int64
			if err := item.query.Count(&count).Error; err != nil {
				return nil, err
			}
			data = append(data, gin.H{"key": item.key, "human_key": item.key, "value": count, "human_value": formatNumber(count), "unit": "records"})
		}
	case "software_versions":
		version := config.GetVersion()
		data = append(data, gin.H{"key": "community", "human_key": "Community", "value": version, "human_value": version})
	}
	if query != nil {
		if err := query.Order("count DESC").Limit(20).Scan(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			switch key {
			case "languages":
				row.Key = row.Language
			case "sources":
				row.Key = strconv.FormatUint(row.ApplicationId, 10)
				row.HumanKey = row.Name
			case "servers":
				row.Key = row.Domain
			}
			if key == "languages" && row.Key == "" {
				row.Key = "und"
			}
			label := row.HumanKey
			if label == "" {
				label = row.Key
			}
			data = append(data, gin.H{"key": row.Key, "human_key": label, "value": row.Count, "human_value": formatNumber(row.Count)})
		}
	}
	return gin.H{"key": key, "data": data}, nil
}

// GetMeasures returns exact numeric totals. New/active users use a rolling 30-day window.
func GetMeasures(c *gin.Context) {
	conn := db.GetDB().WithContext(c.Request.Context())
	now := time.Now().UTC()
	result := make([]gin.H, 0)
	for _, key := range statsKeys(c) {
		value, err := getStatsMeasure(conn, key, now)
		if err != nil {
			c.JSON(500, gin.H{"error": "Unable to load statistics"})
			return
		}
		result = append(result, value)
	}
	c.JSON(200, result)
}

func getStatsMeasure(conn *gorm.DB, key string, now time.Time) (gin.H, error) {
	var count int64
	var query *gorm.DB
	var unit any
	switch key {
	case "active_users", "new_users", "user_count":
		unit = "users"
		query = conn.Model(&models.Account{}).Where("domain IS NULL")
		if key == "active_users" {
			query = query.Where("last_activity_at >= ? AND last_activity_at <= ?", now.AddDate(0, 0, -30), now)
		}
		if key == "new_users" {
			query = query.Where("created_at >= ? AND created_at <= ?", now.AddDate(0, 0, -30), now)
		}
	case "opened_reports", "resolved_reports":
		unit = "reports"
		query = conn.Model(&models.Report{})
		// opened_reports is the total, including resolved reports, for dashboard subtraction.
		if key == "resolved_reports" {
			query = query.Where("action_taken_at IS NOT NULL")
		}
	case "status_count":
		unit = "statuses"
		query = publishedStats(conn)
	case "interactions":
		unit = "interactions"
		var reblogs, replies int64
		if err := publishedStats(conn).Where("reblog_id IS NOT NULL").Count(&reblogs).Error; err != nil {
			return nil, err
		}
		if err := publishedStats(conn).Where("in_reply_to_id IS NOT NULL").Count(&replies).Error; err != nil {
			return nil, err
		}
		count = reblogs + replies
	}
	if query != nil {
		if err := query.Count(&count).Error; err != nil {
			return nil, err
		}
	}
	value := gin.H{"key": key, "unit": unit, "total": strconv.FormatInt(count, 10), "data": []gin.H{}}
	if key == "active_users" || key == "new_users" {
		value["window_days"] = 30
	}
	return value, nil
}

// GetRetention reports 12 completed UTC day/week signup cohorts. Retained means
// an account has posted after its signup cohort ended; this is not login retention.
func GetRetention(c *gin.Context) {
	frequency := c.DefaultQuery("frequency", "day")
	if frequency != "day" && frequency != "week" {
		c.JSON(400, gin.H{"error": "frequency must be day or week"})
		return
	}
	now := time.Now().UTC()
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	days := 1
	if frequency == "week" {
		days = 7
		end = end.AddDate(0, 0, -(int(end.Weekday())+6)%7)
	}
	start := end.AddDate(0, 0, -12*days)
	conn := db.GetDB().WithContext(c.Request.Context())
	data := make([]gin.H, 0, 12)
	for i := range 12 {
		cohortStart := start.AddDate(0, 0, i*days)
		cohortEnd := cohortStart.AddDate(0, 0, days)
		var counts struct {
			CohortSize int64
			Retained   int64
		}
		cohort := conn.Model(&models.Account{}).Where("domain IS NULL AND created_at >= ? AND created_at < ?", cohortStart, cohortEnd)
		if err := cohort.Count(&counts.CohortSize).Error; err != nil {
			c.JSON(500, gin.H{"error": "Unable to load retention"})
			return
		}
		if err := cohort.Where("last_status_at >= ? AND last_status_at <= ?", cohortEnd, now).Count(&counts.Retained).Error; err != nil {
			c.JSON(500, gin.H{"error": "Unable to load retention"})
			return
		}
		data = append(data, gin.H{"date": cohortStart.Format("2006-01-02"), "frequency": frequency, "percent": calculatePercentage(counts.Retained, counts.CohortSize), "cohort_size": counts.CohortSize, "retained": counts.Retained, "retention_basis": "posted_after_cohort"})
	}
	c.JSON(200, data)
}

func calculatePercentage(part, total int64) string {
	if total <= 0 || part <= 0 {
		return "0"
	}
	if part >= total {
		return "100"
	}
	return formatFloat(float64(part)/float64(total)*100, 1)
}
func formatNumber(n int64) string {
	if n >= 1000000 {
		return formatFloat(float64(n)/1000000, 1) + "M"
	}
	if n >= 1000 {
		return formatFloat(float64(n)/1000, 1) + "K"
	}
	return formatInt(n)
}
func formatFloat(f float64, precision int) string {
	value := strconv.FormatFloat(f, 'f', precision, 64)
	if precision > 0 {
		value = strings.TrimRight(strings.TrimRight(value, "0"), ".")
	}
	return value
}
func formatInt(n int64) string { return strconv.FormatInt(n, 10) }
