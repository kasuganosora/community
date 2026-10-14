package discovery

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPrivacyAcceptanceGenericAndOffDoNotReadPrivateRankingSources(t *testing.T) {
	feedTestRedis(t)
	conn, viewer := feedTestDatabase(t)
	ctx := context.Background()
	require.NoError(t, RefreshFeedCandidates(ctx))
	// Dropping the personal-ranking sources makes an accidental read fail,
	// instead of inferring privacy merely from a coincidentally equal ranking.
	require.NoError(t, conn.Migrator().DropTable("followed_tags", &models.Follow{}))
	page, err := StatusPage(ctx, viewer, "", 20)
	require.NoError(t, err)
	require.Equal(t, models.RecommendationGeneric, page.Preference)
	require.NotEmpty(t, page.Items)
	for _, item := range page.Items {
		if item.Reason != nil {
			require.NotEqual(t, "followed_topic", item.Reason.Code)
		}
	}
	_, err = models.SetRecommendationPreference(conn, viewer.UserId, models.RecommendationPersonalized)
	require.NoError(t, err)
	_, err = StatusPage(ctx, viewer, "", 20)
	require.Error(t, err, "control: personalized really attempts its private ranking source")
	_, err = models.SetRecommendationPreference(conn, viewer.UserId, models.RecommendationOff)
	require.NoError(t, err)
	require.NoError(t, conn.Migrator().DropTable(&models.Status{}, &models.StatusContent{}))
	page, err = StatusPage(ctx, viewer, "", 20)
	require.NoError(t, err)
	require.True(t, page.Disabled)
	require.Empty(t, page.Items)
	require.Nil(t, page.NextCursor)
}

func TestPrivacyAcceptanceOldSnapshotReadsCurrentRulesAndBindsViewer(t *testing.T) {
	feedTestRedis(t)
	conn, viewer := feedTestDatabase(t)
	ctx := context.Background()
	now := time.Now().UTC()
	generation, err := feedGeneration()
	require.NoError(t, err)
	snapshot := Snapshot{Generation: generation, UserID: viewer.Id, Preference: models.RecommendationGeneric, Algorithm: AlgorithmVersion, Mode: "rules", CreatedAt: now, ExpiresAt: now.Add(SnapshotTTL), Blocks: [][]RankedItem{{{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4}}}}
	_, err = saveSnapshot(ctx, snapshot)
	require.NoError(t, err)
	cursor, err := encodeFeedCursor(feedCursor{UserID: viewer.Id, Generation: generation, Algorithm: AlgorithmVersion, Expires: snapshot.ExpiresAt.Unix()})
	require.NoError(t, err)
	page, err := StatusPage(ctx, viewer, cursor, 20)
	require.NoError(t, err)
	require.Len(t, page.Items, 4)
	other := &models.Account{Id: 2, UserId: 2}
	_, err = StatusPage(ctx, other, cursor, 20)
	require.ErrorIs(t, err, ErrResetRequired)
	require.NoError(t, conn.Model(&models.Status{}).Where("id = ?", 1).UpdateColumn("visibility", models.StatusVisibilityDirect).Error)
	_, err = models.PutSiteRecommendationFeedback(conn, "status", "2")
	require.NoError(t, err)
	// ID 3 belongs to author 5 in the fixture.
	require.NoError(t, conn.Model(&models.Account{}).Where("id = ?", 5).UpdateColumn("discoverable", false).Error)
	page, err = StatusPage(ctx, viewer, cursor, 20)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.EqualValues(t, 4, page.Items[0].Status.Id)
	_, err = models.PutRecommendationFeedback(conn, viewer.Id, "hide_status", "status", "4")
	require.NoError(t, err)
	_, err = StatusPage(ctx, viewer, cursor, 20)
	require.ErrorIs(t, err, ErrResetRequired, "personal feedback invalidates the old preference revision")
}

func TestPrivacyAcceptanceMeiliExtractionDoesNotExportPrivateObjects(t *testing.T) {
	conn, _ := feedTestDatabase(t)
	now := time.Now().UTC()
	updates := map[uint64]map[string]any{
		1: {"visibility": models.StatusVisibilityPrivate},
		2: {"visibility": models.StatusVisibilityDirect},
		3: {"visibility": models.StatusVisibilityUnlisted},
		4: {"deleted_at": now},
		5: {"scheduled_at": now.Add(time.Hour)},
		6: {"properties": `{"backfilled":"1"}`},
		7: {"created_at": now.Add(time.Hour)},
		8: {"quote_id": 1},
		9: {"created_at": now.Add(-8 * 24 * time.Hour)},
	}
	for id, patch := range updates {
		require.NoError(t, conn.Model(&models.Status{}).Where("id = ?", id).UpdateColumns(patch).Error)
		require.NoError(t, conn.Model(&models.StatusContent{}).Where("status_id = ?", id).UpdateColumn("content", "privacy-acceptance-do-not-export-"+strconv.FormatUint(id, 10)).Error)
	}
	p := &MeiliProvider{conn: func() *gorm.DB { return conn }}
	documents, err := p.documents(context.Background(), now)
	require.NoError(t, err)
	require.NotEmpty(t, documents, "the test must include a positive public control")
	for _, document := range documents {
		id, err := strconv.ParseUint(document.ID, 10, 64)
		require.NoError(t, err)
		_, excluded := updates[id]
		require.False(t, excluded, "private/hidden root or private quote entered the search document set")
		encoded, err := json.Marshal(document)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), "privacy-acceptance-do-not-export")
		for _, disallowed := range []string{"access_token", "password", "matrix", "bookmark", "followed_tags", "email"} {
			require.False(t, strings.Contains(string(encoded), disallowed))
		}
	}
	// Shared index build must fail closed if the policy store is unavailable.
	require.NoError(t, conn.Migrator().DropTable(&models.RecommendationFeedback{}))
	documents, err = p.documents(context.Background(), now)
	require.Error(t, err)
	require.Empty(t, documents)
}

func TestPrivacyAcceptanceDerivedInterestsUseOnlyAllowedPublicActivity(t *testing.T) {
	conn, viewer := feedTestDatabase(t)
	now := time.Now().UTC()
	created := now.Add(-time.Minute)
	names := []string{"a_private", "b_direct", "c_unlisted", "d_noindex", "e_hidden", "f_privatequote", "g_expiredactivity", "h_otheruser", "publiclike", "publicreaction"}
	write := conn.Session(&gorm.Session{SkipHooks: true})
	for i, name := range names {
		id := uint64(i + 1)
		require.NoError(t, write.Create(&models.Tag{Id: id, Name: name, NameKey: name}).Error)
		require.NoError(t, write.Create(&models.StatusTag{StatusId: id, TagId: id}).Error)
		when, actor := created, viewer.Id
		if id == 7 {
			when = now.Add(-31 * 24 * time.Hour)
		}
		if id == 8 {
			actor = 2
		}
		if id == 10 {
			require.NoError(t, write.Create(&models.StatusReaction{AccountID: actor, StatusID: id, Name: "👍", CreatedAt: when}).Error)
		} else {
			require.NoError(t, write.Create(&models.FavoriteStatus{AccountId: actor, StatusId: id, CreatedAt: &when}).Error)
		}
	}
	for id, visibility := range map[uint64]uint8{1: models.StatusVisibilityPrivate, 2: models.StatusVisibilityDirect, 3: models.StatusVisibilityUnlisted} {
		require.NoError(t, conn.Model(&models.Status{}).Where("id = ?", id).UpdateColumn("visibility", visibility).Error)
	}
	require.NoError(t, conn.Model(&models.Account{}).Where("id = ?", 6).UpdateColumn("no_index", true).Error)
	require.NoError(t, conn.Model(&models.StatusContent{}).Where("status_id = ?", 5).UpdateColumn("content", "privacy-derived-hidden-marker").Error)
	require.NoError(t, conn.Create(&models.Filter{AccountId: viewer.Id, Context: []string{"home"}, FilterAction: models.FilterActionHide, Keywords: []*models.FilterKeyword{{Keyword: "privacy-derived-hidden-marker"}}}).Error)
	require.NoError(t, conn.Model(&models.Status{}).Where("id = ?", 6).UpdateColumn("quote_id", 1).Error)
	topics, err := feedInterestTopics(conn, viewer, now)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"publiclike", "publicreaction"}, topics)
}

func TestPrivacyAcceptanceSafetyPolicyRevokesOldWindowsPermanently(t *testing.T) {
	feedTestRedis(t)
	_, viewer := feedTestDatabase(t)
	ctx := context.Background()
	policy, enabled := "approved-v1:epoch1", true
	cfg := feedConfig()
	cfg.Policy = func(context.Context) (string, error) { return policy, nil }
	cfg.Enabled = func() bool { return enabled }
	Configure(cfg)
	require.NoError(t, RefreshFeedCandidates(ctx))
	page, err := StatusPage(ctx, viewer, "", 1)
	require.NoError(t, err)
	require.NotNil(t, page.NextCursor)
	cursor := *page.NextCursor
	enabled, policy = false, "disabled:epoch2"
	page, err = StatusPage(ctx, viewer, cursor, 1)
	require.NoError(t, err)
	require.True(t, page.Disabled)
	enabled, policy = true, "approved-v1:epoch3"
	_, err = StatusPage(ctx, viewer, cursor, 1)
	require.ErrorIs(t, err, ErrResetRequired, "re-enabling must not resurrect a window invalidated by a safety stop")
}

func TestPrivacyAcceptanceRealMeiliContainsOnlyPublicDocument(t *testing.T) {
	host := strings.TrimRight(os.Getenv("DISCOVERY_TEST_MEILI_URL"), "/")
	if host == "" {
		t.Skip("set DISCOVERY_TEST_MEILI_URL to an isolated Meilisearch instance")
	}
	p, _ := meiliTestProvider(t, host)
	conn := meiliSQLFixture(t)
	p.conn = func() *gorm.DB { return conn }
	require.NoError(t, conn.Model(&models.StatusContent{}).Where("status_id <> ?", 1).UpdateColumn("content", "privacy-acceptance-engine-secret").Error)
	created := time.Now().UTC().Add(-time.Minute)
	privateID := uint64(3)
	write := conn.Session(&gorm.Session{SkipHooks: true})
	require.NoError(t, write.Omit("Content").Create(&models.Status{Id: 5, AccountId: 1, Visibility: models.StatusVisibilityPublic, QuoteId: &privateID, CreatedAt: &created}).Error)
	require.NoError(t, write.Create(&models.StatusContent{StatusId: 5, Content: "privacy-acceptance-private-quote"}).Error)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		state, _ := p.readState(cleanup)
		indexes := []string{state.Pending, state.Retired}
		if state.Active != nil {
			indexes = append(indexes, state.Active.Index)
		}
		for _, index := range indexes {
			if index != "" {
				_ = p.retire(cleanup, index)
			}
		}
	})
	require.NoError(t, p.rebuild(ctx))
	state, err := p.readState(ctx)
	require.NoError(t, err)
	require.NotNil(t, state.Active)
	var response struct {
		Results []meiliDocument `json:"results"`
		Total   int             `json:"total"`
	}
	require.NoError(t, p.http(ctx, http.MethodGet, "/indexes/"+state.Active.Index+"/documents?limit=100", nil, &response))
	require.Equal(t, 1, response.Total)
	require.Len(t, response.Results, 1)
	require.Equal(t, "1", response.Results[0].ID)
	encoded, err := json.Marshal(response)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "privacy-acceptance")
}
