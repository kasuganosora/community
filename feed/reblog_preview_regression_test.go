package feed_test

import (
	"context"
	"testing"
	"time"

	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/feed/home"
	"github.com/kasuganosora/community/feed/public"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTimelineFirstBatchDoesNotDropAllowedReblogBehindBlockedReblog(t *testing.T) {
	run := func(t *testing.T, durableCached bool) {
		conn, client := setupTimelineOrder(t)
		require.NoError(t, conn.AutoMigrate(&models.SystemSetting{}))
		if durableCached {
			require.False(t, feed.ForceDatabaseReads())
		}
		seen := time.Now().UTC()
		viewer := &models.Account{Id: 101, Username: "synth_viewer", FeedLastSeenAt: &seen}
		origAuthor := &models.Account{Id: 102, Username: "synth_orig"}
		allowedBooster := &models.Account{Id: 103, Username: "synth_allowed"}
		blockedBooster := &models.Account{Id: 104, Username: "synth_blocked"}
		otherAuthor := &models.Account{Id: 105, Username: "synth_other"}
		require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create([]*models.Account{
			viewer, origAuthor, allowedBooster, blockedBooster, otherAuthor,
		}).Error)
		require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create([]*models.Follow{
			{AccountId: viewer.Id, FollowId: origAuthor.Id, Status: models.FollowStatusFollowed},
			{AccountId: viewer.Id, FollowId: allowedBooster.Id, Status: models.FollowStatusFollowed},
			{AccountId: viewer.Id, FollowId: blockedBooster.Id, Status: models.FollowStatusFollowed},
			{AccountId: viewer.Id, FollowId: otherAuthor.Id, Status: models.FollowStatusFollowed},
		}).Error)
		require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&models.Block{
			AccountId: viewer.Id, Type: models.BlockTypeAccount, TargetAccountId: blockedBooster.Id,
		}).Error)

		base := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
		origID := uint64(10)
		insertStatus(t, conn, &models.Status{Id: origID, AccountId: origAuthor.Id, Visibility: models.StatusVisibilityPublic}, base)
		insertStatus(t, conn, &models.Status{Id: 11, AccountId: otherAuthor.Id, Visibility: models.StatusVisibilityPublic}, base.Add(time.Minute))
		insertStatus(t, conn, &models.Status{Id: 12, AccountId: otherAuthor.Id, Visibility: models.StatusVisibilityPublic}, base.Add(2*time.Minute))
		insertStatus(t, conn, &models.Status{Id: 13, AccountId: allowedBooster.Id, Visibility: models.StatusVisibilityPublic, ReblogId: &origID}, base.Add(3*time.Minute))
		insertStatus(t, conn, &models.Status{Id: 14, AccountId: blockedBooster.Id, Visibility: models.StatusVisibilityPublic, ReblogId: &origID}, base.Add(4*time.Minute))

		params := func(maxID uint64) *feed.ListParam {
			p := &feed.ListParam{Viewer: viewer, Limit: 2, MaxId: maxID}
			if !durableCached {
				p.Other = map[string]any{"database_only": true}
			}
			return p
		}
		if durableCached {
			for _, tl := range []feed.Timeline{&public.PublicTimeline{}, &home.HomeTimeline{Account: viewer}} {
				key := feed.TimelineKey(tl)
				seedTimeline(t, client, key, map[string]float64{
					"10": float64(base.UnixMicro()),
					"11": float64(base.Add(time.Minute).UnixMicro()),
					"12": float64(base.Add(2 * time.Minute).UnixMicro()),
					"13": float64(base.Add(3 * time.Minute).UnixMicro()),
					"14": float64(base.Add(4 * time.Minute).UnixMicro()),
				}, feed.TimelineRankCreatedAt)
				require.NoError(t, client.HSet(context.Background(), key+":meta", "complete", "1").Err())
			}
		}

		pubPage1, err := public.GetTimelineContentAll(params(0))
		require.NoError(t, err)
		require.Equal(t, []uint64{13, 12}, pubPage1.GetIds())
		pubPage2, err := public.GetTimelineContentAll(params(pubPage1[len(pubPage1)-1].Id))
		require.NoError(t, err)
		require.Equal(t, []uint64{11, 10}, pubPage2.GetIds())

		homePage1, err := home.GetTimeLineContent(viewer, params(0))
		require.NoError(t, err)
		require.Equal(t, []uint64{13, 12}, homePage1.GetIds())
		homePage2, err := home.GetTimeLineContent(viewer, params(homePage1[len(homePage1)-1].Id))
		require.NoError(t, err)
		require.Equal(t, []uint64{11, 10}, homePage2.GetIds())
	}
	t.Run("sql", func(t *testing.T) { run(t, false) })
	t.Run("cached", func(t *testing.T) { run(t, true) })
}

func TestTimelineForwardMinIdKeepsNearestVisibleReblog(t *testing.T) {
	conn, _ := setupTimelineOrder(t)
	require.NoError(t, conn.AutoMigrate(&models.SystemSetting{}))
	seen := time.Now().UTC()
	viewer := &models.Account{Id: 201, Username: "synth_min_viewer", FeedLastSeenAt: &seen}
	origAuthor := &models.Account{Id: 202, Username: "synth_min_orig"}
	allowedA := &models.Account{Id: 203, Username: "synth_min_allowed_a"}
	allowedB := &models.Account{Id: 204, Username: "synth_min_allowed_b"}
	blocked := &models.Account{Id: 205, Username: "synth_min_blocked"}
	other := &models.Account{Id: 206, Username: "synth_min_other"}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create([]*models.Account{
		viewer, origAuthor, allowedA, allowedB, blocked, other,
	}).Error)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&models.Block{
		AccountId: viewer.Id, Type: models.BlockTypeAccount, TargetAccountId: blocked.Id,
	}).Error)

	t.Run("blocked older boost does not hide the next visible boost", func(t *testing.T) {
		base := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
		origID := uint64(5)
		insertStatus(t, conn, &models.Status{Id: origID, AccountId: origAuthor.Id, Visibility: models.StatusVisibilityPublic}, base)
		insertStatus(t, conn, &models.Status{Id: 10, AccountId: other.Id, Visibility: models.StatusVisibilityPublic}, base.Add(time.Minute))
		insertStatus(t, conn, &models.Status{Id: 20, AccountId: blocked.Id, Visibility: models.StatusVisibilityPublic, ReblogId: &origID}, base.Add(2*time.Minute))
		insertStatus(t, conn, &models.Status{Id: 30, AccountId: allowedA.Id, Visibility: models.StatusVisibilityPublic, ReblogId: &origID}, base.Add(3*time.Minute))
		insertStatus(t, conn, &models.Status{Id: 40, AccountId: other.Id, Visibility: models.StatusVisibilityPublic}, base.Add(4*time.Minute))
		insertStatus(t, conn, &models.Status{Id: 50, AccountId: other.Id, Visibility: models.StatusVisibilityPublic}, base.Add(5*time.Minute))
		got, err := public.GetTimelineContentAll(&feed.ListParam{
			Viewer: viewer, MinId: 10, Limit: 2, Other: map[string]any{"database_only": true},
		})
		require.NoError(t, err)
		require.Equal(t, []uint64{40, 30}, got.GetIds())
	})

	t.Run("ascending page keeps the nearest visible reblog", func(t *testing.T) {
		base := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
		origID := uint64(500)
		insertStatus(t, conn, &models.Status{Id: origID, AccountId: origAuthor.Id, Visibility: models.StatusVisibilityPublic}, base)
		insertStatus(t, conn, &models.Status{Id: 510, AccountId: other.Id, Visibility: models.StatusVisibilityPublic}, base.Add(time.Minute))
		insertStatus(t, conn, &models.Status{Id: 520, AccountId: allowedA.Id, Visibility: models.StatusVisibilityPublic, ReblogId: &origID}, base.Add(2*time.Minute))
		insertStatus(t, conn, &models.Status{Id: 530, AccountId: other.Id, Visibility: models.StatusVisibilityPublic}, base.Add(3*time.Minute))
		insertStatus(t, conn, &models.Status{Id: 540, AccountId: allowedB.Id, Visibility: models.StatusVisibilityPublic, ReblogId: &origID}, base.Add(4*time.Minute))
		got, err := public.GetTimelineContentAll(&feed.ListParam{
			Viewer: viewer, MinId: 510, Limit: 2, Other: map[string]any{"database_only": true},
		})
		require.NoError(t, err)
		require.Equal(t, []uint64{530, 520}, got.GetIds())
		got3, err := public.GetTimelineContentAll(&feed.ListParam{
			Viewer: viewer, MinId: 510, Limit: 3, Other: map[string]any{"database_only": true},
		})
		require.NoError(t, err)
		require.Equal(t, []uint64{530, 520}, got3.GetIds())
	})
}
