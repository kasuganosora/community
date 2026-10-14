package models

import (
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func recommendationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	cfg := &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true, Logger: logger.Default.LogMode(logger.Silent)}
	var conn *gorm.DB
	var err error
	var driver gorm.Dialector = sqlite.Open("file:" + t.Name() + "?mode=memory&cache=shared")
	if dsn := os.Getenv("DISCOVERY_TEST_MYSQL_DSN"); dsn != "" {
		driver = mysql.Open(dsn)
	}
	if pgtest.Enabled() && os.Getenv("DISCOVERY_TEST_MYSQL_DSN") == "" {
		conn = pgtest.Open(t, cfg)
	} else {
		conn, err = gorm.Open(driver, cfg)
	}
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	models := []any{&User{}, &UserSetting{}, &Account{}, &AccountExtra{}, &Status{}, &StatusContent{}, &Tag{}, &StatusTag{}, &DomainBlock{}, &Block{}, &MuteAccount{}, &MuteStatus{}, &Filter{}, &Follow{}, &RecommendationFeedback{}}
	require.NoError(t, conn.AutoMigrate(models...))
	require.NoError(t, EnsureRecommendationFeedbackSchema(conn))
	for _, model := range models {
		require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true, AllowGlobalUpdate: true}).Unscoped().Delete(model).Error)
	}
	return conn
}

func recommendationTestAccount(t *testing.T, conn *gorm.DB, id uint64) *Account {
	t.Helper()
	u := &User{Id: id, UUID: fmt.Sprintf("recommendation-%d", id), UserName: fmt.Sprintf("recommendation-%d", id), EmailHash: fmt.Sprintf("recommendation-%d", id), Status: UserStatusNormal}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(u).Error)
	a := &Account{Id: id, UserId: id, Username: u.UserName, Acct: u.UserName, Discoverable: true}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Omit("Extra", "User").Create(a).Error)
	return a
}

func recommendationTestStatus(t *testing.T, conn *gorm.DB, id, author uint64, created time.Time) *Status {
	t.Helper()
	s := &Status{Id: id, AccountId: author, Visibility: StatusVisibilityPublic, CreatedAt: &created}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Omit("Content").Create(s).Error)
	c := &StatusContent{StatusId: id, Content: "A useful community post"}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(c).Error)
	return s
}

func TestRecommendationPreferencesPreserveSettings(t *testing.T) {
	conn := recommendationTestDB(t)
	a := recommendationTestAccount(t, conn, 1)
	p, err := GetRecommendationPreference(conn, a.UserId)
	require.NoError(t, err)
	require.Equal(t, RecommendationGeneric, p.Preference)
	require.Zero(t, p.Revision)
	_, err = UpdateUserSettingData(conn, a.UserId, map[string]any{"reading_expand_media": "hide_all"})
	require.NoError(t, err)
	p, err = SetRecommendationPreference(conn, a.UserId, RecommendationPersonalized)
	require.NoError(t, err)
	require.EqualValues(t, 1, p.Revision)
	_, err = UpdateUserSettingData(conn, a.UserId, map[string]any{"follow_protect": true})
	require.NoError(t, err)
	p, err = GetRecommendationPreference(conn, a.UserId)
	require.NoError(t, err)
	require.Equal(t, RecommendationPersonalized, p.Preference)
	require.EqualValues(t, 1, p.Revision)
	p, err = SetRecommendationPreference(conn, a.UserId, RecommendationOff)
	require.NoError(t, err)
	require.EqualValues(t, 2, p.Revision)
	var settings UserSetting
	require.NoError(t, conn.Where("user_id = ?", a.UserId).Take(&settings).Error)
	require.Equal(t, "hide_all", settings.Data["reading_expand_media"])
	require.Equal(t, true, settings.Data["follow_protect"])
	_, err = SetRecommendationPreference(conn, a.UserId, "enabled")
	require.ErrorIs(t, err, ErrInvalidRecommendationPreference)
	_, err = UpdateUserSettingData(conn, a.UserId, map[string]any{recommendationRevisionKey: "0"})
	require.ErrorIs(t, err, ErrInvalidRecommendationPreference)
}

func TestRecommendationConcurrentPreferenceWriters(t *testing.T) {
	conn := recommendationTestDB(t)
	if pgtest.Enabled() || os.Getenv("DISCOVERY_TEST_MYSQL_DSN") != "" {
		sqlDB, err := conn.DB()
		require.NoError(t, err)
		sqlDB.SetMaxOpenConns(8)
	}
	account := recommendationTestAccount(t, conn, 1)
	var wg sync.WaitGroup
	errors := make(chan error, 9)
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, err := UpdateUserSettingData(conn, account.UserId, map[string]any{fmt.Sprintf("independent_%d", n): true})
			errors <- err
		}(n)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := SetRecommendationPreference(conn, account.UserId, RecommendationPersonalized)
		errors <- err
	}()
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	var setting UserSetting
	require.NoError(t, conn.Where("user_id = ?", account.UserId).Take(&setting).Error)
	for n := 0; n < 8; n++ {
		require.Equal(t, true, setting.Data[fmt.Sprintf("independent_%d", n)])
	}
	p, err := GetRecommendationPreference(conn, account.UserId)
	require.NoError(t, err)
	require.Equal(t, RecommendationPersonalized, p.Preference)
	require.EqualValues(t, 1, p.Revision)
}

func TestRecommendationFeedbackActionsAndScope(t *testing.T) {
	conn := recommendationTestDB(t)
	a := recommendationTestAccount(t, conn, 1)
	recommendationTestAccount(t, conn, 2)
	_, err := PutRecommendationFeedback(conn, a.Id, "dismiss_suggestion", "account", "0002")
	require.NoError(t, err)
	_, err = PutRecommendationFeedback(conn, a.Id, "hide_author", "account", "2")
	require.NoError(t, err)
	_, err = PutRecommendationFeedback(conn, a.Id, "hide_author", "account", "2")
	require.NoError(t, err)
	rows, err := ListRecommendationFeedback(conn, a.Id)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.NoError(t, DeleteRecommendationFeedback(conn, a.Id, "dismiss_suggestion", "account", "2"))
	rows, err = ListRecommendationFeedback(conn, a.Id)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "hide_author", rows[0].Action)
	_, err = PutSiteRecommendationFeedback(conn, "account", "2")
	require.NoError(t, err)
	require.NoError(t, DeleteUserRecommendationFeedback(conn, a.Id))
	rows, err = EffectiveRecommendationFeedback(conn, a.Id)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "site", rows[0].Scope)
	require.Zero(t, rows[0].OwnerID)
	for _, invalid := range [][3]string{{"suppress", "account", "2"}, {"hide_author", "status", "2"}, {"hide_status", "status", "0"}, {"hide_tag", "tag", "<script>"}, {"hide_author", "account", "+2"}, {"hide_author", "account", "18446744073709551615"}} {
		_, err = PutRecommendationFeedback(conn, a.Id, invalid[0], invalid[1], invalid[2])
		require.ErrorIs(t, err, ErrInvalidRecommendationFeedback)
	}
	f, err := PutRecommendationFeedback(conn, a.Id, "hide_tag", "tag", "#COMMUNITY")
	require.NoError(t, err)
	require.Equal(t, "community", f.TargetKey)
}

func TestRecommendationFeedbackTagKeysKeepDistinctAccents(t *testing.T) {
	conn := recommendationTestDB(t)
	a := recommendationTestAccount(t, conn, 1)
	for _, key := range []string{"cafe", "caf\u00e9"} {
		_, err := PutRecommendationFeedback(conn, a.Id, "hide_tag", "tag", key)
		require.NoError(t, err)
	}
	rows, err := ListRecommendationFeedback(conn, a.Id)
	require.NoError(t, err)
	require.Len(t, rows, 2, "TagKey normalizes case/NFC but does not remove accents")
	require.NoError(t, DeleteRecommendationFeedback(conn, a.Id, "hide_tag", "tag", "cafe"))
	rows, err = ListRecommendationFeedback(conn, a.Id)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "caf\u00e9", rows[0].TargetKey)
}

func TestRecommendationLanguagesAtomicAndCanonical(t *testing.T) {
	conn := recommendationTestDB(t)
	a := recommendationTestAccount(t, conn, 1)
	_, err := UpdateUserSettingData(conn, a.UserId, map[string]any{"posting_default_language": "ja"})
	require.NoError(t, err)
	p, err := GetRecommendationPreference(conn, a.UserId)
	require.NoError(t, err)
	require.Empty(t, p.Languages, "posting defaults must not become reading preferences")
	languages := []string{"zh-hans", "EN-us", "en-US"}
	p, err = SetRecommendationPreferences(conn, a.UserId, RecommendationPersonalized, &languages)
	require.NoError(t, err)
	require.Equal(t, []string{"en-US", "zh-Hans"}, p.Languages)
	require.EqualValues(t, 1, p.Revision, "a combined change bumps once")
	p, err = SetRecommendationPreference(conn, a.UserId, RecommendationGeneric)
	require.NoError(t, err)
	require.Equal(t, []string{"en-US", "zh-Hans"}, p.Languages)
	require.EqualValues(t, 2, p.Revision)
	p, err = SetRecommendationLanguages(conn, a.UserId, []string{"EN-us", "zh-Hans"})
	require.NoError(t, err)
	require.EqualValues(t, 2, p.Revision, "same normalized set is idempotent")
	for _, invalid := range [][]string{{""}, {"not a language"}, {"en", "zh", "ja", "fr", "de"}} {
		_, err = SetRecommendationPreferences(conn, a.UserId, RecommendationOff, &invalid)
		require.ErrorIs(t, err, ErrInvalidRecommendationPreference)
	}
	p, err = GetRecommendationPreference(conn, a.UserId)
	require.NoError(t, err)
	require.Equal(t, RecommendationGeneric, p.Preference, "invalid language must not partially update mode")
	empty := []string{}
	p, err = SetRecommendationPreferences(conn, a.UserId, RecommendationOff, &empty)
	require.NoError(t, err)
	require.Empty(t, p.Languages)
	require.EqualValues(t, 3, p.Revision)
}

func TestRecommendationLanguageAllowedUnknownIsNotGuessed(t *testing.T) {
	for _, language := range []string{"", "und", "und-US", "und-Latn", "mul", "zxx", "invalid text", "zh", "zh-Hant"} {
		require.True(t, RecommendationLanguageAllowed(language, []string{"zh-Hans"}), language)
	}
	require.True(t, RecommendationLanguageAllowed("en-GB", []string{"en-US"}))
	require.False(t, RecommendationLanguageAllowed("ja", []string{"en", "zh"}))
	require.True(t, RecommendationLanguageAllowed("ja", nil))
}

func TestRecommendationUserDenyDoesNotRevealPrivateExistence(t *testing.T) {
	conn := recommendationTestDB(t)
	viewer := recommendationTestAccount(t, conn, 1)
	author := recommendationTestAccount(t, conn, 2)
	private := recommendationTestStatus(t, conn, 10, author.Id, time.Now().Add(-time.Hour))
	require.NoError(t, conn.Model(private).UpdateColumn("visibility", StatusVisibilityDirect).Error)
	for _, key := range []string{"10", "99999999"} {
		row, err := PutRecommendationFeedback(conn, viewer.Id, "hide_status", "status", key)
		require.NoError(t, err, "private and nonexistent targets have the same response")
		require.Equal(t, key, row.TargetKey)
		require.Equal(t, "hide_status", row.Action)
	}
}

func TestRecommendationEligibilityCurrentStateAndQuotes(t *testing.T) {
	conn := recommendationTestDB(t)
	viewer := recommendationTestAccount(t, conn, 1)
	recommendationTestAccount(t, conn, 2)
	recommendationTestAccount(t, conn, 3)
	now := time.Now().UTC()
	opts := RecommendationEligibilityOptions{Context: "home", AsOf: now, Window: 7 * 24 * time.Hour, RootWindow: true}
	root := recommendationTestStatus(t, conn, 10, 2, now.Add(-time.Hour))
	quote := recommendationTestStatus(t, conn, 11, 3, now.Add(-30*24*time.Hour))
	require.NoError(t, conn.Model(&Status{}).Where("id = ?", root.Id).UpdateColumn("quote_id", quote.Id).Error)
	input := Statuses{root}
	got, err := FilterRecommendableStatuses(conn, viewer, input, opts)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.NotNil(t, got[0].Quote)
	// The input remains public/stale; SQL unlisted must remove the entire root.
	require.NoError(t, conn.Model(&Status{}).Where("id = ?", quote.Id).UpdateColumn("visibility", StatusVisibilityUnlisted).Error)
	got, err = FilterRecommendableStatuses(conn, viewer, input, opts)
	require.NoError(t, err)
	require.Empty(t, got)
	require.NoError(t, conn.Model(&Status{}).Where("id = ?", quote.Id).UpdateColumn("visibility", StatusVisibilityPublic).Error)
	_, err = PutRecommendationFeedback(conn, viewer.Id, "hide_status", "status", strconv.FormatUint(quote.Id, 10))
	require.NoError(t, err)
	got, err = FilterRecommendableStatuses(conn, viewer, input, opts)
	require.NoError(t, err)
	require.Empty(t, got)
	require.NoError(t, DeleteRecommendationFeedback(conn, viewer.Id, "hide_status", "status", strconv.FormatUint(quote.Id, 10)))
	// A cycle cannot escape bounded embedded-object validation.
	require.NoError(t, conn.Model(&Status{}).Where("id = ?", quote.Id).UpdateColumn("quote_id", root.Id).Error)
	got, err = FilterRecommendableStatuses(conn, viewer, input, opts)
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestRecommendationEligibilityFiltersAndSuggestions(t *testing.T) {
	conn := recommendationTestDB(t)
	viewer := recommendationTestAccount(t, conn, 1)
	author := recommendationTestAccount(t, conn, 2)
	now := time.Now().UTC()
	s := recommendationTestStatus(t, conn, 10, author.Id, now.Add(-time.Hour))
	opts := RecommendationEligibilityOptions{Context: "home", AsOf: now, RootWindow: true}
	_, err := PutRecommendationFeedback(conn, viewer.Id, "dismiss_suggestion", "account", "2")
	require.NoError(t, err)
	accounts, err := FilterRecommendableAccounts(conn, viewer, []*Account{author}, true)
	require.NoError(t, err)
	require.Empty(t, accounts)
	got, err := FilterRecommendableStatuses(conn, viewer, Statuses{s}, opts)
	require.NoError(t, err)
	require.Len(t, got, 1, "suggestion dismissal must not hide posts")
	f := &Filter{AccountId: viewer.Id, Title: "home-only", FilterAction: FilterActionHide, Context: []string{"home"}, Keywords: []*FilterKeyword{{Keyword: "useful"}}}
	require.NoError(t, conn.Create(f).Error)
	got, err = FilterRecommendableStatuses(conn, viewer, Statuses{s}, opts)
	require.NoError(t, err)
	require.Empty(t, got)
	opts.Context = "public"
	got, err = FilterRecommendableStatuses(conn, viewer, Statuses{s}, opts)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.NoError(t, conn.Model(f).UpdateColumn("filter_action", FilterActionWarn).Error)
	opts.Context = "home"
	got, err = FilterRecommendableStatuses(conn, viewer, Statuses{s}, opts)
	require.NoError(t, err)
	require.Len(t, got, 1, "warn remains serializable")
	// Future, backfilled and newly private content cannot be pushed via stale input.
	for _, change := range []map[string]any{{"created_at": now.Add(time.Hour)}, {"visibility": StatusVisibilityPrivate}, {"scheduled_at": now.Add(-time.Hour)}, {"deleted_at": now}} {
		require.NoError(t, conn.Model(&Status{}).Where("id = ?", s.Id).Updates(change).Error)
		got, err = FilterRecommendableStatuses(conn, viewer, Statuses{s}, opts)
		require.NoError(t, err)
		require.Empty(t, got)
		require.NoError(t, conn.Model(&Status{}).Where("id = ?", s.Id).Updates(map[string]any{"created_at": now.Add(-time.Hour), "visibility": StatusVisibilityPublic, "scheduled_at": nil, "deleted_at": nil}).Error)
	}
}

func TestRecommendationEligibilityTagAndDomainRevocation(t *testing.T) {
	conn := recommendationTestDB(t)
	viewer := recommendationTestAccount(t, conn, 1)
	author := recommendationTestAccount(t, conn, 2)
	now := time.Now().UTC()
	s := recommendationTestStatus(t, conn, 10, author.Id, now.Add(-time.Hour))
	opts := RecommendationEligibilityOptions{Context: "public", AsOf: now, RootWindow: true}
	tag := &Tag{Name: "Community"}
	require.NoError(t, conn.Create(tag).Error)
	require.NoError(t, conn.Create(&StatusTag{StatusId: s.Id, TagId: tag.Id}).Error)
	_, err := PutSiteRecommendationFeedback(conn, "tag", "COMMUNITY")
	require.NoError(t, err)
	got, err := FilterRecommendableStatuses(conn, viewer, Statuses{s}, opts)
	require.NoError(t, err)
	require.Empty(t, got)
	require.NoError(t, DeleteSiteRecommendationFeedback(conn, "tag", "community"))
	got, err = FilterRecommendableStatuses(conn, viewer, Statuses{s}, opts)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.NoError(t, conn.Create(&Block{AccountId: author.Id, Type: BlockTypeAccount, TargetAccountId: viewer.Id}).Error)
	got, err = FilterRecommendableStatuses(conn, viewer, Statuses{s}, opts)
	require.NoError(t, err)
	require.Empty(t, got, "reverse account blocks apply")
	require.NoError(t, conn.Where("account_id = ?", author.Id).Delete(&Block{}).Error)
	require.NoError(t, conn.Model(&User{}).Where("id = ?", author.UserId).UpdateColumn("status", UserStatusUnapproved).Error)
	got, err = FilterRecommendableStatuses(conn, viewer, Statuses{s}, opts)
	require.NoError(t, err)
	require.Empty(t, got, "local user must be active")
}

func TestRecommendationEligibilityRemoteMuteAndFailureClosed(t *testing.T) {
	conn := recommendationTestDB(t)
	viewer := recommendationTestAccount(t, conn, 1)
	author := recommendationTestAccount(t, conn, 2)
	now := time.Now().UTC()
	checked := now.Add(-time.Minute)
	domain := "remote.example"
	require.NoError(t, conn.Model(&Account{}).Where("id = ?", author.Id).Updates(map[string]any{
		"domain": domain, "remote_discoverable": true, "remote_discovery_checked_at": checked,
	}).Error)
	s := recommendationTestStatus(t, conn, 10, author.Id, now.Add(-time.Hour))
	opts := RecommendationEligibilityOptions{Context: "public", AsOf: now, RootWindow: true}
	got, err := FilterRecommendableStatuses(conn, viewer, Statuses{s}, opts)
	require.NoError(t, err)
	require.Len(t, got, 1)
	policy := &DomainBlock{Domain: domain, Severity: "silence"}
	require.NoError(t, conn.Create(policy).Error)
	got, err = FilterRecommendableStatuses(conn, viewer, Statuses{s}, opts)
	require.NoError(t, err)
	require.Empty(t, got, "silenced instances cannot be promoted even when readable")
	require.NoError(t, conn.Delete(policy).Error)
	for _, change := range []map[string]any{{"remote_discoverable": nil}, {"remote_indexable": false}, {"remote_discovery_checked_at": now.Add(-25 * time.Hour)}} {
		require.NoError(t, conn.Model(&Account{}).Where("id = ?", author.Id).Updates(change).Error)
		got, err = FilterRecommendableStatuses(conn, viewer, Statuses{s}, opts)
		require.NoError(t, err)
		require.Empty(t, got)
		require.NoError(t, conn.Model(&Account{}).Where("id = ?", author.Id).Updates(map[string]any{"remote_discoverable": true, "remote_indexable": nil, "remote_discovery_checked_at": checked}).Error)
	}
	expired := now.Add(-time.Minute)
	mute := &MuteAccount{AccountId: viewer.Id, TargetId: author.Id, ExpiredAt: &expired}
	require.NoError(t, conn.Create(mute).Error)
	got, err = FilterRecommendableStatuses(conn, viewer, Statuses{s}, opts)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.NoError(t, conn.Model(mute).UpdateColumn("expired_at", nil).Error)
	got, err = FilterRecommendableStatuses(conn, viewer, Statuses{s}, opts)
	require.NoError(t, err)
	require.Empty(t, got)
	// A missing governance table cannot silently disable explicit refusals.
	if pgtest.Enabled() && os.Getenv("DISCOVERY_TEST_MYSQL_DSN") == "" {
		// pgtest also puts public on search_path for citext. Do not let a
		// pre-existing public table mask the deliberately removed test table.
		require.NoError(t, conn.Exec("SELECT set_config('search_path', current_schema(), false)").Error)
	}
	require.NoError(t, conn.Migrator().DropTable(&RecommendationFeedback{}))
	_, err = FilterRecommendableStatuses(conn, viewer, Statuses{s}, opts)
	require.Error(t, err)
}

func TestRecommendationStatusScopeWindow(t *testing.T) {
	conn := recommendationTestDB(t)
	author := recommendationTestAccount(t, conn, 1)
	now := time.Now().UTC()
	good := recommendationTestStatus(t, conn, 10, author.Id, now.Add(-time.Hour))
	future := recommendationTestStatus(t, conn, 11, author.Id, now.Add(time.Hour))
	old := recommendationTestStatus(t, conn, 12, author.Id, now.Add(-8*24*time.Hour))
	backfill := recommendationTestStatus(t, conn, 13, author.Id, now.Add(-time.Hour))
	backfill.MarkBackfilled()
	require.NoError(t, conn.Model(backfill).Select("Properties").Updates(backfill).Error)
	private := recommendationTestStatus(t, conn, 14, author.Id, now.Add(-time.Hour))
	require.NoError(t, conn.Model(private).UpdateColumn("visibility", StatusVisibilityPrivate).Error)
	_ = future
	_ = old
	var ids []uint64
	require.NoError(t, RecommendationStatusScope(conn, now, 7*24*time.Hour).Pluck("id", &ids).Error)
	require.Equal(t, []uint64{good.Id}, ids)
}
