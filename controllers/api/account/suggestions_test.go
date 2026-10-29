package account

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/discovery"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func suggestionFixture(t *testing.T) (*gorm.DB, []*models.Account) {
	t.Helper()
	var driver gorm.Dialector = sqlite.Open(fmt.Sprintf("file:suggestions_%d?mode=memory&cache=shared", time.Now().UnixNano()))
	if dsn := os.Getenv("DISCOVERY_TEST_MYSQL_DSN"); dsn != "" {
		driver = mysql.Open(dsn)
	} else if dsn := os.Getenv("TEST_DATABASE_URL"); dsn != "" {
		driver = dialect.OpenPostgres(dsn)
	}
	conn, err := gorm.Open(driver, &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true, Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	tables := []any{&models.User{}, &models.Account{}, &models.AccountExtra{}, &models.Emoji{}, &models.Block{}, &models.MuteAccount{}, &models.DomainBlock{}, &models.Follow{}, &models.RecommendationFeedback{}, &models.UserSetting{}, &models.SystemSetting{}, &models.Status{}, &models.StatusContent{}, &models.Tag{}, &models.StatusTag{}, &models.Filter{}, &models.MuteStatus{}, &models.FavoriteStatus{}, &models.AccountLanguage{}}
	require.NoError(t, conn.AutoMigrate(tables...))
	require.NoError(t, models.EnsureRecommendationFeedbackSchema(conn))
	for _, table := range tables {
		require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true, AllowGlobalUpdate: true}).Unscoped().Delete(table).Error)
	}
	database.SetTestDB(conn)
	t.Cleanup(func() { database.ResetTestDB(); sqlDB, _ := conn.DB(); _ = sqlDB.Close() })
	require.NoError(t, conn.AutoMigrate(&models.Follow{}, &models.RecommendationFeedback{}, &models.UserSetting{}, &models.SystemSetting{}))
	require.NoError(t, conn.Create(&models.SystemSetting{Key: systemsettings.KeyRecommendationEnabled, Val: true}).Error)
	accounts := make([]*models.Account, 0, 5)
	for i, name := range []string{"viewer", "candidate", "pending", "followed", "intermediary"} {
		u := &models.User{UserName: name, EmailHash: name, Status: models.UserStatusNormal}
		require.NoError(t, conn.Create(u).Error)
		a := &models.Account{Id: uint64(i + 1), UserId: u.Id, Username: name, Discoverable: true, Avatar: "https://cdn.example/media/" + name + "/avatar.png"}
		require.NoError(t, conn.Create(a).Error)
		accounts = append(accounts, a)
	}
	follow := func(from, to uint64, status int) {
		require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&models.Follow{AccountId: from, FollowId: to, Status: status}).Error)
	}
	follow(1, 3, models.FollowStatusRequested)
	follow(1, 4, models.FollowStatusFollowed)
	follow(1, 5, models.FollowStatusFollowed)
	follow(5, 2, models.FollowStatusRequested)
	return conn, accounts
}
func TestSuggestionsPreferenceAndAcceptedPublicGraph(t *testing.T) {
	conn, a := suggestionFixture(t)
	rows, err := discovery.SuggestAccounts(t.Context(), conn, a[0], 20)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, uint64(2), rows[0].Account.Id)
	require.Equal(t, "global", rows[0].Source)
	_, err = models.SetRecommendationPreference(conn, a[0].UserId, models.RecommendationPersonalized)
	require.NoError(t, err)
	rows, err = discovery.SuggestAccounts(t.Context(), conn, a[0], 20)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "global", rows[0].Source, "requested second hop is not an accepted relationship")
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Model(&models.Follow{}).Where("account_id = 5 AND follow_id = 2").Update("status", models.FollowStatusFollowed).Error)
	rows, err = discovery.SuggestAccounts(t.Context(), conn, a[0], 20)
	require.NoError(t, err)
	require.Equal(t, "past_interactions", rows[0].Source)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Model(&models.Account{}).Where("id = 5").Update("hide_collections", true).Error)
	rows, err = discovery.SuggestAccounts(t.Context(), conn, a[0], 20)
	require.NoError(t, err)
	require.Equal(t, "global", rows[0].Source, "hidden intermediary collections must not become recommendation evidence")
	_, err = models.SetRecommendationPreference(conn, a[0].UserId, models.RecommendationOff)
	require.NoError(t, err)
	rows, err = discovery.SuggestAccounts(t.Context(), conn, a[0], 20)
	require.NoError(t, err)
	require.Empty(t, rows)
}
func TestSuggestionDismissPersistsAndV1V2Shapes(t *testing.T) {
	conn, a := suggestionFixture(t)
	for _, v1 := range []bool{true, false} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/suggestions", nil)
		c.Set("account", a[0])
		if v1 {
			ListSuggestionsV1(c)
		} else {
			ListSuggestions(c)
		}
		require.Equal(t, 200, w.Code, w.Body.String())
		var rows []map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rows))
		require.Len(t, rows, 1)
		if v1 {
			require.Equal(t, "2", rows[0]["id"])
			require.NotContains(t, rows[0], "account")
		} else {
			require.Contains(t, rows[0], "account")
			require.Equal(t, "global", rows[0]["source"])
		}
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("DELETE", "/suggestions/2", nil)
	c.Params = gin.Params{{Key: "account_id", Value: "2"}}
	c.Set("account", a[0])
	RemoveSuggestion(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	rows, err := discovery.SuggestAccounts(t.Context(), conn, a[0], 20)
	require.NoError(t, err)
	require.Empty(t, rows)
	require.NoError(t, models.DeleteRecommendationFeedback(conn, a[0].Id, "dismiss_suggestion", "account", "2"))
	rows, err = discovery.SuggestAccounts(t.Context(), conn, a[0], 20)
	require.NoError(t, err)
	require.Len(t, rows, 1)
}

func TestSuggestionsOnlyPublicInteractionsAndUndo(t *testing.T) {
	conn, a := suggestionFixture(t)
	now := time.Now().UTC()
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Model(&models.Account{}).Where("id = 5").Update("feed_last_seen_at", now).Error)
	user := &models.User{Id: 6, UserName: "another", EmailHash: "another", Status: models.UserStatusNormal}
	require.NoError(t, conn.Create(user).Error)
	other := &models.Account{Id: 6, UserId: 6, Username: "another", Discoverable: true, Avatar: "https://cdn.example/media/another/avatar.png"}
	require.NoError(t, conn.Create(other).Error)
	at := now.Add(-time.Hour)
	for _, spec := range []struct {
		id, author uint64
		visibility uint8
	}{{1, 2, models.StatusVisibilityPublic}, {2, 6, models.StatusVisibilityPrivate}} {
		require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Omit("Content").Create(&models.Status{Id: spec.id, AccountId: spec.author, Visibility: spec.visibility, CreatedAt: &at}).Error)
		require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&models.StatusContent{StatusId: spec.id, Content: "post"}).Error)
		require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&models.FavoriteStatus{AccountId: 5, StatusId: spec.id, CreatedAt: &at}).Error)
	}
	rows, err := discovery.SuggestAccounts(t.Context(), conn, a[0], 20)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, uint64(2), rows[0].Account.Id, "only the public favourite can improve rank")
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Where("status_id = 1").Delete(&models.FavoriteStatus{}).Error)
	rows, err = discovery.SuggestAccounts(t.Context(), conn, a[0], 20)
	require.NoError(t, err)
	require.Equal(t, uint64(6), rows[0].Account.Id, "undo removes the positive feature without creating a negative")
	var setting models.SystemSetting
	require.NoError(t, conn.First(&setting).Error)
	setting.Val = false
	require.NoError(t, conn.Save(&setting).Error)
	rows, err = discovery.SuggestAccounts(t.Context(), conn, a[0], 20)
	require.NoError(t, err)
	require.Empty(t, rows, "site disable is read fresh")
}

func suggestionUser(t *testing.T, conn *gorm.DB, name, avatar string, lastStatus time.Time) {
	t.Helper()
	u := &models.User{UserName: name, EmailHash: name, Status: models.UserStatusNormal}
	require.NoError(t, conn.Create(u).Error)
	var maxID uint64
	require.NoError(t, conn.Raw("SELECT COALESCE(MAX(id), 0) FROM accounts").Scan(&maxID).Error)
	at := lastStatus
	require.NoError(t, conn.Create(&models.Account{
		Id: maxID + 1, UserId: u.Id, Username: name, Discoverable: true, Avatar: avatar, LastStatusAt: &at,
	}).Error)
}

func TestSuggestionsSkipDefaultAvatarsAndFillLimit(t *testing.T) {
	conn, existing := suggestionFixture(t)
	require.NoError(t, conn.Session(&gorm.Session{AllowGlobalUpdate: true, SkipHooks: true}).Model(&models.Account{}).Update("avatar", "").Error)

	rows, err := discovery.SuggestAccounts(t.Context(), conn, existing[0], 3)
	require.NoError(t, err)
	require.Empty(t, rows)

	base := strings.TrimRight(systemsettings.GetBaseURL(), "/")
	seed := strings.Repeat("ab", 32)
	now := time.Now().UTC()
	// Newer activity ranks first. The newest rows have no custom avatar, so
	// cutting to the limit and then dropping them would leave an empty page.
	for i, spec := range []struct {
		name, avatar string
	}{
		{"blank", ""},
		{"legacy", "/default-assets/avatar.png"},
		{"legacy-absolute", base + "/default-assets/avatar.png"},
		{"identicon", base + "/api/v1/avatars/default/" + seed + ".png"},
		{"identicon-query", "/api/v1/avatars/default/" + seed + ".png?v=1"},
		{"spaces", "   "},
		{"uploaded-new", base + "/media/101/avatar.png"},
		{"uploaded-mid", "https://cdn.example/avatars/mid.png"},
		{"uploaded-old", "avatars/legacy-upload.png"},
	} {
		suggestionUser(t, conn, spec.name, spec.avatar, now.Add(-time.Duration(i)*time.Minute))
	}

	rows, err = discovery.SuggestAccounts(t.Context(), conn, existing[0], 2)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "uploaded-new", rows[0].Account.Username)
	require.Equal(t, "uploaded-mid", rows[1].Account.Username)

	rows, err = discovery.SuggestAccounts(t.Context(), conn, existing[0], 5)
	require.NoError(t, err)
	got := make([]string, 0, len(rows))
	for _, row := range rows {
		got = append(got, row.Account.Username)
	}
	require.Equal(t, []string{"uploaded-new", "uploaded-mid", "uploaded-old"}, got)
}

func TestSuggestionsFillLimitWhenRecentAccountsLackAvatars(t *testing.T) {
	conn, existing := suggestionFixture(t)
	require.NoError(t, conn.Session(&gorm.Session{AllowGlobalUpdate: true, SkipHooks: true}).Model(&models.Account{}).Update("avatar", "").Error)
	now := time.Now().UTC()
	users := make([]*models.User, 400)
	for i := range users {
		name := fmt.Sprintf("noavatar-%03d", i)
		users[i] = &models.User{UserName: name, EmailHash: name, Status: models.UserStatusNormal}
	}
	require.NoError(t, conn.CreateInBatches(users, 50).Error)
	accounts := make([]*models.Account, 400)
	for i, u := range users {
		at := now.Add(-time.Duration(i) * time.Second)
		accounts[i] = &models.Account{Id: uint64(1000 + i), UserId: u.Id, Username: u.UserName, Discoverable: true, Avatar: "", LastStatusAt: &at}
	}
	require.NoError(t, conn.CreateInBatches(accounts, 50).Error)
	suggestionUser(t, conn, "has-avatar", "https://cdn.example/media/has-avatar.png", now.Add(-48*time.Hour))

	rows, err := discovery.SuggestAccounts(t.Context(), conn, existing[0], 1)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "has-avatar", rows[0].Account.Username)
	require.Equal(t, "global", rows[0].Source)
}
