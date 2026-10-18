package homeserver

import (
	"testing"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/twofactor"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// m.login.password carries no second factor, so 2FA accounts are refused
// (they use m.login.token from a web session), and UIA password checks fail.
func TestPasswordLoginRefusedWithTwoFactor(t *testing.T) {
	setupMinimalTestDB(t)
	conn := db.GetDB()
	require.NoError(t, conn.AutoMigrate(&models.User{}, &models.Account{}, &models.AccountExtra{}, &models.UserSecretData{}, &models.UserTwoFactor{}, &models.UserBackupCode{}))
	require.NoError(t, conn.Create(&models.User{Id: 5, UUID: "u5", UserName: "qa2", EmailHash: "h5", Status: models.UserStatusNormal}).Error)
	require.NoError(t, conn.Create(&models.Account{UserId: 5, Username: "qa2"}).Error)
	hash, err := models.EncryptPassword("pw-123456", "s")
	require.NoError(t, err)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&models.UserSecretData{UserId: 5, Salt: "s", Password: hash}).Error)
	require.NoError(t, twofactor.ImportSecret(conn, 5, "JBSWY3DPEHPK3PXP", nil))

	auth := NewAuthService(minimalHS("test.local"))
	resp, merr := auth.loginWithPassword(&LoginRequest{Type: "m.login.password", User: "qa2", Password: "pw-123456"})
	require.Nil(t, resp)
	require.NotNil(t, merr)
	require.Equal(t, "M_FORBIDDEN", merr.ErrCode)
	require.False(t, auth.VerifyUserPassword("@qa2:test.local", "pw-123456"))

	require.NoError(t, twofactor.AdminReset(conn, 5))
	require.True(t, auth.VerifyUserPassword("@qa2:test.local", "pw-123456"))
}
