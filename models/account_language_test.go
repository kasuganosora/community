package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestStatusLanguageBackfillAndAccountPrimaryLanguage(t *testing.T) {
	conn := setupTestDB(t)
	now := time.Now()
	raw := conn.Session(&gorm.Session{SkipHooks: true})
	add := func(account uint64, lang, text string) uint64 {
		s := &Status{AccountId: account, Language: lang, CreatedAt: &now, UpdatedAt: &now}
		require.NoError(t, raw.Omit("Content").Create(s).Error)
		require.NoError(t, raw.Create(&StatusContent{StatusId: s.Id, Content: text}).Error)
		return s.Id
	}
	add(901, "", "<p>今日はとても良い天気ですね、散歩しました</p>")
	add(901, "", "<p>新しいノートを書きました、よろしくお願いします</p>")
	add(901, "en", "hello there")
	short := add(902, "", "ok")
	add(902, "zh-TW", "今天天氣很好")

	cursor, n := uint64(0), 0
	for {
		next, updated, err := BackfillStatusLanguages(conn, cursor, 2)
		require.NoError(t, err)
		n += updated
		if next == 0 {
			break
		}
		cursor = next
	}
	require.Equal(t, 2, n)
	// Idempotent: a second pass changes nothing.
	_, updated, err := BackfillStatusLanguages(conn, 0, 100)
	require.NoError(t, err)
	require.Zero(t, updated)
	var lang string
	require.NoError(t, conn.Table("statuses").Select("language").Where("id = ?", short).Scan(&lang).Error)
	require.Empty(t, lang, "too short text stays unknown")

	langs, err := AccountLanguages(conn, []uint64{901, 902, 903}, 10)
	require.NoError(t, err)
	require.Equal(t, "ja", langs[901])
	require.Equal(t, "zh", langs[902], "zh-TW normalizes to zh")
	require.Empty(t, langs[903])
	var cached int64
	require.NoError(t, conn.Model(&AccountLanguage{}).Count(&cached).Error)
	require.EqualValues(t, 3, cached)
}

func TestStatusBeforeCreateDetectsLanguage(t *testing.T) {
	s := &Status{Content: StatusContent{Content: "Сегодня очень хорошая погода, пойдём гулять"}}
	require.NoError(t, s.BeforeCreate(nil))
	require.Equal(t, "ru", s.Language)
	s = &Status{Language: "fr", Content: StatusContent{Content: "Сегодня очень хорошая погода"}}
	s.FillLanguage()
	require.Equal(t, "fr", s.Language, "explicit language wins")
}
