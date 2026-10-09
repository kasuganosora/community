package activitypub

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestNodeInfoDocuments(t *testing.T) {
	gin.SetMode(gin.TestMode)

	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, conn.AutoMigrate(&models.Emoji{}, &models.Account{}, &models.AccountExtra{}, &models.Status{}))
	previous := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(previous) })
	resetNodeInfoCache()
	t.Cleanup(resetNodeInfoCache)

	prevURL, prevConfigured := config.C.AppBaseURL, config.AppBaseURLConfigured()
	config.SetAppBaseURL("https://social.example", true)
	t.Cleanup(func() { config.SetAppBaseURL(prevURL, prevConfigured) })
	systemsettings.Set(systemsettings.KeyTitle, "Hime")
	systemsettings.Set(systemsettings.KeyIsEnableRegistration, true)
	t.Cleanup(func() {
		systemsettings.Set(systemsettings.KeyTitle, "")
		systemsettings.Set(systemsettings.KeyIsEnableRegistration, false)
	})

	now := time.Now()
	recent, older, ancient := now.AddDate(0, 0, -3), now.AddDate(0, 0, -90), now.AddDate(-2, 0, 0)
	remote := "remote.example"
	accounts := []*models.Account{
		{Id: 1, UserId: 11, Username: "active", LastStatusAt: &recent},
		{Id: 2, UserId: 12, Username: "halfyear", LastActivityAt: &older},
		{Id: 3, UserId: 13, Username: "dormant", LastActivityAt: &ancient},
		{Id: 4, Username: proxyUsername, LastActivityAt: &recent}, // service actor, no user
		{Id: 5, Username: "bob", Domain: &remote, LastStatusAt: &recent},
	}
	for _, a := range accounts {
		require.NoError(t, conn.Create(a).Error)
	}
	for i, owner := range []uint64{1, 1, 2, 5} {
		s := &models.Status{Id: uint64(100 + i), AccountId: owner}
		if owner == 5 {
			s.Domain = &remote
		}
		require.NoError(t, conn.Create(s).Error)
	}

	router := gin.New()
	router.GET("/.well-known/nodeinfo", NodeInfo)
	router.GET("/nodeinfo/2.0", NodeInfo20)
	router.GET("/nodeinfo/2.1", NodeInfo21)

	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))

		return w
	}

	w := get("/.well-known/nodeinfo")
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Header().Get("Content-Type"), "application/json")
	require.Contains(t, w.Body.String(), `"https://social.example/nodeinfo/2.1"`)
	require.Contains(t, w.Body.String(), `"https://social.example/nodeinfo/2.0"`)

	for _, version := range []string{"2.0", "2.1"} {
		w := get("/nodeinfo/" + version)
		require.Equal(t, 200, w.Code, version)
		require.Equal(t, `application/json; profile="http://nodeinfo.diaspora.software/ns/schema/`+version+`#"`, w.Header().Get("Content-Type"))

		var doc struct {
			Version  string `json:"version"`
			Software struct {
				Name, Version, Repository string
			} `json:"software"`
			Protocols         []string       `json:"protocols"`
			Services          map[string]any `json:"services"`
			OpenRegistrations bool           `json:"openRegistrations"`
			Usage             struct {
				Users struct {
					Total, ActiveMonth, ActiveHalfyear int64
				} `json:"users"`
				LocalPosts int64 `json:"localPosts"`
			} `json:"usage"`
			Metadata map[string]any `json:"metadata"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &doc), w.Body.String())
		require.Equal(t, version, doc.Version)
		require.Equal(t, "community", doc.Software.Name)
		require.Equal(t, config.GetVersion(), doc.Software.Version)
		if version == "2.1" {
			require.NotEmpty(t, doc.Software.Repository)
		}
		require.Equal(t, []string{"activitypub"}, doc.Protocols)
		require.Contains(t, doc.Services, "inbound")
		require.True(t, doc.OpenRegistrations)
		require.EqualValues(t, 3, doc.Usage.Users.Total)
		require.EqualValues(t, 1, doc.Usage.Users.ActiveMonth)
		require.EqualValues(t, 2, doc.Usage.Users.ActiveHalfyear)
		require.EqualValues(t, 3, doc.Usage.LocalPosts)
		require.Equal(t, "Hime", doc.Metadata["nodeName"])
	}
}
