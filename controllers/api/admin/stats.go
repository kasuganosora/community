package admin

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
)

// GetDimensions handles GET /api/v1/admin/dimensions
func GetDimensions(c *gin.Context) {
	keys := c.QueryArray("keys[]")
	if len(keys) == 0 {
		keys = c.QueryArray("keys")
	}

	result := make([]gin.H, 0)

	for _, key := range keys {
		switch key {
		case "languages":
			result = append(result, getLanguageDimension())
		case "sources":
			result = append(result, getSourceDimension())
		case "servers":
			result = append(result, getServerDimension())
		case "space_usage":
			result = append(result, getSpaceUsageDimension())
		case "software_versions":
			result = append(result, getSoftwareVersionsDimension())
		default:
			result = append(result, gin.H{
				"key":  key,
				"data": []interface{}{},
			})
		}
	}

	c.JSON(200, result)
}

// GetMeasures handles GET /api/v1/admin/measures
func GetMeasures(c *gin.Context) {
	keys := c.QueryArray("keys[]")
	if len(keys) == 0 {
		keys = c.QueryArray("keys")
	}

	result := make([]gin.H, 0)

	for _, key := range keys {
		switch key {
		case "active_users":
			result = append(result, getActiveUsersMeasure())
		case "new_users":
			result = append(result, getNewUsersMeasure())
		case "interactions":
			result = append(result, getInteractionsMeasure())
		case "opened_reports":
			result = append(result, getOpenedReportsMeasure())
		case "resolved_reports":
			result = append(result, getResolvedReportsMeasure())
		case "status_count":
			result = append(result, getStatusCountMeasure())
		case "user_count":
			result = append(result, getUserCountMeasure())
		default:
			result = append(result, gin.H{
				"key":   key,
				"unit":  nil,
				"total": "0",
				"data":  []interface{}{},
			})
		}
	}

	c.JSON(200, result)
}

// GetRetention handles GET /api/v1/admin/retention
func GetRetention(c *gin.Context) {
	frequency := c.Query("frequency")
	if frequency == "" {
		frequency = "day"
	}

	startAt := time.Now().AddDate(0, 0, -12*7)

	data := make([]gin.H, 0)
	for i := 0; i < 12; i++ {
		weekStart := startAt.AddDate(0, 0, i*7)
		weekEnd := weekStart.AddDate(0, 0, 7)

		var cohortSize int64
		var retained int64

		db.GetDB().Model(&models.Account{}).
			Where("created_at >= ? AND created_at < ?", weekStart, weekEnd).
			Where("domain IS NULL").
			Count(&cohortSize)

		db.GetDB().Model(&models.Account{}).
			Where("created_at >= ? AND created_at < ?", weekStart, weekEnd).
			Where("domain IS NULL").
			Where("last_status_at >= ?", weekEnd).
			Count(&retained)

		data = append(data, gin.H{
			"date":        weekStart.Format("2006-01-02"),
			"frequency":   frequency,
			"percent":     calculatePercentage(retained, cohortSize),
			"cohort_size": cohortSize,
		})
	}

	c.JSON(200, data)
}

// Helper functions for dimensions

func getLanguageDimension() gin.H {
	type LanguageStat struct {
		Language string
		Count    int64
	}

	var stats []LanguageStat
	db.GetDB().Model(&models.Account{}).
		Select("COALESCE(NULLIF(language, ''), 'en') as language, COUNT(*) as count").
		Where("domain IS NULL").
		Group("language").
		Order("count DESC").
		Limit(20).
		Scan(&stats)

	data := make([]gin.H, 0, len(stats))
	for _, s := range stats {
		data = append(data, gin.H{
			"key":         s.Language,
			"human_key":   s.Language,
			"value":       s.Count,
			"human_value": formatNumber(s.Count),
		})
	}

	return gin.H{
		"key":  "languages",
		"data": data,
	}
}

func getSourceDimension() gin.H {
	type SourceStat struct {
		Source string
		Count  int64
	}

	var stats []SourceStat
	db.GetDB().Model(&models.Status{}).
		Select("application_id as source, COUNT(*) as count").
		Where("application_id IS NOT NULL").
		Group("application_id").
		Order("count DESC").
		Limit(20).
		Scan(&stats)

	data := make([]gin.H, 0, len(stats))
	for _, s := range stats {
		data = append(data, gin.H{
			"key":         s.Source,
			"human_key":   s.Source,
			"value":       s.Count,
			"human_value": formatNumber(s.Count),
		})
	}

	return gin.H{
		"key":  "sources",
		"data": data,
	}
}

func getServerDimension() gin.H {
	type ServerStat struct {
		Domain string
		Count  int64
	}

	var stats []ServerStat
	db.GetDB().Model(&models.Account{}).
		Select("domain, COUNT(*) as count").
		Where("domain IS NOT NULL").
		Group("domain").
		Order("count DESC").
		Limit(20).
		Scan(&stats)

	data := make([]gin.H, 0, len(stats))
	for _, s := range stats {
		data = append(data, gin.H{
			"key":         s.Domain,
			"human_key":   s.Domain,
			"value":       s.Count,
			"human_value": formatNumber(s.Count),
		})
	}

	return gin.H{
		"key":  "servers",
		"data": data,
	}
}

func getSpaceUsageDimension() gin.H {
	var totalStatuses int64
	var totalAccounts int64
	var totalMedia int64

	db.GetDB().Model(&models.Status{}).Count(&totalStatuses)
	db.GetDB().Model(&models.Account{}).Count(&totalAccounts)
	db.GetDB().Model(&models.Attachment{}).Count(&totalMedia)

	data := []gin.H{
		{"key": "statuses", "human_key": "Statuses", "value": totalStatuses, "human_value": formatNumber(totalStatuses)},
		{"key": "accounts", "human_key": "Accounts", "value": totalAccounts, "human_value": formatNumber(totalAccounts)},
		{"key": "media_attachments", "human_key": "Media Attachments", "value": totalMedia, "human_value": formatNumber(totalMedia)},
	}

	return gin.H{
		"key":  "space_usage",
		"data": data,
	}
}

func getSoftwareVersionsDimension() gin.H {
	data := []gin.H{
		{"key": "mastodon", "human_key": "Mastodon", "value": "4.0.0", "human_value": "4.0.0 compatible"},
	}

	return gin.H{
		"key":  "software_versions",
		"data": data,
	}
}

// Helper functions for measures

func getActiveUsersMeasure() gin.H {
	var count int64
	db.GetDB().Model(&models.Account{}).
		Where("domain IS NULL").
		Where("last_activity_at >= ?", time.Now().AddDate(0, 0, -30)).
		Count(&count)

	return gin.H{
		"key":   "active_users",
		"unit":  "users",
		"total": formatNumber(count),
		"data":  []gin.H{},
	}
}

func getNewUsersMeasure() gin.H {
	var count int64
	db.GetDB().Model(&models.Account{}).
		Where("domain IS NULL").
		Where("created_at >= ?", time.Now().AddDate(0, 0, -30)).
		Count(&count)

	return gin.H{
		"key":   "new_users",
		"unit":  "users",
		"total": formatNumber(count),
		"data":  []gin.H{},
	}
}

func getInteractionsMeasure() gin.H {
	var reblogs, replies int64

	db.GetDB().Model(&models.Status{}).Where("reblog_id IS NOT NULL").Count(&reblogs)
	db.GetDB().Model(&models.Status{}).Where("in_reply_to_id IS NOT NULL").Count(&replies)

	total := reblogs + replies

	return gin.H{
		"key":   "interactions",
		"unit":  "interactions",
		"total": formatNumber(total),
		"data":  []gin.H{},
	}
}

func getOpenedReportsMeasure() gin.H {
	var count int64
	db.GetDB().Model(&models.Report{}).Count(&count)

	return gin.H{
		"key":   "opened_reports",
		"unit":  "reports",
		"total": formatNumber(count),
		"data":  []gin.H{},
	}
}

func getResolvedReportsMeasure() gin.H {
	var count int64
	db.GetDB().Model(&models.Report{}).Where("action_taken = ?", true).Count(&count)

	return gin.H{
		"key":   "resolved_reports",
		"unit":  "reports",
		"total": formatNumber(count),
		"data":  []gin.H{},
	}
}

func getStatusCountMeasure() gin.H {
	var count int64
	db.GetDB().Model(&models.Status{}).Count(&count)

	return gin.H{
		"key":   "status_count",
		"unit":  "statuses",
		"total": formatNumber(count),
		"data":  []gin.H{},
	}
}

func getUserCountMeasure() gin.H {
	var count int64
	db.GetDB().Model(&models.Account{}).Where("domain IS NULL").Count(&count)

	return gin.H{
		"key":   "user_count",
		"unit":  "users",
		"total": formatNumber(count),
		"data":  []gin.H{},
	}
}

// Utility functions

func calculatePercentage(part, total int64) string {
	if total == 0 {
		return "0"
	}
	percentage := float64(part) / float64(total) * 100
	if percentage >= 100 {
		return "100"
	} else if percentage >= 10 {
		return formatFloat(percentage, 0)
	}
	return formatFloat(percentage, 1)
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
	// Simplified - in production use fmt.Sprintf
	if precision == 0 {
		return formatInt(int64(f))
	}
	// For now, just return as int
	return formatInt(int64(f))
}

func formatInt(n int64) string {
	if n == 0 {
		return "0"
	}
	var neg bool
	if n < 0 {
		neg = true
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}
