package feed

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestHiddenMirrorIDsDropsMirrorOnlyWhenLocalStatusIsOnTimeline(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, conn.AutoMigrate(&models.Account{}, &models.Status{}))
	write := conn.Session(&gorm.Session{SkipHooks: true})
	now := time.Now()
	domain := "localhost"
	require.NoError(t, write.Create(&models.Account{Id: 1, Username: "kanna", Acct: "kanna", DisplayName: "kanna", CreatedAt: &now, UpdatedAt: &now}).Error)
	require.NoError(t, write.Create(&models.Account{Id: 2, Username: "kanna", Domain: &domain, Acct: "kanna@localhost", DisplayName: "kanna", CreatedAt: &now, UpdatedAt: &now}).Error)
	require.NoError(t, write.Create(&models.Status{Id: 15, AccountId: 1, Visibility: models.StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now}).Error)
	origin := "http://localhost/users/kanna/statuses/15"
	require.NoError(t, write.Create(&models.Status{Id: 23, AccountId: 2, Domain: &domain, OriginUri: &origin, Visibility: models.StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now}).Error)

	allow := func(tx *gorm.DB) (*gorm.DB, error) { return tx, nil }
	hidden, err := hiddenMirrorIDs(write, []uint64{15, 23}, allow)
	require.NoError(t, err)
	require.Equal(t, map[uint64]bool{23: true}, hidden)

	excludeLocal := func(tx *gorm.DB) (*gorm.DB, error) {
		return tx.Where("statuses.id <> ?", uint64(15)), nil
	}
	hidden, err = hiddenMirrorIDs(write, []uint64{23}, excludeLocal)
	require.NoError(t, err)
	require.Empty(t, hidden)
}
