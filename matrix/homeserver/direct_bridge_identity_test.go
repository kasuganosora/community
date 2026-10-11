package homeserver

import (
	"strings"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

func TestBridgeAccountIDReadableAndCollisionSafe(t *testing.T) {
	hs, _ := directBridgeDB(t)
	domain := "maid.lat"
	a := models.Account{Id: 1, Username: "luna", Domain: &domain}
	require.Equal(t, "@_ap_luna_maid_lat=1:test.local", hs.bridgeAccountID(a))
	require.Equal(t, "@luna:test.local", hs.bridgeAccountID(models.Account{Username: "luna"}))
	a.Username = "a.b"
	b := a
	b.Id = 2
	b.Username = "a_b"
	require.NotEqual(t, hs.bridgeAccountID(a), hs.bridgeAccountID(b))
	a.Username = strings.Repeat("露娜", 150)
	id := hs.bridgeAccountID(a)
	require.LessOrEqual(t, len(id), 255)
	local, _, ok := ParseMatrixUserID(id)
	require.True(t, ok)
	require.True(t, validLocalpartRe.MatchString(local))
	require.Error(t, models.ValidateUsername("_ap_luna"))
	require.Error(t, models.ValidateUsername("_AP_luna"))
	_, registrationErr := (&AuthService{HS: hs}).Register(&RegisterRequest{Username: "_ap_luna=1", Password: "unused-password"}, false)
	require.Equal(t, ErrInvalidUsername, registrationErr)
}

func TestResolveBridgeAccountOnlyAcceptsCanonicalRemoteIdentity(t *testing.T) {
	hs, conn := directBridgeDB(t)
	seedDirectBridge(t, conn, 310, 2)
	var remote models.Account
	require.NoError(t, conn.First(&remote, 1).Error)
	id := hs.bridgeAccountID(remote)
	account, err := hs.resolveBridgeAccount(conn, id)
	require.NoError(t, err)
	require.Equal(t, remote.Id, account.Id)
	for _, invalid := range []string{
		"@_ap_forged=1:test.local",
		"@_ap_remote_remote_example=2:test.local", // A local account cannot be impersonated.
		"@_ap_remote_remote_example=0:test.local",
		"@_ap_remote_remote_example=zzzz:test.local",
		"@_ap_invalid:test.local",
	} {
		account, err := hs.resolveBridgeAccount(conn, invalid)
		require.Error(t, err, invalid)
		require.Nil(t, account)
	}
	for _, ordinary := range []string{"@alice:test.local", "@_ap_remote_remote_example=1:elsewhere.example"} {
		account, err := hs.resolveBridgeAccount(conn, ordinary)
		require.NoError(t, err)
		require.Nil(t, account)
	}
	require.NoError(t, conn.Model(&models.Account{}).Where("id = ?", remote.Id).Update("is_ban", true).Error)
	_, err = hs.resolveBridgeAccount(conn, id)
	require.Error(t, err)
}
