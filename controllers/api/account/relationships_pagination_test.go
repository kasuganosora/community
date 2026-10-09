package account

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRelationshipPaginationUsesFollowIDs(t *testing.T) {
	for _, kind := range []string{"followers", "following"} {
		t.Run(kind, func(t *testing.T) {
			db, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			t.Cleanup(func() { _ = sqlDB.Close() })
			require.NoError(t, db.AutoMigrate(&models.Account{}, &models.AccountExtra{}, &models.Follow{}, &models.Emoji{}))
			database.SetTestDB(db)
			t.Cleanup(database.ResetTestDB)
			for _, id := range []uint64{1, 100, 300, 200} {
				require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&models.Account{Id: id, Username: "person"}).Error)
			}
			for i, id := range []uint64{100, 300, 200} {
				follow := &models.Follow{Id: uint64(11 + i), AccountId: 1, FollowId: id}
				if kind == "followers" {
					follow.AccountId = id
					follow.FollowId = 1
				}
				require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(follow).Error)
			}
			handler := Followers
			if kind == "following" {
				handler = Following
			}
			request := func(path string) ([]string, string) {
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequest("GET", path, nil)
				c.Params = gin.Params{{Key: "id", Value: "1"}}
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
			ids, link := request("/api/v1/accounts/1/" + kind + "?limit=2")
			require.Equal(t, []string{"200", "300"}, ids)
			require.Contains(t, link, "max_id=12")
			require.Contains(t, link, `rel="next"`)
			next := strings.Split(strings.TrimPrefix(link, "<"), ">")[0]
			ids, link = request(next)
			require.Equal(t, []string{"100"}, ids)
			require.NotContains(t, link, `rel="next"`)
		})
	}
}
