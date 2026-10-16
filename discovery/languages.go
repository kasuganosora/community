package discovery

import (
	"context"
	"sync"

	"github.com/kasuganosora/community/langdetect"
	"github.com/kasuganosora/community/models"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

var languageBackfill struct {
	sync.Mutex
	cursor uint64
	done   bool
}

// refreshLanguages backfills missing status languages in small batches (until
// a full pass ends; new rows are detected on insert) and refreshes a bounded
// number of stale account language caches. Called from the background loop.
func refreshLanguages(ctx context.Context, conn *gorm.DB) {
	conn = conn.WithContext(ctx)
	languageBackfill.Lock()
	for i := 0; i < 10 && !languageBackfill.done && ctx.Err() == nil; i++ {
		next, _, err := models.BackfillStatusLanguages(conn, languageBackfill.cursor, 500)
		if err != nil {
			logrus.WithError(err).Warn("status language backfill failed")
			break
		}
		if next == 0 {
			languageBackfill.done = true
			break
		}
		languageBackfill.cursor = next
	}
	languageBackfill.Unlock()
	if _, err := models.RefreshStaleAccountLanguages(conn, 500); err != nil && ctx.Err() == nil {
		logrus.WithError(err).Warn("account language refresh failed")
	}
}

// Language match tiers used by suggestion ranking.
const (
	languageTierExact = 0
	languageTierClose = 1
	languageTierOther = 2
)

func cjkGroup(lang string) bool { return lang == "zh" || lang == "ja" }

// languageTier compares a candidate's primary language with the viewer's
// ordered language set: exact match, then zh<->ja (shared CJK audience),
// then everything else, including unknown.
func languageTier(candidate string, viewer []string) int {
	candidate = langdetect.Base(candidate)
	if candidate == "" || len(viewer) == 0 {
		return languageTierOther
	}
	tier := languageTierOther
	for _, v := range viewer {
		if v == candidate {
			return languageTierExact
		}
		if cjkGroup(v) && cjkGroup(candidate) {
			tier = languageTierClose
		}
	}
	return tier
}

// viewerLanguages combines the UI locale, the viewer's own posting language
// and (fallback only) Accept-Language, deduplicated as base codes in that priority order.
func viewerLanguages(conn *gorm.DB, viewer *models.Account, acceptLanguage string) []string {
	out := []string{}
	add := func(tag string) {
		if b := langdetect.Base(tag); b != "" {
			for _, existing := range out {
				if existing == b {
					return
				}
			}
			out = append(out, b)
		}
	}
	if viewer.UserId != 0 {
		var setting models.UserSetting
		if err := conn.Where("user_id = ?", viewer.UserId).Limit(1).Find(&setting).Error; err == nil {
			if v, ok := setting.Data["interface_locale"].(string); ok {
				add(v)
			}
		}
	}
	if langs, err := models.AccountLanguages(conn, []uint64{viewer.Id}, 1); err == nil {
		add(langs[viewer.Id])
	}
	// Accept-Language is only a fallback: browsers often list English as a
	// secondary language, which would outrank the viewer's real preference.
	if len(out) == 0 {
		for _, tag := range langdetect.ParseAcceptLanguage(acceptLanguage) {
			add(tag)
		}
	}
	return out
}
