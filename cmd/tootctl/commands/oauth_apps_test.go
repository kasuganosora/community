package commands

import (
	"bytes"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/controllers/api/oauth"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestOAuthAppsCommand(t *testing.T) {
	// The sqlexec test driver double-encodes ApplicationScope; use plain SQLite.
	conn, err := pgtest.GormOpen(t, sqlite.Open("file:tootctl_oauth_apps?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	require.NoError(t, conn.AutoMigrate(&oauth.OAuthApplication{}, &oauth.OAuthAuthorizationCode{}, &models.AuthToken{}, &models.ApplicationUserAuth{}, &models.MisskeyAuthSession{}))
	database.SetTestDB(conn)
	t.Cleanup(database.ResetTestDB)
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		app := &cli.App{Writer: &out, Commands: []*cli.Command{OAuthCommand()}}
		err := app.Run(append([]string{"tootctl", "oauth", "apps"}, args...))

		return out.String(), err
	}
	app := &oauth.OAuthApplication{Application: models.Application{Name: "qa-prefs-check", Website: "https://w", RedirectUri: "urn:ietf:wg:oauth:2.0:oob"}, ClientId: "cid-qa", ClientSecret: "TOPSECRET"}
	require.NoError(t, conn.Create(app).Error)
	exp := time.Now().Add(time.Hour)
	require.NoError(t, conn.Create(&models.AuthToken{Token: "tk", Type: models.AuthTokenTypeOauth, UserId: 1, ApplicationId: app.Id}).Error)
	require.NoError(t, conn.Create(&oauth.OAuthAuthorizationCode{Code: "cd", ApplicationId: app.Id, AccountId: 1, RedirectUri: "x", Scopes: "read", ExpiresAt: &exp}).Error)

	out, err := run("list")
	require.NoError(t, err)
	require.Contains(t, out, "qa-prefs-check")
	require.Contains(t, out, "cid-qa")
	require.NotContains(t, out, "TOPSECRET")

	for _, args := range [][]string{{"remove"}, {"remove", "abc"}, {"remove", "0"}, {"remove", "1", "2"}} {
		_, err = run(args...)
		require.Error(t, err, args)
	}
	_, err = run("remove", "999")
	require.ErrorIs(t, err, oauth.ErrAppNotFound)

	out, err = run("remove", "1")
	require.NoError(t, err)
	require.Contains(t, out, "Removed application 1 and 1 tokens")
	var n int64
	require.NoError(t, conn.Model(&models.AuthToken{}).Count(&n).Error)
	require.Zero(t, n)
	require.NoError(t, conn.Model(&oauth.OAuthAuthorizationCode{}).Count(&n).Error)
	require.Zero(t, n)
	out, err = run("list")
	require.NoError(t, err)
	require.NotContains(t, out, "qa-prefs-check")
}
