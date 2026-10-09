package favourite

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestCollectionPaginationUsesRelationIDs(t *testing.T) {
	for _, kind := range []string{"favourites", "bookmarks"} {
		t.Run(kind, func(t *testing.T) {
			db, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			t.Cleanup(func() { _ = sqlDB.Close() })
			for _, model := range allTestModels {
				stmt := &gorm.Statement{DB: db}
				require.NoError(t, stmt.Parse(model))
				for _, field := range stmt.Schema.Fields {
					if field.AutoIncrement {
						field.DataType = schema.Int
					}
				}
			}
			require.NoError(t, db.AutoMigrate(allTestModels...))
			database.SetTestDB(db)
			t.Cleanup(database.ResetTestDB)
			fix := testutil.NewFixtureBuilder(t, db)
			account := createAccountWithUser(t, fix, "pagination")
			for i, id := range []uint64{100, 300, 200} {
				status := fix.CreateStatus(account, func(s *models.Status) { s.Id = id })
				if kind == "favourites" {
					require.NoError(t, db.Create(&models.FavoriteStatus{Id: uint64(11 + i), AccountId: account.Id, StatusId: status.Id}).Error)
				} else {
					require.NoError(t, db.Create(&models.BookmarkStatus{Id: uint64(11 + i), AccountId: account.Id, StatusId: status.Id}).Error)
				}
			}
			handler := Favourites
			if kind == "bookmarks" {
				handler = Bookmarks
			}
			request := func(path string) ([]string, string) {
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequest("GET", path, nil)
				c.Set("account", account)
				handler(c)
				require.Empty(t, c.Errors)
				require.Equal(t, 200, w.Code)
				var rows []struct {
					ID string `json:"id"`
				}
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rows))
				ids := make([]string, 0, len(rows))
				for _, row := range rows {
					ids = append(ids, row.ID)
				}
				return ids, w.Header().Get("Link")
			}
			ids, link := request("/api/v1/" + kind + "?limit=2")
			require.Equal(t, []string{"200", "300"}, ids)
			require.Contains(t, link, `rel="next"`)
			next, err := url.Parse(strings.Split(strings.TrimPrefix(link, "<"), ">")[0])
			require.NoError(t, err)
			require.Equal(t, "12", next.Query().Get("max_id"))
			ids, link = request(next.String())
			require.Equal(t, []string{"100"}, ids)
			require.NotContains(t, link, `rel="next"`)
		})
	}
}
