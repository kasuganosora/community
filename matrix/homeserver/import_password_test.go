package homeserver

import (
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"testing"
)

func TestImportedPasswordAccountServiceVerification(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sql, err := conn.DB()
	require.NoError(t, err)
	sql.SetMaxOpenConns(1)
	t.Cleanup(func() { sql.Close() })
	previous := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(previous) })
	require.NoError(t, conn.AutoMigrate(&models.User{}, &models.Account{}, &models.UserSecretData{}))
	raw := conn.Session(&gorm.Session{SkipHooks: true})
	user := models.User{UUID: "import-user", UserName: "imported"}
	require.NoError(t, raw.Create(&user).Error)
	require.NoError(t, raw.Create(&models.Account{Username: "imported", UserId: user.Id}).Error)
	hash, err := bcrypt.GenerateFromPassword([]byte("source-password"), bcrypt.MinCost)
	require.NoError(t, err)
	require.NoError(t, raw.Create(&models.UserSecretData{UserId: user.Id, Password: string(hash), PasswordScheme: "bcrypt", Salt: "encryption-only"}).Error)
	service := &AccountService{}
	require.NoError(t, service.verifyUserPassword("@imported:example.test", "source-password"))
	require.Error(t, service.verifyUserPassword("@imported:example.test", "wrong-password"))
}
