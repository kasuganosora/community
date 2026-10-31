package seo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/moderation"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const shell = `<!doctype html><html><head><title>Community</title></head><body><div id="app"></div></body></html>`

func TestHomeAndAboutCarryDiscoveryTerms(t *testing.T) {
	restore := useExampleSite(t)
	defer restore()
	home := Resolve(nil, "/")
	about := Resolve(nil, "/about")
	require.Equal(t, 200, home.Code)
	require.False(t, home.Meta.NoIndex)
	require.NotContains(t, home.Meta.Title, "Misskey")
	require.Contains(t, home.Meta.Description, "Misskey")
	require.Contains(t, home.Meta.Description, "Mastodon")
	require.Contains(t, home.Meta.Description, "fediverse")
	require.Contains(t, home.Meta.Description, "ActivityPub")
	require.Contains(t, home.Meta.Description, "A place for notes")
	require.Contains(t, home.Meta.Keywords, "misskey")
	require.Contains(t, home.Meta.Keywords, "mastodon")
	require.Equal(t, "https://social.example/", home.Meta.Canonical)

	require.Equal(t, "About - Example", about.Meta.Title)
	require.Contains(t, about.Meta.Description, "Misskey")
	require.Contains(t, about.Meta.Keywords, "fediverse")
	require.NotEqual(t, home.Meta.Description, about.Meta.Description)
	require.Equal(t, "https://social.example/about", about.Meta.Canonical)

	html := string(Inject([]byte(shell), home))
	require.Contains(t, html, `<meta name="keywords" content="`)
	require.Contains(t, html, `<link rel="canonical" href="https://social.example/" />`)
	require.Contains(t, html, `property="og:title"`)
	require.NotContains(t, html, `name="robots"`)

	admin := Resolve(nil, "/admin/accounts")
	require.True(t, admin.Meta.NoIndex)
	require.Contains(t, string(Inject([]byte(shell), admin)), `name="robots" content="noindex"`)
	settings := Resolve(nil, "/settings/profile")
	require.True(t, settings.Meta.NoIndex)
}

func TestPublicPagesAreIndexableAndMissingOnes404(t *testing.T) {
	restore := useExampleSite(t)
	defer restore()
	conn := openSEO(t)
	policy := moderation.SetForTesting(moderation.Snapshot{UGCVisibilityForVisitor: moderation.UGCAll, HiddenTags: []string{"secret"}})
	defer policy()

	alice := makeAccount(t, conn, 1, "alice", func(a *models.Account) {
		a.DisplayName = "Alice"
		a.Note = "Writes poems"
		a.Avatar = "https://cdn.example/alice.png"
	})
	makeAccount(t, conn, 2, "bob", func(a *models.Account) { a.NoIndex = true })
	makeStatus(t, conn, 10, alice.Id, models.StatusVisibilityPublic, "a public poem about the sea")
	makeStatus(t, conn, 11, alice.Id, models.StatusVisibilityPrivate, "secret-private-body")
	makeStatus(t, conn, 12, 2, models.StatusVisibilityPublic, "bob is opted out")
	used := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	require.NoError(t, conn.Create(&models.Tag{Name: "Poetry", StatusCount: 4, LastStatusUsedAt: &used}).Error)
	require.NoError(t, conn.Create(&models.Tag{Name: "secret", StatusCount: 2, LastStatusUsedAt: &used}).Error)

	tag := Resolve(conn, "/tags/poetry")
	require.Equal(t, 200, tag.Code)
	require.False(t, tag.Meta.NoIndex, "public tag pages are indexable")
	require.Equal(t, "https://social.example/tags/Poetry", tag.Meta.Canonical)
	require.Empty(t, tag.Meta.Keywords)
	require.NotContains(t, string(Inject([]byte(shell), tag)), `name="keywords"`)
	require.NotContains(t, string(Inject([]byte(shell), tag)), "noindex")

	hidden := Resolve(conn, "/tags/secret")
	require.Equal(t, 200, hidden.Code)
	require.True(t, hidden.Meta.NoIndex)

	require.Equal(t, 404, Resolve(conn, "/tags/missing").Code)
	require.Equal(t, 404, Resolve(conn, "/tags/").Code)

	profile := Resolve(conn, "/@ALICE")
	require.Equal(t, 200, profile.Code)
	require.False(t, profile.Meta.NoIndex)
	require.Equal(t, "https://social.example/@alice", profile.Meta.Canonical)
	require.Contains(t, profile.Meta.Description, "Writes poems")
	require.Equal(t, "https://cdn.example/alice.png", profile.Meta.Image)
	require.Empty(t, profile.Meta.Keywords)

	optedOut := Resolve(conn, "/@bob")
	require.Equal(t, 200, optedOut.Code)
	require.True(t, optedOut.Meta.NoIndex)
	require.Equal(t, 404, Resolve(conn, "/@nobody").Code)

	post := Resolve(conn, "/@alice/9")
	require.Equal(t, 404, post.Code)
	visible := Resolve(conn, "/@alice/10")
	require.Equal(t, 200, visible.Code)
	require.False(t, visible.Meta.NoIndex)
	require.Contains(t, visible.Meta.Description, "a public poem about the sea")
	require.Equal(t, "https://social.example/@alice/status/10", visible.Meta.Canonical)
	require.Equal(t, visible.Meta.Canonical, Resolve(conn, "/@alice/status/10").Meta.Canonical)
	require.Contains(t, string(Inject([]byte(shell), visible)), `property="og:description"`)
	require.NotContains(t, string(Inject([]byte(shell), visible)), `name="keywords"`)

	secret := string(Inject([]byte(shell), Resolve(conn, "/@alice/status/11")))
	require.Equal(t, 404, Resolve(conn, "/@alice/status/11").Code)
	require.NotContains(t, secret, "secret-private-body")
	require.Contains(t, secret, "noindex")

	alias := Resolve(conn, "/public")
	require.Equal(t, "https://social.example/explore", alias.Meta.Canonical)
	require.Equal(t, 404, Resolve(conn, "/explore/nope").Code)
	require.Equal(t, 404, Resolve(conn, "/some/unknown-page").Code)
}

func TestSigninOnlyPostIsNotDescribed(t *testing.T) {
	restore := useExampleSite(t)
	defer restore()
	conn := openSEO(t)
	policy := moderation.SetForTesting(moderation.Snapshot{UGCVisibilityForVisitor: moderation.UGCAll})
	defer policy()
	author := makeAccount(t, conn, 1, "ada", func(a *models.Account) { a.RequireSigninToView = true })
	makeStatus(t, conn, 5, author.Id, models.StatusVisibilityPublic, "hidden-from-visitors")
	doc := Resolve(conn, "/@ada/status/5")
	require.Equal(t, 200, doc.Code)
	require.True(t, doc.Meta.NoIndex)
	html := string(Inject([]byte(shell), doc))
	require.NotContains(t, html, "hidden-from-visitors")
	require.NotContains(t, doc.Meta.Description, "hidden-from-visitors")
}

func TestSitemapListsHomeAboutProfilesAndTagsOnly(t *testing.T) {
	restore := useExampleSite(t)
	defer restore()
	conn := openSEO(t)
	resetSitemap(t)
	policy := moderation.SetForTesting(moderation.Snapshot{UGCVisibilityForVisitor: moderation.UGCAll, HiddenTags: []string{"secret"}})
	defer policy()
	profileLimit = 2
	tagLimit = 10

	alice := makeAccount(t, conn, 1, "alice", nil)
	bob := makeAccount(t, conn, 2, "bob", nil)
	carol := makeAccount(t, conn, 3, "carol", func(a *models.Account) { a.NoIndex = true })
	makeStatus(t, conn, 1, alice.Id, models.StatusVisibilityPublic, "one")
	makeStatus(t, conn, 2, alice.Id, models.StatusVisibilityPublic, "two")
	makeStatus(t, conn, 3, bob.Id, models.StatusVisibilityPublic, "one")
	makeStatus(t, conn, 4, carol.Id, models.StatusVisibilityPublic, "skip")
	makeStatus(t, conn, 5, alice.Id, models.StatusVisibilityPrivate, "nope")

	older := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	mid := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	require.NoError(t, conn.Create(&models.Tag{Name: "older", StatusCount: 1, LastStatusUsedAt: &older}).Error)
	require.NoError(t, conn.Create(&models.Tag{Name: "newer", StatusCount: 1, LastStatusUsedAt: &newer}).Error)
	require.NoError(t, conn.Create(&models.Tag{Name: "mid", StatusCount: 1, LastStatusUsedAt: &mid}).Error)
	require.NoError(t, conn.Create(&models.Tag{Name: "secret", StatusCount: 9, LastStatusUsedAt: &newer}).Error)
	require.NoError(t, conn.Create(&models.Tag{Name: "empty", StatusCount: 0, LastStatusUsedAt: &newer}).Error)

	stats, err := Refresh(context.Background(), true)
	require.NoError(t, err)
	require.Equal(t, 2, stats.Profiles)
	require.Equal(t, 3, stats.Tags)
	require.Equal(t, 7, stats.URLs) // home, about, 2 profiles, 3 tags

	body, err := Body(context.Background())
	require.NoError(t, err)
	xml := string(body)
	require.Contains(t, xml, "<loc>https://social.example/</loc>")
	require.Contains(t, xml, "<loc>https://social.example/about</loc>")
	require.Contains(t, xml, "<loc>https://social.example/@alice</loc>")
	require.Contains(t, xml, "<loc>https://social.example/@bob</loc>")
	require.NotContains(t, xml, "/@carol")
	require.Contains(t, xml, "<loc>https://social.example/tags/newer</loc>")
	require.Contains(t, xml, "<loc>https://social.example/tags/mid</loc>")
	require.Contains(t, xml, "<loc>https://social.example/tags/older</loc>")
	require.NotContains(t, xml, "/tags/secret")
	require.NotContains(t, xml, "/tags/empty")
	require.NotContains(t, xml, "/status/")
	require.Less(t, strings.Index(xml, "/@alice"), strings.Index(xml, "/@bob"))
	require.Less(t, strings.Index(xml, "/tags/newer"), strings.Index(xml, "/tags/mid"))

	require.NoError(t, conn.Create(&models.Tag{Name: "later", StatusCount: 1, LastStatusUsedAt: &newer}).Error)
	cached, err := Refresh(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, stats.Tags, cached.Tags, "a request inside the daily window must not rebuild")
	forced, err := Refresh(context.Background(), true)
	require.NoError(t, err)
	require.Equal(t, 4, forced.Tags)
	again, err := Body(context.Background())
	require.NoError(t, err)
	require.Contains(t, string(again), "/tags/later")

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/sitemap.xml", nil)
	Serve(c)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Header().Get("Content-Type"), "xml")
	require.Contains(t, w.Body.String(), "https://social.example/about")
}

func useExampleSite(t *testing.T) func() {
	t.Helper()
	prevURL := config.C.AppBaseURL
	prevConfigured := config.AppBaseURLConfigured()
	config.SetAppBaseURL("https://social.example", true)
	systemsettings.Set(systemsettings.KeyTitle, "Example")
	systemsettings.Set(systemsettings.KeyDescription, "A place for notes")
	return func() {
		config.SetAppBaseURL(prevURL, prevConfigured)
		systemsettings.Unset(systemsettings.KeyTitle)
		systemsettings.Unset(systemsettings.KeyDescription)
	}
}

func openSEO(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, conn.AutoMigrate(&models.Account{}, &models.AccountExtra{}, &models.Status{}, &models.StatusContent{}, &models.Tag{}, &models.MediaAttachment{}))
	database.SetTestDB(conn)
	t.Cleanup(database.ResetTestDB)
	return conn
}

func resetSitemap(t *testing.T) {
	t.Helper()
	sitemapMu.Lock()
	sitemapCache = cacheEntry{}
	profileLimit = defaultProfileLimit
	tagLimit = defaultTagLimit
	sitemapMu.Unlock()
	t.Cleanup(func() {
		sitemapMu.Lock()
		sitemapCache = cacheEntry{}
		profileLimit = defaultProfileLimit
		tagLimit = defaultTagLimit
		sitemapMu.Unlock()
	})
}

func makeAccount(t *testing.T, conn *gorm.DB, id uint64, name string, fn func(*models.Account)) *models.Account {
	t.Helper()
	account := &models.Account{Id: id, Username: name, DisplayName: name, Acct: name}
	if fn != nil {
		fn(account)
	}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(account).Error)
	return account
}

func makeStatus(t *testing.T, conn *gorm.DB, id, accountID uint64, visibility uint8, text string) {
	t.Helper()
	status := &models.Status{Id: id, AccountId: accountID, Visibility: visibility}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Omit("Content").Create(status).Error)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&models.StatusContent{StatusId: id, Content: text}).Error)
}
