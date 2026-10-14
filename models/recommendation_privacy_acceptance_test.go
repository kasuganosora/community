package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// These cases deliberately pass stale public models back to the recommendation
// boundary after changing SQL. No cache flag may authorize the old model.
func TestPrivacyAcceptanceStatusVisibilityMatrix(t *testing.T) {
	cases := []struct {
		name  string
		patch func(time.Time) map[string]any
	}{
		{"unlisted", func(time.Time) map[string]any { return map[string]any{"visibility": StatusVisibilityUnlisted} }},
		{"private", func(time.Time) map[string]any { return map[string]any{"visibility": StatusVisibilityPrivate} }},
		{"direct", func(time.Time) map[string]any { return map[string]any{"visibility": StatusVisibilityDirect} }},
		{"deleted", func(now time.Time) map[string]any { return map[string]any{"deleted_at": now} }},
		{"scheduled", func(now time.Time) map[string]any { return map[string]any{"scheduled_at": now.Add(time.Hour)} }},
		{"backfill", func(time.Time) map[string]any { return map[string]any{"properties": `{"backfilled":"1"}`} }},
		{"future", func(now time.Time) map[string]any { return map[string]any{"created_at": now.Add(time.Second)} }},
		{"old_root", func(now time.Time) map[string]any { return map[string]any{"created_at": now.Add(-8 * 24 * time.Hour)} }},
		{"reply", func(time.Time) map[string]any { return map[string]any{"in_reply_to_id": 999} }},
		{"boost_shell", func(time.Time) map[string]any { return map[string]any{"reblog_id": 999} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := recommendationTestDB(t)
			viewer := recommendationTestAccount(t, conn, 1)
			recommendationTestAccount(t, conn, 2)
			now := time.Now().UTC()
			root := recommendationTestStatus(t, conn, 10, 2, now.Add(-time.Hour))
			opts := RecommendationEligibilityOptions{Context: "home", AsOf: now, RootWindow: true}
			got, err := FilterRecommendableStatuses(conn, viewer, Statuses{root}, opts)
			require.NoError(t, err)
			require.Len(t, got, 1)
			require.NoError(t, conn.Model(&Status{}).Where("id = ?", root.Id).UpdateColumns(tc.patch(now)).Error)
			for _, reader := range []*Account{nil, viewer} {
				got, err = FilterRecommendableStatuses(conn, reader, Statuses{root}, opts)
				require.NoError(t, err)
				require.Empty(t, got, "the old public model must not override current visibility")
			}
		})
	}
}

func TestPrivacyAcceptanceBlocksMutesAndLocalOnly(t *testing.T) {
	cases := []struct {
		name   string
		deny   bool
		change func(*testing.T, *gorm.DB, time.Time)
	}{
		{"outgoing_block", true, func(t *testing.T, db *gorm.DB, _ time.Time) {
			require.NoError(t, db.Create(&Block{AccountId: 1, TargetAccountId: 2, Type: BlockTypeAccount}).Error)
		}},
		{"incoming_block", true, func(t *testing.T, db *gorm.DB, _ time.Time) {
			require.NoError(t, db.Create(&Block{AccountId: 2, TargetAccountId: 1, Type: BlockTypeAccount}).Error)
		}},
		{"site_block", true, func(t *testing.T, db *gorm.DB, _ time.Time) {
			require.NoError(t, db.Create(&Block{AccountId: 0, TargetAccountId: 2, Type: BlockTypeAccount}).Error)
		}},
		{"permanent_mute", true, func(t *testing.T, db *gorm.DB, _ time.Time) {
			require.NoError(t, db.Create(&MuteAccount{AccountId: 1, TargetId: 2}).Error)
		}},
		{"temporary_mute", true, func(t *testing.T, db *gorm.DB, now time.Time) {
			until := now.Add(time.Hour)
			require.NoError(t, db.Create(&MuteAccount{AccountId: 1, TargetId: 2, ExpiredAt: &until}).Error)
		}},
		{"expired_mute", false, func(t *testing.T, db *gorm.DB, now time.Time) {
			until := now.Add(-time.Hour)
			require.NoError(t, db.Create(&MuteAccount{AccountId: 1, TargetId: 2, ExpiredAt: &until}).Error)
		}},
		{"status_mute", true, func(t *testing.T, db *gorm.DB, _ time.Time) {
			require.NoError(t, db.Create(&MuteStatus{AccountId: 1, StatusId: 10}).Error)
		}},
		{"discoverability_revoke", true, func(t *testing.T, db *gorm.DB, _ time.Time) {
			require.NoError(t, db.Model(&Account{}).Where("id = ?", 2).UpdateColumn("discoverable", false).Error)
		}},
		{"noindex_revoke", true, func(t *testing.T, db *gorm.DB, _ time.Time) {
			require.NoError(t, db.Model(&Account{}).Where("id = ?", 2).UpdateColumn("no_index", true).Error)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := recommendationTestDB(t)
			viewer := recommendationTestAccount(t, conn, 1)
			recommendationTestAccount(t, conn, 2)
			now := time.Now().UTC()
			root := recommendationTestStatus(t, conn, 10, 2, now.Add(-time.Hour))
			tc.change(t, conn, now)
			got, err := FilterRecommendableStatuses(conn, viewer, Statuses{root}, RecommendationEligibilityOptions{Context: "home", AsOf: now, RootWindow: true})
			require.NoError(t, err)
			require.Equal(t, !tc.deny, len(got) == 1)
		})
	}
	t.Run("local_only_preserves_existing_boundary", func(t *testing.T) {
		conn := recommendationTestDB(t)
		viewer := recommendationTestAccount(t, conn, 1)
		recommendationTestAccount(t, conn, 2)
		now := time.Now().UTC()
		root := recommendationTestStatus(t, conn, 10, 2, now.Add(-time.Hour))
		require.NoError(t, conn.Model(&Status{}).Where("id = ?", root.Id).UpdateColumn("local_only", true).Error)
		opts := RecommendationEligibilityOptions{Context: "public", AsOf: now, RootWindow: true}
		got, err := FilterRecommendableStatuses(conn, viewer, Statuses{root}, opts)
		require.NoError(t, err)
		require.Len(t, got, 1)
		domain := "outside.invalid"
		viewer.Domain = &domain
		got, err = FilterRecommendableStatuses(conn, viewer, Statuses{root}, opts)
		require.NoError(t, err)
		require.Empty(t, got)
	})
}

func TestPrivacyAcceptanceNestedQuoteWholeCardRevocation(t *testing.T) {
	conn := recommendationTestDB(t)
	viewer := recommendationTestAccount(t, conn, 1)
	recommendationTestAccount(t, conn, 2)
	now := time.Now().UTC()
	root := recommendationTestStatus(t, conn, 10, 2, now.Add(-time.Hour))
	middle := recommendationTestStatus(t, conn, 11, 2, now.Add(-time.Hour))
	secret := recommendationTestStatus(t, conn, 12, 2, now.Add(-30*24*time.Hour))
	require.NoError(t, conn.Model(&Status{}).Where("id = ?", root.Id).UpdateColumn("quote_id", middle.Id).Error)
	require.NoError(t, conn.Model(&Status{}).Where("id = ?", middle.Id).UpdateColumn("quote_id", secret.Id).Error)
	require.NoError(t, conn.Model(&StatusContent{}).Where("status_id = ?", secret.Id).UpdateColumn("content", "privacy-acceptance-nested-secret").Error)
	for _, contextName := range []string{"home", "public"} {
		opts := RecommendationEligibilityOptions{Context: contextName, AsOf: now, RootWindow: true}
		got, err := FilterRecommendableStatuses(conn, viewer, Statuses{root}, opts)
		require.NoError(t, err)
		require.Len(t, got, 1, "old quote is allowed while the root is fresh")
		filter := &Filter{AccountId: viewer.Id, Context: []string{contextName}, FilterAction: FilterActionHide, Keywords: []*FilterKeyword{{Keyword: "nested-secret"}}}
		require.NoError(t, conn.Create(filter).Error)
		got, err = FilterRecommendableStatuses(conn, viewer, Statuses{root}, opts)
		require.NoError(t, err)
		require.Empty(t, got)
		require.NoError(t, conn.Delete(filter).Error)
	}
	for _, visibility := range []uint8{StatusVisibilityUnlisted, StatusVisibilityPrivate, StatusVisibilityDirect} {
		require.NoError(t, conn.Model(&Status{}).Where("id = ?", secret.Id).UpdateColumn("visibility", visibility).Error)
		got, err := FilterRecommendableStatuses(conn, viewer, Statuses{root}, RecommendationEligibilityOptions{Context: "home", AsOf: now, RootWindow: true})
		require.NoError(t, err)
		require.Empty(t, got, "nested body and all attached media/link/poll containers disappear with the whole card")
	}
}
