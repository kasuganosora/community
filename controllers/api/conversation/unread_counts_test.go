package conversation

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestUnreadAnnouncementsCountsActiveUnreadOnly(t *testing.T) {
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, conn.AutoMigrate(&models.Announcement{}, &models.AnnouncementDismissal{}))
	now := time.Now()
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	mk := func(published bool, starts, ends *time.Time) uint64 {
		a := &models.Announcement{Text: "x", Published: published, StartsAt: starts, EndsAt: ends}
		require.NoError(t, conn.Create(a).Error)
		return a.Id
	}
	unread := mk(true, nil, nil)
	read := mk(true, nil, nil)
	editedAfterRead := mk(true, &past, nil)
	mk(false, nil, nil)    // draft
	mk(true, &future, nil) // not started
	mk(true, nil, &past)   // ended
	require.NoError(t, conn.Create(&models.AnnouncementDismissal{AccountID: 7, AnnouncementID: read, ReadAt: now.Add(time.Minute)}).Error)
	require.NoError(t, conn.Create(&models.AnnouncementDismissal{AccountID: 7, AnnouncementID: editedAfterRead, ReadAt: now.Add(-2 * time.Hour)}).Error)
	// Another account's read state must not leak.
	require.NoError(t, conn.Create(&models.AnnouncementDismissal{AccountID: 8, AnnouncementID: unread, ReadAt: now.Add(time.Minute)}).Error)

	n, err := unreadAnnouncements(conn, 7, now)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	n, err = unreadAnnouncements(conn, 8, now)
	require.NoError(t, err)
	require.Equal(t, 2, n)
}
