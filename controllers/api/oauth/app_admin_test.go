package oauth

import (
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

func seedApp(t *testing.T, env *oauthTestEnv, name, clientID string) *OAuthApplication {
	t.Helper()
	app := &OAuthApplication{Application: models.Application{Name: name, Website: "https://w.example", RedirectUri: "urn:ietf:wg:oauth:2.0:oob"}, ClientId: clientID, ClientSecret: "secret"}
	require.NoError(t, env.gdb.Create(app).Error)

	return app
}

func TestDeleteApplicationRemovesTokensCodesAndGrants(t *testing.T) {
	env := newOAuthTestEnv(t)
	qa := seedApp(t, env, "qa-prefs-check", "cid-qa")
	other := seedApp(t, env, "keep-me", "cid-keep")
	exp := time.Now().Add(time.Hour)
	revoked := time.Now()
	for i, app := range []*OAuthApplication{qa, qa, other} {
		tok := &models.AuthToken{Token: "t" + string(rune('a'+i)), Type: models.AuthTokenTypeOauth, UserId: 1, ApplicationId: app.Id, ExpiredAt: &exp}
		if i == 1 {
			tok.Revoke = &revoked
		}
		require.NoError(t, env.gdb.Create(tok).Error)
	}
	for _, app := range []*OAuthApplication{qa, other} {
		require.NoError(t, env.gdb.Create(&OAuthAuthorizationCode{Code: "code" + app.ClientId, ApplicationId: app.Id, AccountId: 1, RedirectUri: "x", Scopes: "read", ExpiresAt: &exp}).Error)
		require.NoError(t, env.gdb.Create(&models.ApplicationUserAuth{ApplicationId: app.Id, UserId: 1}).Error)
	}

	list, err := ListApplications(env.gdb)
	require.NoError(t, err)
	require.Len(t, list, 2)
	require.EqualValues(t, 1, list[0].Tokens, "keep-me: one active token")
	require.EqualValues(t, 1, list[1].Tokens, "qa app: one active, one revoked token")

	n, err := DeleteApplication(env.gdb, qa.Id)
	require.NoError(t, err)
	require.EqualValues(t, 2, n)

	var count int64
	require.NoError(t, env.gdb.Model(&models.AuthToken{}).Where("application_id = ?", qa.Id).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, env.gdb.Model(&OAuthAuthorizationCode{}).Where("application_id = ?", qa.Id).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, env.gdb.Model(&models.ApplicationUserAuth{}).Where("application_id = ?", qa.Id).Count(&count).Error)
	require.Zero(t, count)
	var found OAuthApplication
	require.Error(t, env.gdb.First(&found, qa.Id).Error, "the application can no longer be loaded")

	// The other application and its data are untouched.
	require.NoError(t, env.gdb.First(&found, other.Id).Error)
	require.NoError(t, env.gdb.Model(&models.AuthToken{}).Where("application_id = ?", other.Id).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, env.gdb.Model(&OAuthAuthorizationCode{}).Where("application_id = ?", other.Id).Count(&count).Error)
	require.EqualValues(t, 1, count)

	list, err = ListApplications(env.gdb)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "keep-me", list[0].Name)
	require.EqualValues(t, 1, list[0].Tokens)

	_, err = DeleteApplication(env.gdb, qa.Id)
	require.ErrorIs(t, err, ErrAppNotFound)
	_, err = DeleteApplication(env.gdb, 9999)
	require.ErrorIs(t, err, ErrAppNotFound)
}
