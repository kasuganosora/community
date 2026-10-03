package tag

import (
	"context"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestTagMembershipBoundedSparseDenseAndEmpty(t *testing.T) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()
	conn := setupTestDB(t)
	for _, id := range []uint64{1, 2, 3} {
		require.NoError(t, conn.Create(&models.Tag{Id: id, Name: string(rune('a' + id))}).Error)
	}
	require.NoError(t, conn.Exec("INSERT INTO status_tags(status_id,tag_id) VALUES(1,1)").Error)
	for i := 1; i <= feed.MAX_ITEMS+1; i++ {
		require.NoError(t, conn.Exec("INSERT INTO status_tags(status_id,tag_id) VALUES(?,2)", i).Error)
	}
	for _, tc := range []struct {
		id       uint64
		want     string
		unwanted string
	}{{1, "statuses.id IN", "SELECT 1 FROM status_tags"}, {2, "SELECT 1 FROM status_tags", "statuses.id IN"}, {3, "1 = 0", "SELECT 1 FROM status_tags"}} {
		apply, err := tagMembership(conn, tc.id)
		require.NoError(t, err)
		query := apply(conn.Session(&gorm.Session{DryRun: true}).Model(&models.Status{})).Order("statuses.id DESC").Limit(20).Find(&models.Statuses{})
		require.Contains(t, query.Statement.SQL.String(), tc.want)
		require.NotContains(t, query.Statement.SQL.String(), tc.unwanted)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := tagMembership(conn.WithContext(ctx), 1)
	require.ErrorIs(t, err, context.Canceled)
}
