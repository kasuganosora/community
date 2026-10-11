package homeserver

import (
	"context"
	"strings"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestBridgeAccountIDReadableWithoutPrefixOrNumber(t *testing.T) {
	hs, conn := directBridgeDB(t)
	domain := "maid.lat"
	a := models.Account{Id: 1, Username: "luna", Domain: &domain}
	require.Equal(t, "@luna_maid_lat_ap:test.local", hs.bridgeAccountID(a))
	require.Error(t, models.ValidateUsername("luna_ap"))
	require.Error(t, models.ValidateUsername("luna_AP"))
	_, registrationErr := hs.Auth.Register(&RegisterRequest{Username: "luna_ap", Password: "unused-password"}, false)
	require.Equal(t, ErrInvalidUsername, registrationErr)
	require.Equal(t, "@luna:test.local", hs.bridgeAccountID(models.Account{Username: "luna"}))
	a.Username = strings.Repeat("露娜", 150)
	require.Error(t, hs.validateBridgeAccountIdentity(conn, a), "overlong names must not be truncated into another identity")
}

func TestResolveBridgeAccountOnlyAcceptsCanonicalRemoteIdentity(t *testing.T) {
	hs, conn := directBridgeDB(t)
	var remote models.Account
	require.NoError(t, conn.First(&remote, 1).Error)
	id := hs.bridgeAccountID(remote)
	require.Equal(t, "@remote_remote_example_ap:test.local", id)
	account, err := hs.resolveBridgeAccount(conn, id)
	require.NoError(t, err)
	require.Equal(t, remote.Id, account.Id)
	for _, ordinary := range []string{"@alice:test.local", "@_ap_remote_remote_example=1:test.local", "@remote_remote_example_ap:elsewhere.example", "@unknown:test.local"} {
		account, err := hs.resolveBridgeAccount(conn, ordinary)
		require.NoError(t, err)
		require.Nil(t, account)
	}
	missing, missingErr := hs.resolveBridgeAccount(conn, "@missing_remote_example_ap:test.local")
	require.Nil(t, missing)
	require.ErrorIs(t, missingErr, errUnknownBridgeAccount)
	require.NoError(t, conn.Model(&models.Account{}).Where("id = ?", remote.Id).Update("is_ban", true).Error)
	_, err = hs.resolveBridgeAccount(conn, id)
	require.Error(t, err)
}

func TestBridgeAccountReadableIdentityRejectsAmbiguity(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(map[bool]string{false: "remote", true: "local"}[local], func(t *testing.T) {
			hs, conn := directBridgeDB(t)
			domain := "remote.example"
			a := models.Account{Id: 10, Username: "a.b", Domain: &domain}
			b := models.Account{Id: 11, Username: "a_b", Domain: &domain}
			if local {
				b.Username = "a_b_remote_example_ap"
				b.Domain = nil
			}
			for _, v := range []*models.Account{&a, &b} {
				v.MatrixAlias = models.NormalizedMatrixAlias(*v)
				require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(v).Error)
			}
			_, err := hs.resolveBridgeAccount(conn, hs.bridgeAccountID(a))
			require.ErrorIs(t, err, models.ErrMatrixAliasCollision)
			require.ErrorIs(t, hs.validateBridgeAccountIdentity(conn, a), models.ErrMatrixAliasCollision)
		})
	}
}

func TestBridgeAliasBackfillAndAccountSave(t *testing.T) {
	hs, conn := directBridgeDB(t)
	require.NoError(t, conn.Model(&models.Account{}).Where("id = ?", 1).UpdateColumn("matrix_alias", "").Error)
	require.NoError(t, models.BackfillMatrixAliases(conn))
	require.NoError(t, models.BackfillMatrixAliases(conn))
	account, err := hs.resolveBridgeAccount(conn, "@remote_remote_example_ap:test.local")
	require.NoError(t, err)
	require.NotNil(t, account)
	account.Username = "renamed"
	require.NoError(t, account.BeforeSave(conn))
	require.Equal(t, "renamed_remote_example_ap", account.MatrixAlias)
}

func TestCollidingReadableAliasCannotEnterExistingDirectRoom(t *testing.T) {
	hs, conn := directBridgeDB(t)
	seedDirectBridge(t, conn, 701, 2)
	require.NoError(t, hs.ProcessDirectMessage(context.Background(), 701))
	domain := "example"
	collision := models.Account{Id: 88, Username: "remote_remote", Domain: &domain}
	collision.MatrixAlias = models.NormalizedMatrixAlias(collision)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Omit("Extra").Create(&collision).Error)
	created, me := hs.Room.CreateRoom("@alice:test.local", &CreateRoomRequest{IsDirect: true, Invite: []string{"@remote_remote_example_ap:test.local"}})
	require.Nil(t, created)
	require.NotNil(t, me)
	seedDirectBridge(t, conn, 702, 2)
	_ = hs.ProcessDirectMessage(context.Background(), 702)
	var delivery models.MatrixDMDelivery
	require.NoError(t, conn.First(&delivery, "status_id = ?", 702).Error)
	require.Equal(t, "skipped", delivery.State)
	require.Empty(t, delivery.EventID)
}
