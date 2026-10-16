package models

import (
	"time"

	"github.com/kasuganosora/community/langdetect"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AccountLanguage caches an account's primary posting language: the majority
// base language of its recent stored statuses. An empty Language means no
// recent status had a known language.
type AccountLanguage struct {
	AccountId uint64    `gorm:"primaryKey;autoIncrement:false" json:"account_id"`
	Language  string    `gorm:"type:varchar(16);not null;default:''" json:"language"`
	Samples   int       `gorm:"not null;default:0" json:"samples"`
	UpdatedAt time.Time `gorm:"not null;index" json:"updated_at"`
}

func (AccountLanguage) TableName() string { return "account_languages" }

const (
	accountLanguageWindow            = 90 * 24 * time.Hour
	accountLanguageSamplesPerAccount = 30
	// AccountLanguageTTL is how long a cached primary language stays fresh.
	AccountLanguageTTL = 24 * time.Hour
)

// PrimaryLanguage picks the majority language from per-language counts.
func PrimaryLanguage(counts map[string]int) (string, int) {
	best, bestN, total := "", 0, 0
	for lang, n := range counts {
		total += n
		if n > bestN || n == bestN && lang < best {
			best, bestN = lang, n
		}
	}
	return best, total
}

// RefreshAccountLanguages recomputes and stores the primary language of the
// given accounts with one bounded grouped query.
func RefreshAccountLanguages(conn *gorm.DB, ids []uint64) (map[uint64]string, error) {
	out := make(map[uint64]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		AccountID uint64
		Language  string
		N         int
	}
	recent := conn.Table("statuses").Select("account_id, language").
		Where("account_id IN ? AND reblog_id IS NULL AND deleted_at IS NULL AND language IS NOT NULL AND language <> '' AND created_at >= ?", ids, time.Now().Add(-accountLanguageWindow)).
		Order("id DESC").Limit(min(20000, accountLanguageSamplesPerAccount*len(ids)))
	if err := conn.Table("(?) AS recent", recent).Select("account_id, language, COUNT(*) AS n").Group("account_id, language").Scan(&rows).Error; err != nil {
		return nil, err
	}
	counts := map[uint64]map[string]int{}
	for _, r := range rows {
		base := langdetect.Base(r.Language)
		if base == "" {
			continue
		}
		if counts[r.AccountID] == nil {
			counts[r.AccountID] = map[string]int{}
		}
		counts[r.AccountID][base] += r.N
	}
	now := time.Now()
	cache := make([]AccountLanguage, 0, len(ids))
	for _, id := range ids {
		lang, total := PrimaryLanguage(counts[id])
		out[id] = lang
		cache = append(cache, AccountLanguage{AccountId: id, Language: lang, Samples: total, UpdatedAt: now})
	}
	err := conn.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "account_id"}}, DoUpdates: clause.AssignmentColumns([]string{"language", "samples", "updated_at"})}).CreateInBatches(cache, 200).Error
	return out, err
}

// AccountLanguages returns cached primary languages, refreshing at most
// maxRefresh missing or stale entries inline.
func AccountLanguages(conn *gorm.DB, ids []uint64, maxRefresh int) (map[uint64]string, error) {
	out := make(map[uint64]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var cached []AccountLanguage
	if err := conn.Where("account_id IN ?", ids).Find(&cached).Error; err != nil {
		return nil, err
	}
	fresh := map[uint64]bool{}
	for _, c := range cached {
		out[c.AccountId] = c.Language
		if time.Since(c.UpdatedAt) < AccountLanguageTTL {
			fresh[c.AccountId] = true
		}
	}
	stale := []uint64{}
	for _, id := range ids {
		if !fresh[id] && len(stale) < maxRefresh {
			stale = append(stale, id)
		}
	}
	refreshed, err := RefreshAccountLanguages(conn, stale)
	if err != nil {
		return nil, err
	}
	for id, lang := range refreshed {
		out[id] = lang
	}
	return out, nil
}

// RefreshStaleAccountLanguages refreshes up to limit recently active accounts
// whose cache entry is missing or stale. It returns how many were refreshed.
func RefreshStaleAccountLanguages(conn *gorm.DB, limit int) (int, error) {
	var ids []uint64
	err := conn.Table("accounts a").Select("a.id").
		Joins("LEFT JOIN account_languages l ON l.account_id = a.id").
		Where("a.deleted_at IS NULL AND a.last_status_at >= ?", time.Now().Add(-accountLanguageWindow)).
		Where("l.account_id IS NULL OR l.updated_at < ? OR l.updated_at < a.last_status_at", time.Now().Add(-AccountLanguageTTL)).
		Order("a.last_status_at DESC").Limit(limit).Scan(&ids).Error
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	_, err = RefreshAccountLanguages(conn, ids)
	return len(ids), err
}

// BackfillStatusLanguages detects the language of up to batch statuses with
// id > afterID whose language is empty. It is idempotent: only empty rows
// are written. It returns the last scanned id (0 when done).
func BackfillStatusLanguages(conn *gorm.DB, afterID uint64, batch int) (uint64, int, error) {
	var rows []struct {
		ID          uint64
		Content     string
		SpoilerText string
	}
	err := conn.Table("statuses s").Select("s.id, c.content, c.spoiler_text").
		Joins("JOIN status_contents c ON c.status_id = s.id").
		Where("s.id > ? AND (s.language IS NULL OR s.language = '') AND s.reblog_id IS NULL AND s.deleted_at IS NULL", afterID).
		Order("s.id ASC").Limit(batch).Scan(&rows).Error
	if err != nil || len(rows) == 0 {
		return 0, 0, err
	}
	updated := 0
	for _, r := range rows {
		lang := langdetect.Detect(r.SpoilerText + "\n" + r.Content)
		if lang == "" {
			continue
		}
		// UpdateColumn skips hooks and updated_at: this is derived metadata.
		res := conn.Table("statuses").Where("id = ? AND (language IS NULL OR language = '')", r.ID).UpdateColumn("language", lang)
		if res.Error != nil {
			return 0, updated, res.Error
		}
		updated += int(res.RowsAffected)
	}
	return rows[len(rows)-1].ID, updated, nil
}
