package oauth

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/twofactor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Mastodon refuses the password grant for 2FA accounts with invalid_grant;
// our otp_attempt extension lets a client that knows the code through.
func TestToken_PasswordGrant_TwoFactor(t *testing.T) {
	env := newOAuthTestEnv(t)
	app := createTestApp(t, env, "TestApp", "https://example.com/cb")
	user := env.fix.CreateUser(func(u *models.User) { u.UserName = "tfuser" })
	hash, err := models.EncryptPassword("correctpassword", "s")
	require.NoError(t, err)
	require.NoError(t, env.gdb.Create(&models.UserSecretData{UserId: user.Id, Salt: "s", Password: hash}).Error)
	require.NoError(t, env.gdb.Create(&models.Account{UserId: user.Id, Acct: "tfuser"}).Error)
	require.NoError(t, twofactor.ImportSecret(env.gdb, user.Id, "JBSWY3DPEHPK3PXP", []string{"backup-one"}))

	grant := func(otp string) (int, string) {
		form := url.Values{}
		form.Set("grant_type", "password")
		form.Set("client_id", app.ClientId)
		form.Set("client_secret", app.ClientSecret)
		form.Set("username", user.UserName)
		form.Set("password", "correctpassword")
		form.Set("scope", "read")
		if otp != "" {
			form.Set("otp_attempt", otp)
		}
		c, w := newFormContext(t, http.MethodPost, form)
		Token(c)
		return w.Code, w.Body.String()
	}

	code, body := grant("")
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, body, "invalid_grant")
	assert.NotContains(t, body, "access_token")

	code, _ = grant("000000")
	assert.Equal(t, http.StatusBadRequest, code)

	totp, _ := twofactor.Generate("JBSWY3DPEHPK3PXP", time.Now())
	code, body = grant(totp)
	assert.Equal(t, http.StatusOK, code, body)
	code, _ = grant(totp)
	assert.Equal(t, http.StatusBadRequest, code, "TOTP replay")

	code, _ = grant("backup-one")
	assert.Equal(t, http.StatusOK, code)
	code, _ = grant("backup-one")
	assert.Equal(t, http.StatusBadRequest, code, "backup code reuse")
}
