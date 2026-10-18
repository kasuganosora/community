package account

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func relationListsDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	modelList := []any{&models.User{}, &models.UserSetting{}, &models.UserSecretData{}, &models.Account{}, &models.AccountExtra{}, &models.Follow{}, &models.Block{}, &models.MuteAccount{}}
	for _, model := range modelList {
		stmt := &gorm.Statement{DB: db}
		require.NoError(t, stmt.Parse(model))
		for _, field := range stmt.Schema.Fields {
			if field.AutoIncrement {
				field.DataType = schema.Int
			}
		}
	}
	require.NoError(t, db.AutoMigrate(modelList...))
	database.SetTestDB(db)
	t.Cleanup(database.ResetTestDB)

	return db
}

func callList(t *testing.T, handler gin.HandlerFunc, actor *models.Account, target string) (*httptest.ResponseRecorder, []map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", target, nil)
	c.Set("account", actor)
	handler(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	var out []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))

	return w, out
}

func usernames(items []map[string]any) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item["username"].(string)) //nolint:forcetypeassert
	}

	return out
}

func TestBlocksAndMutesListOrderAndLinks(t *testing.T) {
	db := relationListsDB(t)
	me := &models.Account{Id: 1, Username: "me"}
	require.NoError(t, db.Create(me).Error)
	for i, name := range []string{"a", "b", "c"} {
		target := &models.Account{Id: uint64(10 + i), Username: name} //nolint:gosec
		require.NoError(t, db.Create(target).Error)
		require.NoError(t, db.Create(&models.Block{AccountId: me.Id, Type: models.BlockTypeAccount, TargetAccountId: target.Id}).Error)
		require.NoError(t, db.Create(&models.MuteAccount{AccountId: me.Id, TargetId: target.Id}).Error)
	}
	require.NoError(t, db.Create(&models.Block{AccountId: me.Id, Type: models.BlockTypeDomain, TargetDomain: "bad.example"}).Error)

	_, items := callList(t, BlocksList, me, "/api/v1/blocks")
	assert.Equal(t, []string{"c", "b", "a"}, usernames(items))

	w, items := callList(t, BlocksList, me, "/api/v1/blocks?order=asc&limit=2")
	assert.Equal(t, []string{"a", "b"}, usernames(items))
	link := w.Header().Get("Link")
	assert.Contains(t, link, `rel="next"`)
	assert.Contains(t, link, "order=asc")
	assert.Contains(t, link, "min_id=")

	_, items = callList(t, BlocksList, me, "/api/v1/blocks?order=asc&min_id=2")
	assert.Equal(t, []string{"c"}, usernames(items))

	w, items = callList(t, MutesList, me, "/api/v1/mutes?limit=2")
	assert.Equal(t, []string{"c", "b"}, usernames(items))
	assert.Contains(t, w.Header().Get("Link"), "max_id=")

	w, items = callList(t, MutesList, me, "/api/v1/mutes?limit=5")
	assert.Len(t, items, 3)
	assert.NotContains(t, w.Header().Get("Link"), `rel="next"`, "a short page has no next link")

	// Expired mutes are not listed.
	past := time.Now().Add(-time.Hour)
	require.NoError(t, db.Model(&models.MuteAccount{}).Where("target_id = ?", 10).Update("expired_at", past).Error)
	_, items = callList(t, MutesList, me, "/api/v1/mutes?order=asc")
	assert.Equal(t, []string{"b", "c"}, usernames(items))
}

func TestDomainBlocksListOrder(t *testing.T) {
	db := relationListsDB(t)
	me := &models.Account{Id: 1, Username: "me"}
	require.NoError(t, db.Create(me).Error)
	for _, d := range []string{"one.example", "two.example"} {
		require.NoError(t, db.Create(&models.Block{AccountId: me.Id, Type: models.BlockTypeDomain, TargetDomain: d}).Error)
	}
	for _, order := range []struct{ q, first string }{{"", "two.example"}, {"?order=asc", "one.example"}} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/v1/domain_blocks"+order.q, nil)
		c.Set("account", me)
		DomainBlocksList(c)
		var out []string
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		require.Len(t, out, 2)
		assert.Equal(t, order.first, out[0])
	}
}

func TestFollowReblogsToggleAndHiddenReblogsList(t *testing.T) {
	db := relationListsDB(t)
	me := &models.Account{Id: 1, Username: "me"}
	quiet := &models.Account{Id: 2, Username: "quiet"}
	loud := &models.Account{Id: 3, Username: "loud"}
	for _, a := range []*models.Account{me, quiet, loud} {
		require.NoError(t, db.Create(a).Error)
	}
	follow := func(id, body string) map[string]any {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/", strings.NewReader(body))
		if body != "" {
			c.Request.Header.Set("Content-Type", "application/json")
		}
		c.Params = gin.Params{{Key: "id", Value: id}}
		c.Set("account", me)
		Follow(c)
		require.Equal(t, 200, w.Code, w.Body.String())
		var out map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))

		return out
	}

	assert.Equal(t, true, follow("2", "")["showing_reblogs"], "new follows show reblogs by default")
	follow("3", "")
	assert.Equal(t, false, follow("2", `{"reblogs":false}`)["showing_reblogs"])
	// Re-following without the flag keeps the earlier choice.
	assert.Equal(t, false, follow("2", "")["showing_reblogs"])

	_, items := callList(t, HiddenReblogsList, me, "/api/v1/hidden_reblogs")
	assert.Equal(t, []string{"quiet"}, usernames(items))

	rel := buildRelationship(me, quiet)
	assert.Equal(t, false, rel["showing_reblogs"])
	assert.Equal(t, true, buildRelationship(me, loud)["showing_reblogs"])

	assert.Equal(t, true, follow("2", `{"reblogs":true}`)["showing_reblogs"])
	_, items = callList(t, HiddenReblogsList, me, "/api/v1/hidden_reblogs")
	assert.Empty(t, items)

	var count int64
	require.NoError(t, db.Model(&models.Follow{}).Where("account_id = ? AND follow_id = ?", me.Id, quiet.Id).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestValidBlockDomain(t *testing.T) {
	for _, ok := range []string{"example.com", "a.b.example", "例子.测试", "xn--fsqu00a.xn--0zwm56d"} {
		assert.True(t, validBlockDomain(ok), ok)
	}
	for _, bad := range []string{"", "localhost", "exa mple.com", "a..b", "-a.com", "user@example.com", "example.com/path", "*.example.com"} {
		assert.False(t, validBlockDomain(bad), bad)
	}
}
