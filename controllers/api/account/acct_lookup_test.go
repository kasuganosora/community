package account

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

// acctFixture stores a local "luna" plus same-named remote accounts on real
// SQLite (or PostgreSQL via TEST_DATABASE_URL).
func acctFixture(t *testing.T) *models.Account {
	t.Helper()
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	all := []any{&models.Account{}, &models.AccountExtra{}, &models.Emoji{}, &models.AccountEmoji{}, &models.Follow{}, &models.Block{}, &models.DomainBlock{}, &models.SystemSetting{}}
	if conn.Dialector.Name() == "sqlite" {
		sqlDB.SetMaxOpenConns(1)
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
	systemsettings.Set(systemsettings.KeyDomain, "local.example")

	local := &models.Account{Username: "luna"}
	require.NoError(t, conn.Create(local).Error)
	for _, r := range [][3]string{{"lunatic", "maid.example", "https://maid.example/users/2"}, {"luna", "maid.example", "https://maid.example/users/9h6zahxdlu"}, {"luna", "other.example", "https://other.example/users/luna"}} {
		domain := r[1]
		require.NoError(t, conn.Create(&models.Account{Username: r[0], Domain: &domain, OriginId: r[2]}).Error)
	}
	return local
}

func call(t *testing.T, handler gin.HandlerFunc, target string, viewer *models.Account) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", target, nil)
	if viewer != nil {
		c.Set("authenticated", true)
		c.Set("account", viewer)
	}
	handler(c)
	return w
}

func searchAccts(t *testing.T, target string, viewer *models.Account) []string {
	t.Helper()
	w := call(t, Search, target, viewer)
	require.Equal(t, 200, w.Code, w.Body.String())
	var accounts []serializer.Account
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &accounts))
	out := []string{}
	for _, a := range accounts {
		out = append(out, a.Acct)
	}
	return out
}

func TestAccountsSearchAcctHonoursDomain(t *testing.T) {
	viewer := acctFixture(t)
	require.Equal(t, []string{"luna@maid.example", "lunatic@maid.example"}, searchAccts(t, "/api/v1/accounts/search?q=%40luna%40maid.example&limit=40", nil))
	require.Equal(t, []string{"luna"}, searchAccts(t, "/api/v1/accounts/search?q=luna%40local.example", nil))
	require.Empty(t, searchAccts(t, "/api/v1/accounts/search?q=luna%40nowhere.example", nil))
	// A failing resolve (blocked loopback WebFinger) is not an error.
	require.Empty(t, searchAccts(t, "/api/v1/accounts/search?q=luna%40127.0.0.1&resolve=true", viewer))
	// Bare usernames rank exact matches first.
	got := searchAccts(t, "/api/v1/accounts/search?q=luna&limit=40", nil)
	require.Len(t, got, 4)
	require.Equal(t, "lunatic@maid.example", got[3])
	// Cached actor ids match exactly.
	require.Equal(t, []string{"luna@maid.example"}, searchAccts(t, "/api/v1/accounts/search?q=https%3A%2F%2Fmaid.example%2Fusers%2F9h6zahxdlu", nil))
}

func TestAccountsLookupIsExact(t *testing.T) {
	acctFixture(t)
	for target, want := range map[string]string{
		"/api/v1/accounts/lookup?acct=luna":                 "luna",
		"/api/v1/accounts/lookup?acct=%40luna":              "luna",
		"/api/v1/accounts/lookup?acct=luna%40local.example": "luna",
		"/api/v1/accounts/lookup?acct=luna%40maid.example":  "luna@maid.example",
		"/api/v1/accounts/lookup?acct=LUNA%40Maid.Example":  "luna@maid.example",
		"/api/v1/accounts/lookup?acct=luna%40other.example": "luna@other.example",
	} {
		w := call(t, Lookup, target, nil)
		require.Equal(t, 200, w.Code, target)
		var account serializer.Account
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &account))
		require.Equal(t, want, account.Acct, target)
	}
	for _, target := range []string{"/api/v1/accounts/lookup?acct=lun", "/api/v1/accounts/lookup?acct=luna%40nowhere.example", "/api/v1/accounts/lookup?acct=lunatic"} {
		require.Equal(t, 404, call(t, Lookup, target, nil).Code, target)
	}
}
