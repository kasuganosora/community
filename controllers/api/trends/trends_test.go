package trends

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/db/dialect"
	redisdb "github.com/kasuganosora/community/db/redis"
	"github.com/kasuganosora/community/discovery"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupTrendDB(t *testing.T) *gorm.DB {
	t.Helper()
	var driver gorm.Dialector = sqlite.Open(fmt.Sprintf("file:trend_%d?mode=memory&cache=shared", time.Now().UnixNano()))
	if dsn := os.Getenv("DISCOVERY_TEST_MYSQL_DSN"); dsn != "" {
		driver = mysql.Open(dsn)
	} else if dsn := os.Getenv("TEST_DATABASE_URL"); dsn != "" {
		driver = dialect.OpenPostgres(dsn)
	}
	conn, err := gorm.Open(driver, &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true, Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	tables := []any{&models.User{}, &models.Account{}, &models.AccountExtra{}, &models.Emoji{}, &models.Status{}, &models.StatusContent{}, &models.Tag{}, &models.StatusTag{}, &models.StatusCard{}, &models.DomainBlock{}, &models.Block{}, &models.MuteAccount{}, &models.MuteStatus{}, &models.Follow{}, &models.Filter{}, &models.RecommendationFeedback{}, &models.UserSetting{}, &models.SystemSetting{}}
	require.NoError(t, conn.AutoMigrate(tables...))
	require.NoError(t, models.EnsureRecommendationFeedbackSchema(conn))
	for _, table := range tables {
		require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true, AllowGlobalUpdate: true}).Unscoped().Delete(table).Error)
	}
	database.SetTestDB(conn)
	t.Cleanup(func() { database.ResetTestDB(); sqlDB, _ := conn.DB(); _ = sqlDB.Close() })
	return conn
}
func trendAuthor(t *testing.T, conn *gorm.DB, id uint64) *models.Account {
	t.Helper()
	user := &models.User{Id: id, UserName: fmt.Sprintf("user%d", id), EmailHash: fmt.Sprintf("email%d", id), Status: models.UserStatusNormal}
	require.NoError(t, conn.Create(user).Error)
	a := &models.Account{Id: id, UserId: id, Username: user.UserName, Discoverable: true}
	require.NoError(t, conn.Create(a).Error)
	return a
}
func trendPost(t *testing.T, conn *gorm.DB, id uint64, author *models.Account, at time.Time, visibility uint8, tags ...*models.Tag) {
	t.Helper()
	s := &models.Status{Id: id, AccountId: author.Id, Domain: author.Domain, Visibility: visibility, CreatedAt: &at, UpdatedAt: &at}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Omit("Content").Create(s).Error)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&models.StatusContent{StatusId: id, Content: "a public post"}).Error)
	for _, tag := range tags {
		require.NoError(t, conn.Create(&models.StatusTag{StatusId: id, TagId: tag.Id}).Error)
	}
}
func trendContext(path string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", path, nil)
	return c, w
}
func TestTagCountsDistinctAndRevoke(t *testing.T) {
	conn := setupTrendDB(t)
	asOf := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	tag := &models.Tag{Name: "GoLang"}
	require.NoError(t, conn.Create(tag).Error)
	for i := uint64(1); i <= 5; i++ {
		a := trendAuthor(t, conn, i)
		trendPost(t, conn, i, a, asOf.Add(-time.Hour), models.StatusVisibilityPublic, tag)
	}
	var first models.Account
	require.NoError(t, conn.First(&first, 1).Error)
	trendPost(t, conn, 10, &first, asOf.Add(-25*time.Hour), models.StatusVisibilityPublic, tag)
	trendPost(t, conn, 11, &first, asOf.Add(-time.Minute), models.StatusVisibilityPrivate, tag)
	trendPost(t, conn, 12, &first, asOf.Add(time.Minute), models.StatusVisibilityPublic, tag)
	rows, err := discovery.AggregateTagTrends(t.Context(), conn, nil, "7d", "local", asOf, nil)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, 6, rows[0].PostsCount)
	require.Equal(t, 5, rows[0].AuthorsCount)
	require.Equal(t, "5", rows[0].History[0].Accounts)
	require.Equal(t, "1", rows[0].History[1].Accounts)
	rows, err = discovery.AggregateTagTrends(t.Context(), conn, nil, "24h", "local", asOf, nil)
	require.NoError(t, err)
	require.Equal(t, 5, rows[0].PostsCount)
	require.NoError(t, conn.Model(&models.Status{}).Where("id = ?", 2).Update("visibility", models.StatusVisibilityUnlisted).Error)
	rows, err = discovery.AggregateTagTrends(t.Context(), conn, nil, "24h", "local", asOf, []string{"golang"})
	require.NoError(t, err)
	require.Equal(t, 4, rows[0].AuthorsCount)
	require.NoError(t, conn.Model(&models.Account{}).Where("id IN ?", []uint64{1, 3, 4, 5}).Update("is_ban", true).Error)
	rows, err = discovery.AggregateTagTrends(t.Context(), conn, nil, "24h", "local", asOf, nil)
	require.NoError(t, err)
	require.Empty(t, rows)
}
func TestTagPersonalHideAndSmallSiteCompatibility(t *testing.T) {
	conn := setupTrendDB(t)
	now := time.Now().UTC()
	author := trendAuthor(t, conn, 1)
	viewer := trendAuthor(t, conn, 2)
	tag := &models.Tag{Name: "秘密"}
	require.NoError(t, conn.Create(tag).Error)
	trendPost(t, conn, 1, author, now.Add(-time.Hour), models.StatusVisibilityPublic, tag)
	rows, err := discovery.AggregateTagTrends(t.Context(), conn, viewer, "24h", "federated", now, nil)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	_, err = models.PutRecommendationFeedback(conn, viewer.Id, "hide_tag", "tag", tag.Name)
	require.NoError(t, err)
	rows, err = discovery.AggregateTagTrends(t.Context(), conn, viewer, "24h", "federated", now, nil)
	require.NoError(t, err)
	require.Empty(t, rows)
	c, w := trendContext("/api/v1/trends/tags")
	ListTags(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.JSONEq(t, "[]", w.Body.String())
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
}
func TestLinksRequireCurrentPublicSource(t *testing.T) {
	conn := setupTrendDB(t)
	now := time.Now().UTC()
	a := trendAuthor(t, conn, 1)
	for id, visibility := range []uint8{models.StatusVisibilityPublic, models.StatusVisibilityUnlisted, models.StatusVisibilityPrivate, models.StatusVisibilityDirect} {
		n := uint64(id + 1)
		trendPost(t, conn, n, a, now.Add(-time.Hour), visibility)
		card := &models.StatusCard{StatusId: n, Url: fmt.Sprintf("https://example.test/%d", n), Title: fmt.Sprintf("card%d", n), UrlHash: fmt.Sprint(n)}
		require.NoError(t, conn.Create(card).Error)
	}
	c, w := trendContext("/api/v1/trends/links")
	ListLinks(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	var rows []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rows))
	require.Len(t, rows, 1)
	require.Equal(t, "https://example.test/1", rows[0]["url"])
	require.NoError(t, conn.Model(&models.Status{}).Where("id = 1").Update("deleted_at", now).Error)
	c, w = trendContext("/api/v1/trends/links")
	ListLinks(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.JSONEq(t, "[]", w.Body.String())
}
func TestTrendsSQLFailureDoesNotReturnCachedData(t *testing.T) {
	conn := setupTrendDB(t)
	require.NoError(t, conn.Migrator().DropTable(&models.Status{}))
	c, w := trendContext("/api/v1/trends/tags")
	ListTags(c)
	require.Equal(t, 503, w.Code)
	require.Equal(t, "5", w.Header().Get("Retry-After"))
}

func TestTagSnapshotBoundCursorAndCurrentRecheck(t *testing.T) {
	conn := setupTrendDB(t)
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	previous := redisdb.GetRedisesForTest()
	var previousUniversal redis.UniversalClient
	if redisdb.HasUniversalRedis() {
		previousUniversal = redisdb.GetUniversalRedis()
	}
	redisdb.SetRedisesForTest(nil)
	redisdb.SetUniversalRedis(client)
	t.Cleanup(func() {
		redisdb.SetUniversalRedis(previousUniversal)
		redisdb.SetRedisesForTest(previous)
		_ = client.Close()
	})
	discovery.Configure(discovery.Config{SigningKeys: [][]byte{[]byte("a-shared-deployment-key-at-least-32-bytes")}})
	now := time.Now().UTC()
	tags := []*models.Tag{{Name: "alpha"}, {Name: "beta"}}
	for _, tag := range tags {
		require.NoError(t, conn.Create(tag).Error)
	}
	for i := uint64(1); i <= 5; i++ {
		a := trendAuthor(t, conn, i)
		trendPost(t, conn, i, a, now.Add(-time.Hour), models.StatusVisibilityPublic, tags...)
	}
	page, err := discovery.ReadTagTrends(t.Context(), nil, "24h", "local", 1, "")
	require.NoError(t, err)
	require.Equal(t, "trending", page.Kind)
	require.Len(t, page.Items, 1)
	require.NotNil(t, page.NextCursor)
	var account models.Account
	require.NoError(t, conn.First(&account, 1).Error)
	_, err = discovery.ReadTagTrends(t.Context(), &account, "24h", "local", 1, *page.NextCursor)
	require.ErrorIs(t, err, discovery.ErrTagCursor, "a cursor must not transfer across accounts")
	_, err = discovery.ReadTagTrends(t.Context(), nil, "7d", "local", 1, *page.NextCursor)
	require.ErrorIs(t, err, discovery.ErrTagCursor, "window is bound into the cursor")
	// The old candidate remains in Redis; its current support now falls below
	// threshold and must be omitted rather than keeping a stale high rank.
	require.NoError(t, conn.Model(&models.Status{}).Where("id = 1").Update("visibility", models.StatusVisibilityUnlisted).Error)
	next, err := discovery.ReadTagTrends(t.Context(), nil, "24h", "local", 1, *page.NextCursor)
	require.NoError(t, err)
	require.Empty(t, next.Items)
	require.Nil(t, next.NextCursor)
	// A fresh low-volume board honestly becomes recent topics.
	recent, err := discovery.ReadTagTrends(t.Context(), nil, "24h", "federated", 20, "")
	require.NoError(t, err)
	require.Equal(t, "recent", recent.Kind)
	require.Len(t, recent.Items, 2)
	require.Equal(t, 4, recent.Items[0].AuthorsCount)
	server.FastForward(11 * time.Minute)
	_, err = discovery.ReadTagTrends(t.Context(), nil, "24h", "local", 1, *page.NextCursor)
	require.ErrorIs(t, err, discovery.ErrTagCursor)
	redisdb.SetUniversalRedis(nil)
	_, err = discovery.ReadTagTrends(t.Context(), nil, "24h", "local", 1, "")
	require.ErrorIs(t, err, discovery.ErrUnavailable, "no Redis configuration must not panic")
	redisdb.SetUniversalRedis(client)
	require.NoError(t, conn.Create(&models.SystemSetting{Key: systemsettings.KeyDiscoveryTrendsEnabled, Val: false}).Error)
	disabled, err := discovery.ReadTagTrends(t.Context(), nil, "24h", "local", 1, "")
	require.NoError(t, err)
	require.True(t, disabled.Disabled)
	require.Empty(t, disabled.Items)
	c, w := trendContext("/api/v1/trends/tags")
	ListTags(c)
	require.Equal(t, 200, w.Code)
	require.JSONEq(t, "[]", w.Body.String())
}

func TestTagTrendsOverCapArePartialAndLocalScopeIsIndependent(t *testing.T) {
	conn := setupTrendDB(t)
	defer discovery.SetTagTrendLimitsForTest(3, 50000)()
	asOf := time.Now().UTC()
	tag := &models.Tag{Name: "Fediverse"}
	require.NoError(t, conn.Create(tag).Error)
	for i := uint64(1); i <= 5; i++ {
		a := trendAuthor(t, conn, i)
		trendPost(t, conn, i, a, asOf.Add(-time.Duration(i)*time.Minute), models.StatusVisibilityPublic, tag)
	}
	// Five statuses with a cap of three: newest three, flagged partial.
	rows, partial, err := discovery.AggregateTagTrendsPartial(t.Context(), conn, nil, "24h", "federated", asOf, nil)
	require.NoError(t, err)
	require.True(t, partial)
	require.Len(t, rows, 1)
	require.Equal(t, 3, rows[0].PostsCount)
	// Make four of them remote: local scope only sees one and is complete.
	remote := "remote.example"
	require.NoError(t, conn.Model(&models.Status{}).Where("id IN ?", []uint64{1, 2, 3, 4}).Update("domain", &remote).Error)
	rows, partial, err = discovery.AggregateTagTrendsPartial(t.Context(), conn, nil, "24h", "local", asOf, nil)
	require.NoError(t, err)
	require.False(t, partial)
	require.Len(t, rows, 1)
	require.Equal(t, 1, rows[0].PostsCount)
	// Association cap also degrades to partial rather than failing.
	discovery.SetTagTrendLimitsForTest(100, 2)
	rows, partial, err = discovery.AggregateTagTrendsPartial(t.Context(), conn, nil, "24h", "federated", asOf, nil)
	require.NoError(t, err)
	require.True(t, partial)
	require.Equal(t, 2, rows[0].PostsCount)
	// The compatibility endpoint answers 200 with the partial header.
	c, w := trendContext("/api/v1/trends/tags?scope=federated&window=24h")
	ListTags(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, "true", w.Header().Get("X-Trends-Partial"))
	c, w = trendContext("/api/v1/trends/tags?scope=local")
	ListTags(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Empty(t, w.Header().Get("X-Trends-Partial"))
	c, w = trendContext("/api/v1/trends/tags?scope=bogus")
	ListTags(c)
	require.Equal(t, 400, w.Code)
}

func TestTagTrendsCountUnknownRemoteAuthorsButNotRefusals(t *testing.T) {
	conn := setupTrendDB(t)
	asOf := time.Now().UTC()
	tag := &models.Tag{Name: "Legacy"}
	require.NoError(t, conn.Create(tag).Error)
	remote := "legacy.example"
	for i := uint64(1); i <= 6; i++ {
		a := trendAuthor(t, conn, i)
		// Pre-discovery-column remote account: legacy discoverable=false, nothing read.
		require.NoError(t, conn.Model(&models.Account{}).Where("id = ?", a.Id).Updates(map[string]any{"domain": remote, "discoverable": false, "remote_discoverable": nil, "remote_discovery_checked_at": nil, "remote_discovery_source": ""}).Error)
		trendPost(t, conn, i, a, asOf.Add(-time.Duration(i)*time.Minute), models.StatusVisibilityPublic, tag)
		require.NoError(t, conn.Model(&models.Status{}).Where("id = ?", i).Update("domain", remote).Error)
	}
	rows, err := discovery.AggregateTagTrends(t.Context(), conn, nil, "24h", "federated", asOf, nil)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, 6, rows[0].AuthorsCount)
	c, w := trendContext("/api/v1/trends/tags")
	ListTags(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "Legacy")
	// An explicit refusal read from the actor removes that author.
	require.NoError(t, conn.Model(&models.Account{}).Where("id = ?", 1).Updates(map[string]any{"remote_discoverable": false, "remote_discovery_checked_at": asOf, "remote_discovery_source": "actor"}).Error)
	rows, err = discovery.AggregateTagTrends(t.Context(), conn, nil, "24h", "federated", asOf, nil)
	require.NoError(t, err)
	require.Equal(t, 5, rows[0].AuthorsCount)
	// Individual status recommendations still require an actual opt-in.
	var statuses models.Statuses
	require.NoError(t, conn.Where("id IN ?", []uint64{2, 3}).Find(&statuses).Error)
	got, err := models.FilterRecommendableStatuses(conn, nil, statuses, models.RecommendationEligibilityOptions{Context: "public", AsOf: asOf, RootWindow: true})
	require.NoError(t, err)
	require.Empty(t, got)
}
