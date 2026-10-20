package llmsvc

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/thinkbot/llm"
	"gorm.io/gorm"
)

// UsageDaily aggregates LLM calls per UTC day, feature, provider and model.
type UsageDaily struct {
	ID           uint64    `gorm:"primaryKey;autoIncrement" json:"-"`
	Day          string    `gorm:"size:10;not null;uniqueIndex:idx_llm_usage_daily_key,priority:1" json:"day"`
	Feature      string    `gorm:"size:32;not null;uniqueIndex:idx_llm_usage_daily_key,priority:2" json:"feature"`
	ProviderID   string    `gorm:"size:64;not null;uniqueIndex:idx_llm_usage_daily_key,priority:3" json:"provider_id"`
	Model        string    `gorm:"size:128;not null;uniqueIndex:idx_llm_usage_daily_key,priority:4" json:"model"`
	Calls        int64     `gorm:"not null;default:0" json:"calls"`
	Errors       int64     `gorm:"not null;default:0" json:"errors"`
	InputTokens  int64     `gorm:"not null;default:0" json:"input_tokens"`
	OutputTokens int64     `gorm:"not null;default:0" json:"output_tokens"`
	CachedTokens int64     `gorm:"not null;default:0" json:"cached_tokens"`
	Cost         float64   `gorm:"not null;default:0" json:"cost"`
	Currency     string    `gorm:"size:8;not null;default:''" json:"currency"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (UsageDaily) TableName() string { return "llm_usage_daily" }

// UserUsageDaily counts LLM-backed calls per account and UTC day (quota).
type UserUsageDaily struct {
	ID        uint64 `gorm:"primaryKey;autoIncrement"`
	Day       string `gorm:"size:10;not null;uniqueIndex:idx_llm_user_usage_daily_key,priority:1"`
	Feature   string `gorm:"size:32;not null;uniqueIndex:idx_llm_user_usage_daily_key,priority:2"`
	AccountID uint64 `gorm:"not null;uniqueIndex:idx_llm_user_usage_daily_key,priority:3"`
	Calls     int64  `gorm:"not null;default:0"`
}

func (UserUsageDaily) TableName() string { return "llm_user_usage_daily" }

// Models lists the tables owned by this package (for migrations).
func Models() []any { return []any{&UsageDaily{}, &UserUsageDaily{}, &TranslationCache{}} }

// Today returns the current UTC day key.
func Today() string { return time.Now().UTC().Format("2006-01-02") }

// dbRecorder implements ThinkBot's llm.UsageRecorder on top of UsageDaily.
type dbRecorder struct{ provider ProviderConfig }

func (r *dbRecorder) RecordUsage(_ context.Context, m llm.UsageMetric) {
	_, _, cost := llm.ComputeCost(m.Usage, r.provider.Price())
	cached := m.Usage.InputTokenDetails.CacheReadTokens
	if cached == 0 {
		cached = m.Usage.CachedInputTokens
	}
	addUsage(UsageDaily{
		Day: m.At.UTC().Format("2006-01-02"), Feature: m.Feature, ProviderID: r.provider.ID, Model: m.Model,
		Calls: 1, InputTokens: int64(m.Usage.InputTokens), OutputTokens: int64(m.Usage.OutputTokens),
		CachedTokens: int64(cached), Cost: cost, Currency: r.provider.Currency,
	})
}

func recordFailure(p ProviderConfig, feature, model string) {
	addUsage(UsageDaily{Day: Today(), Feature: feature, ProviderID: p.ID, Model: model, Calls: 1, Errors: 1, Currency: p.Currency})
}

var usageMu sync.Mutex // serialises upserts on SQLite

func addUsage(u UsageDaily) {
	conn := db.GetDB()
	if conn == nil {
		return
	}
	if u.Model == "" {
		u.Model = "-"
	}
	u.UpdatedAt = time.Now()
	usageMu.Lock()
	defer usageMu.Unlock()
	updates := map[string]any{
		"calls":         gorm.Expr("calls + ?", u.Calls),
		"errors":        gorm.Expr("errors + ?", u.Errors),
		"input_tokens":  gorm.Expr("input_tokens + ?", u.InputTokens),
		"output_tokens": gorm.Expr("output_tokens + ?", u.OutputTokens),
		"cached_tokens": gorm.Expr("cached_tokens + ?", u.CachedTokens),
		"cost":          gorm.Expr("cost + ?", u.Cost),
		"currency":      u.Currency,
		"updated_at":    u.UpdatedAt,
	}
	where := func() *gorm.DB {
		return conn.Model(&UsageDaily{}).Where("day = ? AND feature = ? AND provider_id = ? AND model = ?", u.Day, u.Feature, u.ProviderID, u.Model)
	}
	if err := upsert(where, updates, &u); err != nil {
		log.Warn("llm usage: could not record usage for feature %s", u.Feature)
	}
}

// upsert increments an existing row or inserts a new one. It avoids
// dialect-specific ON CONFLICT syntax so it behaves the same on PostgreSQL,
// MySQL and SQLite; a concurrent insert of the same key (unique index) is
// resolved by retrying the update.
func upsert(where func() *gorm.DB, updates map[string]any, row any) error {
	for attempt := 0; attempt < 3; attempt++ {
		res := where().Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected > 0 {
			return nil
		}
		if err := db.GetDB().Create(row).Error; err == nil {
			return nil
		}
	}
	return errors.New("upsert failed")
}

// IncrementUserCalls adds one call to the account's daily counter.
func IncrementUserCalls(feature string, accountID uint64) {
	conn := db.GetDB()
	if conn == nil || accountID == 0 {
		return
	}
	row := UserUsageDaily{Day: Today(), Feature: feature, AccountID: accountID, Calls: 1}
	usageMu.Lock()
	defer usageMu.Unlock()
	where := func() *gorm.DB {
		return conn.Model(&UserUsageDaily{}).Where("day = ? AND feature = ? AND account_id = ?", row.Day, row.Feature, row.AccountID)
	}
	if err := upsert(where, map[string]any{"calls": gorm.Expr("calls + 1")}, &row); err != nil {
		log.Warn("llm usage: could not record user call")
	}
}

// UserCallsToday returns the account's calls for feature today.
func UserCallsToday(feature string, accountID uint64) int64 {
	return sumCalls(func(c *gorm.DB) *gorm.DB {
		return c.Model(&UserUsageDaily{}).Where("day = ? AND feature = ? AND account_id = ?", Today(), feature, accountID)
	})
}

// CallsToday returns the instance-wide calls (including failures) for feature today.
func CallsToday(feature string) int64 {
	return sumCalls(func(c *gorm.DB) *gorm.DB {
		return c.Model(&UsageDaily{}).Where("day = ? AND feature = ?", Today(), feature)
	})
}

func sumCalls(build func(*gorm.DB) *gorm.DB) int64 {
	conn := db.GetDB()
	if conn == nil {
		return 0
	}
	// Summed in Go: SUM() result types differ between drivers.
	var calls []int64
	if err := build(conn).Limit(10000).Pluck("calls", &calls).Error; err != nil {
		log.Warn("llm usage: could not read call counters")
	}
	var total int64
	for _, c := range calls {
		total += c
	}
	return total
}

// UsageSummaryDay is one row of the admin usage summary.
type UsageSummaryDay struct {
	Day          string             `json:"day"`
	Calls        int64              `json:"calls"`
	Errors       int64              `json:"errors"`
	InputTokens  int64              `json:"input_tokens"`
	OutputTokens int64              `json:"output_tokens"`
	CachedTokens int64              `json:"cached_tokens"`
	Cost         map[string]float64 `json:"cost"` // currency -> amount
}

// UsageSummary returns per-day totals (newest first) for the last days days
// plus the raw rows (per feature/provider/model).
func UsageSummary(days int) ([]UsageSummaryDay, []UsageDaily, error) {
	if days <= 0 || days > 90 {
		days = 30
	}
	since := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	rows := make([]UsageDaily, 0)
	conn := db.GetDB()
	if conn == nil {
		return nil, rows, nil
	}
	if err := conn.Where("day >= ?", since).Order("day DESC, feature, provider_id, model").Limit(5000).Find(&rows).Error; err != nil {
		return nil, nil, err
	}
	out := make([]UsageSummaryDay, 0)
	index := map[string]int{}
	for _, r := range rows {
		i, ok := index[r.Day]
		if !ok {
			i = len(out)
			index[r.Day] = i
			out = append(out, UsageSummaryDay{Day: r.Day, Cost: map[string]float64{}})
		}
		d := &out[i]
		d.Calls += r.Calls
		d.Errors += r.Errors
		d.InputTokens += r.InputTokens
		d.OutputTokens += r.OutputTokens
		d.CachedTokens += r.CachedTokens
		if r.Cost > 0 {
			d.Cost[r.Currency] += r.Cost
		}
	}
	return out, rows, nil
}
