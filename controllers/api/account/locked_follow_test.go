package account

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/controllers/api/follow_request"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestLockedAccountFollowApprovalLifecycle(t *testing.T) {
	db, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	modelList := []any{&models.User{}, &models.UserSetting{}, &models.UserSecretData{}, &models.Account{}, &models.AccountExtra{}, &models.Follow{}}
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
	requester := &models.Account{Id: 10, Username: "requester"}
	locked := &models.Account{Id: 20, Username: "locked", Locked: true}
	require.NoError(t, db.Create(requester).Error)
	require.NoError(t, db.Create(locked).Error)
	call := func(handler gin.HandlerFunc, actor *models.Account, id string) (int, map[string]any) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/", nil)
		c.Params = gin.Params{{Key: "id", Value: id}}
		c.Set("account", actor)
		handler(c)
		require.Empty(t, c.Errors)
		var response map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		return w.Code, response
	}
	counts := func(expected int) {
		var from, to models.AccountExtra
		require.NoError(t, db.First(&from, "account_id = ?", requester.Id).Error)
		require.NoError(t, db.First(&to, "account_id = ?", locked.Id).Error)
		require.EqualValues(t, expected, from.FollowingCount)
		require.EqualValues(t, expected, to.FollowersCount)
	}
	code, response := call(Follow, requester, "20")
	require.Equal(t, 200, code)
	require.Equal(t, true, response["requested"])
	require.Equal(t, false, response["following"])
	counts(0)
	code, response = call(Follow, requester, "20")
	require.Equal(t, 200, code)
	require.Equal(t, true, response["requested"])
	counts(0)
	code, _ = call(follow_request.Reject, locked, "10")
	require.Equal(t, 200, code)
	counts(0)
	code, _ = call(Follow, requester, "20")
	require.Equal(t, 200, code)
	code, response = call(follow_request.Authorize, locked, "10")
	require.Equal(t, 200, code)
	require.Equal(t, true, response["followed_by"])
	require.Equal(t, false, response["following"])
	counts(1)
	code, _ = call(follow_request.Authorize, locked, "10")
	require.Equal(t, 404, code)
	counts(1)
	code, response = call(Follow, requester, "20")
	require.Equal(t, 200, code)
	require.Equal(t, true, response["following"])
	require.Equal(t, false, response["requested"])
	counts(1)
	code, _ = call(UnFollow, requester, "20")
	require.Equal(t, 200, code)
	counts(0)
}
