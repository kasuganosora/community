package list

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func callTags(t *testing.T, fn func(*gin.Context), method string, list *models.List, as *models.Account, names ...string) (int, []string) {
	t.Helper()
	var body []byte
	if names != nil {
		body, _ = json.Marshal(map[string]any{"names": names})
	}
	c, w := newContext(t, method, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10)+"/tags", body, as)
	setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
	callHandler(fn, c)
	var tags []struct{ Name string }
	_ = json.Unmarshal(w.Body.Bytes(), &tags)
	out := []string{}
	for _, tag := range tags {
		out = append(out, tag.Name)
	}
	return w.Code, out
}

// sqliteListDB uses real SQLite (or PostgreSQL via TEST_DATABASE_URL); the
// in-memory mock engine mishandles joins after deletes.
func sqliteListDB(t *testing.T) {
	t.Helper()
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	pool, err := conn.DB()
	require.NoError(t, err)
	all := []any{&models.User{}, &models.Account{}, &models.AccountExtra{}, &models.List{}, &models.ListAccount{}, &models.ListFollowAccount{}, &models.Follow{}, &models.Tag{}, &models.ListHashtag{}}
	if conn.Dialector.Name() == "sqlite" {
		pool.SetMaxOpenConns(1)
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
	testDB = conn
	database.SetTestDB(conn)
	t.Cleanup(func() { _ = pool.Close() })
}

func TestListTagsAddListRemoveAndLimit(t *testing.T) {
	sqliteListDB(t)
	owner := createTestAccount(t, createTestUser(t).Id)
	other := createTestAccount(t, createTestUser(t).Id)
	list := createTestList(t, owner.Id)

	code, tags := callTags(t, AddListTags, http.MethodPost, list, owner, "#Godot", "godot", "gamedev")
	require.Equal(t, 200, code)
	require.Equal(t, []string{"Godot", "gamedev"}, tags)

	code, _ = callTags(t, AddListTags, http.MethodPost, list, other, "x")
	require.Equal(t, 403, code)

	code, tags = callTags(t, GetListTags, http.MethodGet, list, owner)
	require.Equal(t, 200, code)
	require.Equal(t, []string{"Godot", "gamedev"}, tags)

	code, tags = callTags(t, RemoveListTags, http.MethodDelete, list, owner, "GODOT")
	require.Equal(t, 200, code)
	require.Equal(t, []string{"gamedev"}, tags)

	many := []string{}
	for i := 0; i < models.MaxListHashtags; i++ {
		many = append(many, fmt.Sprintf("t%d", i))
	}
	code, _ = callTags(t, AddListTags, http.MethodPost, list, owner, many...)
	require.Equal(t, 422, code)
	code, _ = callTags(t, AddListTags, http.MethodPost, list, owner, "!!!")
	require.Equal(t, 422, code)
}
