package account

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/color"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/cache"
	mediactl "github.com/kasuganosora/community/controllers/api/media"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestLegacyProfileImageEchoAndMultipartSave is the credential compatibility
// contract. An unchanged avatar or header value is kept, including a legacy
// URL that is not a local /media attachment. A different value still has to
// be an owned, ready image. The multipart case calls PUT /api/v1/accounts/me,
// which binds file parts named avatar and header.
func TestLegacyProfileImageEchoAndMultipartSave(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	orig, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(orig) })

	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{
		SkipDefaultTransaction:                   true,
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, conn.AutoMigrate(
		&models.Account{}, &models.AccountExtra{}, &models.Emoji{}, &models.User{},
		&models.MediaAttachment{}, &models.MediaProcessTask{}, &models.MediaQueueGate{},
		&models.DomainBlock{},
	))
	previous := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(previous) })
	useAccountCache(t)

	const (
		legacyAvatar       = "http://localhost:8080/legacy/avatar.webp"
		legacyStatic       = "http://localhost:8080/legacy/avatar-static.jpg"
		legacyHeader       = "http://localhost:8080/legacy/header.webp"
		legacyHeaderStatic = "http://localhost:8080/legacy/header-static.jpg"
		staleAvatar        = "http://localhost:8080/legacy/stale-avatar.webp"
		staleStatic        = "http://localhost:8080/legacy/stale-static.jpg"
		staleHeader        = "http://localhost:8080/legacy/stale-header.webp"
		staleHeaderStatic  = "http://localhost:8080/legacy/stale-header-static.jpg"
	)
	user := &models.User{UserName: "legacy-owner", EmailHash: "legacy-owner-hash", Status: models.UserStatusNormal}
	require.NoError(t, conn.Create(user).Error)
	owner := &models.Account{
		Id: 42, Username: "legacy-owner", UserId: user.Id, DisplayName: "before",
		Avatar: legacyAvatar, AvatarStatic: legacyStatic, Header: legacyHeader, HeaderStatic: legacyHeaderStatic,
	}
	other := &models.Account{Id: 43, Username: "legacy-other"}
	require.NoError(t, conn.Create(owner).Error)
	require.NoError(t, conn.Create(other).Error)

	stale := false
	router := gin.New()
	router.Use(func(c *gin.Context) {
		id := uint64(42)
		if c.GetHeader("X-Fixture-Owner") == "43" {
			id = 43
		}
		var row models.Account
		require.NoError(t, conn.First(&row, id).Error)
		if stale && id == owner.Id {
			row.DisplayName = "stale-name"
			row.Avatar = staleAvatar
			row.AvatarStatic = staleStatic
			row.Header = staleHeader
			row.HeaderStatic = staleHeaderStatic
		}
		c.Set("authenticated", true)
		c.Set("account", &row)
		c.Set("user", &models.User{Id: user.Id})
		c.Next()
	})
	router.POST("/api/v1/media", mediactl.Upload)
	router.PATCH("/api/v1/accounts/update_credentials", UpdateCredentials)
	router.PUT("/api/v1/accounts/me", UpdateAccount)
	router.GET("/api/v1/accounts/:id", GetAccount)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	call := func(t *testing.T, method, path, contentType string, body []byte, ownerID string) (int, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, bytes.NewReader(body))
		require.NoError(t, err)
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		req.Header.Set("X-Fixture-Owner", ownerID)
		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = res.Body.Close() }()
		data, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		require.NoError(t, err)
		return res.StatusCode, data
	}
	patch := func(t *testing.T, fields map[string]any) (int, map[string]any) {
		t.Helper()
		payload, err := json.Marshal(fields)
		require.NoError(t, err)
		status, data := call(t, http.MethodPatch, "/api/v1/accounts/update_credentials", "application/json", payload, "42")
		var body map[string]any
		_ = json.Unmarshal(data, &body)
		return status, body
	}
	upload := func(t *testing.T, payload []byte, name, contentType, ownerID string) string {
		t.Helper()
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", `form-data; name="file"; filename="`+name+`"`)
		header.Set("Content-Type", contentType)
		part, err := writer.CreatePart(header)
		require.NoError(t, err)
		_, err = part.Write(payload)
		require.NoError(t, err)
		require.NoError(t, writer.Close())
		status, data := call(t, http.MethodPost, "/api/v1/media", writer.FormDataContentType(), buf.Bytes(), ownerID)
		require.Equal(t, http.StatusOK, status, string(data))
		var resp struct {
			URL string `json:"url"`
		}
		require.NoError(t, json.Unmarshal(data, &resp))
		require.NotEmpty(t, resp.URL)
		return resp.URL
	}

	t.Run("echo legacy avatar and header", func(t *testing.T) {
		before := reloadAccount(t, conn, owner.Id)
		status, body := patch(t, map[string]any{
			"display_name": "kept-images",
			"avatar":       before.Avatar,
			"header":       before.Header,
		})
		require.Equal(t, http.StatusOK, status, fmt.Sprint(body))
		require.Equal(t, legacyAvatar, body["avatar"])
		require.Equal(t, legacyStatic, body["avatar_static"])
		require.Equal(t, legacyHeader, body["header"])
		require.Equal(t, legacyHeaderStatic, body["header_static"])
		saved := reloadAccount(t, conn, owner.Id)
		require.Equal(t, "kept-images", saved.DisplayName)
		require.Equal(t, legacyAvatar, saved.Avatar)
		require.Equal(t, legacyStatic, saved.AvatarStatic)
		require.Equal(t, legacyHeader, saved.Header)
		require.Equal(t, legacyHeaderStatic, saved.HeaderStatic)
	})

	owned := upload(t, pngBytes(t, color.RGBA{R: 30, G: 90, B: 200, A: 255}), "replacement.png", "image/png", "42")
	foreign := upload(t, pngBytes(t, color.RGBA{R: 200, G: 90, B: 30, A: 255}), "foreign.png", "image/png", "43")

	t.Run("reject external replacement", func(t *testing.T) {
		before := reloadAccount(t, conn, owner.Id)
		status, _ := patch(t, map[string]any{
			"display_name": "must-not-commit",
			"avatar":       "https://files.example/not-owned.png",
			"header":       before.Header,
		})
		require.Equal(t, http.StatusUnprocessableEntity, status)
		saved := reloadAccount(t, conn, owner.Id)
		require.Equal(t, before.DisplayName, saved.DisplayName)
		require.Equal(t, before.Avatar, saved.Avatar)
		require.Equal(t, before.AvatarStatic, saved.AvatarStatic)
		require.Equal(t, before.Header, saved.Header)
		require.Equal(t, before.HeaderStatic, saved.HeaderStatic)
	})

	t.Run("reject foreign media", func(t *testing.T) {
		before := reloadAccount(t, conn, owner.Id)
		status, _ := patch(t, map[string]any{"display_name": "must-not-commit", "avatar": foreign})
		require.Equal(t, http.StatusUnprocessableEntity, status)
		saved := reloadAccount(t, conn, owner.Id)
		require.Equal(t, before.DisplayName, saved.DisplayName)
		require.Equal(t, before.Avatar, saved.Avatar)
		require.Equal(t, before.AvatarStatic, saved.AvatarStatic)
	})

	t.Run("stale context does not rewrite the saved pair", func(t *testing.T) {
		require.NoError(t, conn.Model(&models.Account{}).Where("id = ?", owner.Id).Updates(map[string]any{
			"avatar": owned, "avatar_static": owned + "#still",
			"header": legacyHeader, "header_static": legacyHeaderStatic,
			"display_name": "current",
		}).Error)
		cacheKey := fmt.Sprintf(cache.KeyAccountByAcct, "42")
		require.NoError(t, cache.GetCache().Set(t.Context(), cacheKey, &models.Account{
			Id: owner.Id, Avatar: staleAvatar, AvatarStatic: staleStatic, Header: staleHeader, HeaderStatic: staleHeaderStatic, DisplayName: "stale-name",
		}, cache.DefaultExpiration))
		stale = true
		t.Cleanup(func() { stale = false })

		status, body := patch(t, map[string]any{"display_name": "after-echo", "avatar": owned, "header": legacyHeader})
		require.Equal(t, http.StatusOK, status, fmt.Sprint(body))
		saved := reloadAccount(t, conn, owner.Id)
		require.Equal(t, "after-echo", saved.DisplayName)
		require.Equal(t, owned, saved.Avatar)
		require.Equal(t, owned+"#still", saved.AvatarStatic)
		require.Equal(t, legacyHeader, saved.Header)
		require.Equal(t, legacyHeaderStatic, saved.HeaderStatic)
		var cached models.Account
		require.ErrorIs(t, cache.GetCache().Get(t.Context(), cacheKey, &cached), cache.ErrCacheMiss)

		status, _ = patch(t, map[string]any{"display_name": "must-not-commit", "avatar": staleAvatar})
		require.Equal(t, http.StatusUnprocessableEntity, status)
		saved = reloadAccount(t, conn, owner.Id)
		require.Equal(t, "after-echo", saved.DisplayName)
		require.Equal(t, owned, saved.Avatar)
		require.Equal(t, owned+"#still", saved.AvatarStatic)
		stale = false

		statusGet, data := call(t, http.MethodGet, "/api/v1/accounts/42", "", nil, "42")
		require.Equal(t, http.StatusOK, statusGet, string(data))
		var got map[string]any
		require.NoError(t, json.Unmarshal(data, &got))
		require.Equal(t, owned, got["avatar"])
		require.Equal(t, owned+"#still", got["avatar_static"])
		require.Equal(t, legacyHeader, got["header"])
		require.Equal(t, legacyHeaderStatic, got["header_static"])
	})

	t.Run("multipart file parts update both images", func(t *testing.T) {
		require.NoError(t, conn.Model(&models.Account{}).Where("id = ?", owner.Id).Updates(map[string]any{
			"avatar": legacyAvatar, "avatar_static": legacyStatic,
			"header": legacyHeader, "header_static": legacyHeaderStatic,
		}).Error)
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)
		for _, part := range []struct {
			name  string
			pixel color.RGBA
		}{
			{name: "avatar", pixel: color.RGBA{R: 180, G: 40, B: 20, A: 255}},
			{name: "header", pixel: color.RGBA{R: 20, G: 40, B: 180, A: 255}},
		} {
			header := make(textproto.MIMEHeader)
			header.Set("Content-Disposition", `form-data; name="`+part.name+`"; filename="legacy.png"`)
			header.Set("Content-Type", "image/png")
			file, err := writer.CreatePart(header)
			require.NoError(t, err)
			_, err = file.Write(pngBytes(t, part.pixel))
			require.NoError(t, err)
		}
		require.NoError(t, writer.WriteField("display_name", "multipart-saved"))
		require.NoError(t, writer.Close())
		status, data := call(t, http.MethodPut, "/api/v1/accounts/me", writer.FormDataContentType(), buf.Bytes(), "42")
		require.Equal(t, http.StatusOK, status, string(data))
		var envelope struct {
			Account map[string]any `json:"account"`
		}
		require.NoError(t, json.Unmarshal(data, &envelope))
		saved := reloadAccount(t, conn, owner.Id)
		require.Equal(t, "multipart-saved", saved.DisplayName)
		require.NotEqual(t, legacyAvatar, saved.Avatar)
		require.NotEqual(t, legacyHeader, saved.Header)
		require.Equal(t, saved.Avatar, saved.AvatarStatic)
		require.Equal(t, saved.Header, saved.HeaderStatic)
		require.Contains(t, saved.Avatar, "/media/")
		require.Contains(t, saved.Header, "/media/")
		require.Equal(t, saved.Avatar, envelope.Account["avatar"])
		require.Equal(t, saved.AvatarStatic, envelope.Account["avatar_static"])
		require.Equal(t, saved.Header, envelope.Account["header"])
		require.Equal(t, saved.HeaderStatic, envelope.Account["header_static"])

		status, data = call(t, http.MethodGet, "/api/v1/accounts/42", "", nil, "42")
		require.Equal(t, http.StatusOK, status, string(data))
		var got map[string]any
		require.NoError(t, json.Unmarshal(data, &got))
		require.Equal(t, saved.Avatar, got["avatar"])
		require.Equal(t, saved.AvatarStatic, got["avatar_static"])
		require.Equal(t, saved.Header, got["header"])
		require.Equal(t, saved.HeaderStatic, got["header_static"])
	})
}
