package search

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func acctSearchFixture(t *testing.T) *models.Account {
	t.Helper()
	conn := sqlTestDB(t)
	systemsettings.Set(systemsettings.KeyDomain, "local.example")
	remote := func(name, domain, origin string) {
		d := domain
		require.NoError(t, conn.Create(&models.Account{Username: name, Domain: &d, OriginId: origin}).Error)
	}
	local := &models.Account{Username: "luna"}
	require.NoError(t, conn.Create(local).Error)
	remote("lunatic", "maid.example", "https://maid.example/users/2")
	remote("luna", "maid.example", "https://maid.example/users/9h6zahxdlu")
	remote("luna", "other.example", "https://other.example/users/luna")
	return local
}

// sqlTestDB uses real SQLite (or PostgreSQL via TEST_DATABASE_URL): the
// account matching relies on LOWER() and CASE ordering, which the in-memory
// test engine does not evaluate.
func sqlTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	all := []any{&models.Account{}, &models.AccountExtra{}, &models.Emoji{}, &models.AccountEmoji{}, &models.Status{}, &models.StatusContent{},
		&models.Follow{}, &models.Block{}, &models.DomainBlock{}, &models.SystemSetting{}, &models.MediaAttachment{}, &models.Tag{}, &models.StatusTag{}, &models.StatusEmoji{}, &models.StatusToAccount{}, &models.Poll{}, &models.PollOption{}, &models.Filter{}, &models.FilterKeyword{}}
	if conn.Dialector.Name() == "sqlite" {
		sqlDB.SetMaxOpenConns(1)
		// Integer primary keys so SQLite assigns ids, as the real migrations do.
		for _, m := range all {
			stmt := &gorm.Statement{DB: conn}
			require.NoError(t, stmt.Parse(m))
			for _, field := range stmt.Schema.Fields {
				if field.AutoIncrement {
					field.DataType = schema.Int
				}
			}
		}
	}
	require.NoError(t, conn.AutoMigrate(all...))
	previous := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(previous); _ = sqlDB.Close() })
	return conn
}

func runSearch(t *testing.T, query string, viewer *models.Account) SearchResult {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v2/search?"+query, nil)
	if viewer != nil {
		c.Set("authenticated", true)
		c.Set("account", viewer)
	}
	Search(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	var result SearchResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	return result
}

func accts(result SearchResult) []string {
	out := make([]string, 0, len(result.Accounts))
	for _, a := range result.Accounts {
		out = append(out, a.Acct)
	}
	return out
}

func TestSearchAcctMatchesUsernameAndDomain(t *testing.T) {
	acctSearchFixture(t)
	for _, q := range []string{"q=%40luna%40maid.example&resolve=true", "q=luna%40maid.example", "q=%40LUNA%40Maid.Example&type=accounts"} {
		result := runSearch(t, q, nil)
		require.Equal(t, []string{"luna@maid.example", "lunatic@maid.example"}, accts(result), q)
		require.Empty(t, result.Statuses)
		require.Empty(t, result.Hashtags)
	}
}

func TestSearchAcctWithOwnDomainIsLocal(t *testing.T) {
	acctSearchFixture(t)
	require.Equal(t, []string{"luna"}, accts(runSearch(t, "q=%40luna%40local.example", nil)))
}

func TestSearchAcctUnknownDomainReturnsNothingLocal(t *testing.T) {
	acctSearchFixture(t)
	require.Empty(t, accts(runSearch(t, "q=%40luna%40nowhere.example", nil)))
}

func TestSearchAuthenticatedResolveFailureKeepsCachedResults(t *testing.T) {
	viewer := acctSearchFixture(t)
	// The WebFinger target is a blocked loopback address; the failure must not
	// turn the whole search into an error.
	result := runSearch(t, "q=%40luna%40127.0.0.1&resolve=true", viewer)
	require.Empty(t, result.Accounts)
	result = runSearch(t, "q=%40luna%40maid.example&resolve=true", viewer)
	require.Equal(t, []string{"luna@maid.example", "lunatic@maid.example"}, accts(result))
}

func TestSearchURLMatchesCachedAccounts(t *testing.T) {
	acctSearchFixture(t)
	require.Equal(t, []string{"luna@maid.example"}, accts(runSearch(t, "q=https%3A%2F%2Fmaid.example%2Fusers%2F9h6zahxdlu&resolve=true", nil)))
	require.Equal(t, []string{"luna@maid.example"}, accts(runSearch(t, "q=https%3A%2F%2Fmaid.example%2F%40luna", nil)))
	require.Equal(t, []string{"luna"}, accts(runSearch(t, "q=https%3A%2F%2Flocal.example%2F%40luna", nil)))
	require.Empty(t, accts(runSearch(t, "q=https%3A%2F%2Fmaid.example%2F%40nobody", nil)))
}

func TestSearchBareUsernameStillFindsAll(t *testing.T) {
	acctSearchFixture(t)
	got := accts(runSearch(t, "q=luna&type=accounts", nil))
	require.Len(t, got, 4)
	require.ElementsMatch(t, []string{"luna", "luna@maid.example", "luna@other.example"}, got[:3], "exact usernames rank before prefix matches")
}

func TestSearchURLMatchesCachedStatus(t *testing.T) {
	acctSearchFixture(t)
	var remote models.Account
	require.NoError(t, database.GetDB().Where("origin_id = ?", "https://maid.example/users/9h6zahxdlu").First(&remote).Error)
	uri := "https://maid.example/notes/a1b2c3"
	require.NoError(t, database.GetDB().Create(&models.Status{AccountId: remote.Id, OriginUri: &uri, Visibility: models.StatusVisibilityPublic, Content: models.StatusContent{Content: "hello"}}).Error)
	result := runSearch(t, "q=https%3A%2F%2Fmaid.example%2Fnotes%2Fa1b2c3", nil)
	require.Len(t, result.Statuses, 1)
	require.Empty(t, result.Accounts)
	result = runSearch(t, "q=https%3A%2F%2Fmaid.example%2Fnotes%2Fa1b2c3&type=accounts", nil)
	require.Empty(t, result.Statuses)
}
