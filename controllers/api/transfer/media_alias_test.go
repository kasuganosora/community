package transfer

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/datatransfer"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMediaAliasServingUsesConfiguredOriginAndLocalRedirect(t *testing.T) {
	db := transferDB(t)
	require.NoError(t, db.AutoMigrate(&datatransfer.ImportedMediaAlias{}, &models.MediaAttachment{}))
	old, configured := config.C.AppBaseURL, config.AppBaseURLConfigured()
	config.SetAppBaseURL("https://social.test", true)
	t.Cleanup(func() { config.SetAppBaseURL(old, configured) })
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&models.MediaAttachment{Id: 91, FileName: "random.png", FilePath: "imports/random.png"}).Error)
	for _, p := range []string{"/system/media/original.png", "/files/old.png"} {
		require.NoError(t, datatransfer.RecordMediaAlias(db, datatransfer.Options{Scope: "site", TargetBaseURL: "https://social.test"}, datatransfer.Media{OriginalURL: "https://social.test" + p}, 91))
	}
	router := gin.New()
	router.GET("/system/*file", ServeMediaAlias)
	router.GET("/files/*file", ServeMediaAlias)
	for _, p := range []string{"/system/media/original.png", "/files/old.png"} {
		req := httptest.NewRequest("GET", p, nil)
		req.Host = "attacker.test"
		req.Header.Set("X-Forwarded-Host", "attacker.test")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, 307, w.Code)
		require.Equal(t, "/media/91/random.png", w.Header().Get("Location"))
	}
	for _, p := range []string{"/files/unknown", "/files/old.png?token=secret", "/files/old%2fpng"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		require.Equal(t, 404, w.Code)
		require.Empty(t, w.Header().Get("Location"))
	}
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Where("id = ?", 91).Delete(&models.MediaAttachment{}).Error)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/files/old.png", nil))
	require.Equal(t, 404, w.Code)
}
