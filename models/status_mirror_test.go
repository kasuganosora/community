package models

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func mirrorDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, conn.AutoMigrate(&Account{}, &Status{}))
	return conn.Session(&gorm.Session{SkipHooks: true})
}

func mirrorAccount(t *testing.T, conn *gorm.DB, id uint64, username string, domain *string) {
	t.Helper()
	now := time.Now()
	acct := username
	if domain != nil {
		acct = username + "@" + *domain
	}
	require.NoError(t, conn.Create(&Account{
		Id: id, Username: username, Domain: domain, Acct: acct, DisplayName: username, CreatedAt: &now, UpdatedAt: &now,
	}).Error)
}

func mirrorStatus(t *testing.T, conn *gorm.DB, status *Status) {
	t.Helper()
	now := time.Now()
	if status.CreatedAt == nil {
		status.CreatedAt = &now
	}
	if status.UpdatedAt == nil {
		status.UpdatedAt = &now
	}
	require.NoError(t, conn.Create(status).Error)
}

func TestMirrorLocalIDsCollapsesOriginAndReblogOfLocalStatus(t *testing.T) {
	conn := mirrorDB(t)
	base := "https://social.example"
	localDomain := "social.example"
	remoteDomain := "other.example"
	mirrorAccount(t, conn, 1, "kanna", nil)
	mirrorAccount(t, conn, 2, "kanna", &localDomain)
	mirrorAccount(t, conn, 3, "bob", &remoteDomain)
	note := base + "/notes/local-note"
	mirrorStatus(t, conn, &Status{Id: 15, AccountId: 1, LocalObjectURI: note, Visibility: StatusVisibilityPublic})
	origin := base + "/users/kanna/statuses/15"
	web := base + "/@kanna/15"
	webStatus := base + "/@kanna/status/15"
	mirrorStatus(t, conn, &Status{Id: 23, AccountId: 2, Domain: &localDomain, OriginUri: &origin, Visibility: StatusVisibilityPublic})
	mirrorStatus(t, conn, &Status{Id: 24, AccountId: 2, Domain: &localDomain, OriginUrl: &web, Visibility: StatusVisibilityPublic})
	mirrorStatus(t, conn, &Status{Id: 25, AccountId: 2, Domain: &localDomain, OriginUri: &webStatus, Visibility: StatusVisibilityPublic})
	noteCopy := note
	mirrorStatus(t, conn, &Status{Id: 26, AccountId: 2, Domain: &localDomain, OriginUri: &noteCopy, Visibility: StatusVisibilityPublic})
	announce := "https://other.example/users/kanna/statuses/90"
	reblogID := uint64(15)
	mirrorStatus(t, conn, &Status{Id: 27, AccountId: 2, Domain: &localDomain, OriginUri: &announce, ReblogId: &reblogID, Visibility: StatusVisibilityPublic})
	bobAnnounce := "https://other.example/users/bob/statuses/9"
	mirrorStatus(t, conn, &Status{Id: 28, AccountId: 3, Domain: &remoteDomain, OriginUri: &bobAnnounce, ReblogId: &reblogID, Visibility: StatusVisibilityPublic})
	replyOrigin := "https://other.example/users/bob/statuses/10"
	parent := uint64(15)
	mirrorStatus(t, conn, &Status{Id: 29, AccountId: 3, Domain: &remoteDomain, OriginUri: &replyOrigin, InReplyToId: &parent, Visibility: StatusVisibilityPublic})

	got, err := MirrorLocalIDs(conn, []uint64{15, 23, 24, 25, 26, 27, 28, 29}, base)
	require.NoError(t, err)
	require.Equal(t, map[uint64]uint64{23: 15, 24: 15, 25: 15, 26: 15, 27: 15}, got)
}

func TestMirrorLocalIDsIgnoresEmptyInput(t *testing.T) {
	got, err := MirrorLocalIDs(nil, nil, "https://social.example")
	require.NoError(t, err)
	require.Empty(t, got)
}
