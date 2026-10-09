package admin

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers/api/oauth"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAdminDeleteOAuthApplication(t *testing.T) {
	db, admin := operationsDB(t)
	require.NoError(t, db.AutoMigrate(&oauth.OAuthApplication{}, &oauth.OAuthAuthorizationCode{}, &models.AuthToken{}, &models.ApplicationUserAuth{}, &models.MisskeyAuthSession{}))
	app := &oauth.OAuthApplication{Application: models.Application{Name: "qa-prefs-check", Website: "https://w", RedirectUri: "urn:ietf:wg:oauth:2.0:oob"}, ClientId: "cid", ClientSecret: "s"}
	require.NoError(t, db.Create(app).Error)
	exp := time.Now().Add(time.Hour)
	require.NoError(t, db.Create(&models.AuthToken{Token: "tok", Type: models.AuthTokenTypeOauth, UserId: 1, ApplicationId: app.Id}).Error)
	require.NoError(t, db.Create(&oauth.OAuthAuthorizationCode{Code: "c", ApplicationId: app.Id, AccountId: 1, RedirectUri: "x", Scopes: "read", ExpiresAt: &exp}).Error)

	// Listing never exposes secrets.
	c, w := newGetContext(t, nil, "")
	c.Set("account", admin)
	ListOAuthApplications(c)
	require.Equal(t, 200, w.Code)
	require.NotContains(t, w.Body.String(), "secret")
	var list []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &list))
	require.Len(t, list, 1)
	require.Equal(t, "qa-prefs-check", list[0]["name"])

	// Anonymous and unprivileged callers are refused and nothing is deleted.
	c, w = newJSONContext(t, "DELETE", gin.Params{{Key: "id", Value: "1"}}, nil)
	DeleteOAuthApplication(c)
	require.Equal(t, 401, w.Code)
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&models.User{Id: 2, UUID: "u2", UserName: "plain", EmailHash: "plain", Status: models.UserStatusNormal}).Error)
	plain := &models.Account{Id: 2, UserId: 2, Username: "plain"}
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(plain).Error)
	c, w = newJSONContext(t, "DELETE", gin.Params{{Key: "id", Value: "1"}}, nil)
	c.Set("account", plain)
	DeleteOAuthApplication(c)
	require.Equal(t, 403, w.Code)
	var n int64
	require.NoError(t, db.Model(&models.AuthToken{}).Count(&n).Error)
	require.EqualValues(t, 1, n)

	for _, bad := range []string{"abc", "0", "999"} {
		c, w = newJSONContext(t, "DELETE", gin.Params{{Key: "id", Value: bad}}, nil)
		c.Set("account", admin)
		DeleteOAuthApplication(c)
		require.Equal(t, 404, w.Code, bad)
	}

	c, w = newJSONContext(t, "DELETE", gin.Params{{Key: "id", Value: "1"}}, nil)
	c.Set("account", admin)
	DeleteOAuthApplication(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.JSONEq(t, `{"id":"1","deleted_tokens":1}`, w.Body.String())
	require.NoError(t, db.Model(&models.AuthToken{}).Count(&n).Error)
	require.Zero(t, n)
	require.NoError(t, db.Model(&oauth.OAuthAuthorizationCode{}).Count(&n).Error)
	require.Zero(t, n)

	c, w = newJSONContext(t, "DELETE", gin.Params{{Key: "id", Value: "1"}}, nil)
	c.Set("account", admin)
	DeleteOAuthApplication(c)
	require.Equal(t, 404, w.Code)
}
