package account

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/kasuganosora/community/cache"
	"github.com/kasuganosora/community/tests/pgtest"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	cRedis "github.com/kasuganosora/community/db/redis"
	"github.com/kasuganosora/community/models"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestUpdateCredentialsDisplayNameJSONAndFormPersist(t *testing.T) {
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, conn.AutoMigrate(&models.Account{}, &models.AccountExtra{}, &models.Emoji{}))
	previousDB := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(previousDB) })
	accountCacheRedis.once.Do(func() {
		accountCacheRedis.server, accountCacheRedis.err = miniredis.Run()
		if accountCacheRedis.err == nil {
			accountCacheRedis.client = redis.NewClient(&redis.Options{Addr: accountCacheRedis.server.Addr()})
		}
	})
	require.NoError(t, accountCacheRedis.err)
	previousRedis := cRedis.GetRedisesForTest()
	cRedis.SetRedisesForTest([]*redis.Client{accountCacheRedis.client})
	t.Cleanup(func() { cRedis.SetRedisesForTest(previousRedis) })
	a := &models.Account{Id: 808, Username: "profile_emoji", ImportedNoteHTML: "<p>Imported bio</p>", Avatar: "https://example.com/avatar.png", Header: "https://example.com/header.png"}
	require.NoError(t, conn.Create(a).Error)
	var multipartBody bytes.Buffer
	writer := multipart.NewWriter(&multipartBody)
	require.NoError(t, writer.WriteField("display_name", "Multipart :wave:"))
	require.NoError(t, writer.WriteField("note", "Multipart bio"))
	require.NoError(t, writer.Close())
	cases := []struct{ contentType, body, wantName, wantNote string }{
		{"application/json", `{"display_name":"JSON :wave:","note":"JSON bio","hide_collections":true}`, "JSON :wave:", "JSON bio"},
		{"application/x-www-form-urlencoded", "display_name=Form+%3Awave%3A&note=Form+bio", "Form :wave:", "Form bio"},
		{writer.FormDataContentType(), multipartBody.String(), "Multipart :wave:", "Multipart bio"},
	}
	for _, tc := range cases {
		t.Run(tc.wantName, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("PATCH", "/api/v1/accounts/update_credentials", strings.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", tc.contentType)
			c.Set("account", a)
			cacheKey := fmt.Sprintf(cache.KeyAccountByAcct, fmt.Sprint(a.Id))
			require.NoError(t, cache.GetCache().Set(context.Background(), cacheKey, map[string]any{"display_name": "stale"}, cache.DefaultExpiration))
			UpdateCredentials(c)
			var stale map[string]any
			require.ErrorIs(t, cache.GetCache().Get(context.Background(), cacheKey, &stale), cache.ErrCacheMiss)
			require.Equal(t, 200, w.Code, w.Body.String())
			var body map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			require.Equal(t, tc.wantName, body["display_name"])
			var saved models.Account
			require.NoError(t, conn.First(&saved, a.Id).Error)
			require.Equal(t, tc.wantName, saved.DisplayName)
			require.Equal(t, tc.wantNote, saved.Note)
			require.Empty(t, saved.ImportedNoteHTML)
			require.Equal(t, a.Avatar, saved.Avatar)
			require.Equal(t, a.Header, saved.Header)
		})
	}
}
func TestUpdateCredentialsMalformedPayloadStopsBeforeMutation(t *testing.T) {
	for _, body := range []string{`{"display_name":123}`, `{"display_name":"partial",`, `{"locked":"not-a-boolean"}`} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("PATCH", "/", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		// No account/DB exists: malformed input must stop before accessing either.
		UpdateCredentials(c)
		require.Equal(t, 400, w.Code)
	}
}
