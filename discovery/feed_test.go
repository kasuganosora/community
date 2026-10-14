package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	appredis "github.com/kasuganosora/community/db/redis"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func feedTestRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr(), ContextTimeoutEnabled: true, MaxRetries: -1})
	var previous redis.UniversalClient
	if appredis.HasUniversalRedis() {
		previous = appredis.GetUniversalRedis()
	}
	appredis.SetUniversalRedis(client)
	prior := feedConfig()
	Configure(Config{Enabled: func() bool { return true }, SigningKeys: [][]byte{[]byte(strings.Repeat("s", 32))}, MaxActiveSnapshots: 256})
	t.Cleanup(func() { Configure(prior); appredis.SetUniversalRedis(previous); _ = client.Close() })
	return mini, client
}

func TestFeedRRFFixedFamilyWeights(t *testing.T) {
	a := []Source{{Family: "lexical", Key: "go", Rank: 1}, {Family: "exact", Key: "go", Rank: 5}}
	first := ReciprocalRankFusion(a)
	a = append(a, Source{Family: "lexical", Key: "duplicate", Rank: 1})
	require.InDelta(t, first, ReciprocalRankFusion(a), 1e-12)
	require.InDelta(t, .4, ReciprocalRankFusion([]Source{{Family: "lexical", Rank: 1}}), 1e-12)
	require.Zero(t, ReciprocalRankFusion([]Source{{Family: "lexical", Rank: 0}, {Family: "unknown", Rank: 1}}))
	require.LessOrEqual(t, ReciprocalRankFusion([]Source{{Family: "lexical", Rank: 1}, {Family: "exact", Rank: 1}, {Family: "semantic", Rank: 1}}), 1.0)
}

func TestFeedLogicalBlocksDiversityAndStableTies(t *testing.T) {
	now := time.Now().UTC()
	candidates := []Candidate{}
	for i := uint64(1); i <= 400; i++ {
		domain := ""
		if i%2 == 0 {
			domain = "remote.example"
		}
		candidates = append(candidates, Candidate{StatusID: i, AuthorID: i%40 + 1, Domain: domain, CreatedAt: now.Add(-time.Hour), Explore: i%3 == 0, Heat: .2})
	}
	a := RankCandidates(candidates, now, false, "stable")
	b := RankCandidates(candidates, now, false, "stable")
	require.Equal(t, a, b)
	require.LessOrEqual(t, len(a), 10)
	seen := map[uint64]bool{}
	remainingExploration := 133
	for _, block := range a {
		require.LessOrEqual(t, len(block), 20)
		authors := map[uint64]int{}
		remote := 0
		explorations := 0
		for _, item := range block {
			require.False(t, seen[item.ID])
			seen[item.ID] = true
			authors[item.ID%40+1]++
			if item.ID%2 == 0 {
				remote++
			}
			if item.ID%3 == 0 {
				explorations++
			}
		}
		for _, count := range authors {
			require.LessOrEqual(t, count, 2)
		}
		require.LessOrEqual(t, remote, 8)
		if remainingExploration >= 4 {
			require.GreaterOrEqual(t, explorations, 4)
		}
		remainingExploration -= explorations
	}
	require.LessOrEqual(t, len(seen), 200)
	future := []Candidate{{StatusID: 1, AuthorID: 1, CreatedAt: now.Add(time.Second)}}
	require.Empty(t, RankCandidates(future, now, false, "stable"))
}

func TestFeedSignedCursorBindingRotationAndExpiry(t *testing.T) {
	feedTestRedis(t)
	now := time.Now().UTC()
	cursor := feedCursor{UserID: 7, Revision: 9, Generation: strings.Repeat("a", 32), Algorithm: AlgorithmVersion, Block: 1, Offset: 2, Expires: now.Add(time.Minute).Unix()}
	token, err := encodeFeedCursor(cursor)
	require.NoError(t, err)
	decoded, err := decodeFeedCursor(token, 7, 9, now)
	require.NoError(t, err)
	require.Equal(t, cursor, decoded)
	_, err = decodeFeedCursor(token, 8, 9, now)
	require.ErrorIs(t, err, ErrResetRequired)
	_, err = decodeFeedCursor(token, 7, 10, now)
	require.ErrorIs(t, err, ErrResetRequired)
	_, err = decodeFeedCursor(token, 7, 9, now.Add(2*time.Minute))
	require.ErrorIs(t, err, ErrResetRequired)
	_, err = decodeFeedCursor(token+"tampered", 7, 9, now)
	require.ErrorIs(t, err, ErrResetRequired)
	config := feedConfig()
	config.SigningKeys = append([][]byte{[]byte(strings.Repeat("n", 32))}, config.SigningKeys...)
	Configure(config)
	_, err = decodeFeedCursor(token, 7, 9, now)
	require.NoError(t, err, "shared old key remains valid during rotation")
}

func TestFeedSnapshotsConcurrentCapAndBudget(t *testing.T) {
	_, client := feedTestRedis(t)
	ctx := context.Background()
	now := time.Now().UTC()
	var wg sync.WaitGroup
	for i := uint64(1); i <= 16; i++ {
		wg.Add(1)
		go func(revision uint64) {
			defer wg.Done()
			generation, _ := feedGeneration()
			_, err := saveSnapshot(ctx, Snapshot{Generation: generation, UserID: 1, Revision: revision, Preference: "generic", Algorithm: AlgorithmVersion, Mode: "rules", CreatedAt: now, ExpiresAt: now.Add(SnapshotTTL), Blocks: [][]RankedItem{{{ID: revision}}}})
			if err != nil {
				t.Errorf("save: %v", err)
			}
		}(i)
	}
	wg.Wait()
	dataKey, expiryKey := snapshotKeys(1)
	require.EqualValues(t, 2, client.HLen(ctx, dataKey).Val())
	require.EqualValues(t, 2, client.ZCard(ctx, expiryKey).Val())
	config := feedConfig()
	config.MaxActiveSnapshots = 1
	Configure(config)
	generation, _ := feedGeneration()
	_, err := saveSnapshot(ctx, Snapshot{Generation: generation, UserID: 99, CreatedAt: now, ExpiresAt: now.Add(SnapshotTTL)})
	require.ErrorIs(t, err, ErrUnavailable)
	require.EqualValues(t, 2, client.HLen(ctx, dataKey).Val(), "capacity failure must preserve existing valid snapshots")
}

func TestFeedPoolLostLeaseCannotPublish(t *testing.T) {
	_, client := feedTestRedis(t)
	ctx := context.Background()
	require.NoError(t, client.Set(ctx, feedPoolLeaseKey, "new-writer", time.Minute).Err())
	require.NoError(t, client.Set(ctx, feedPoolKey, "old-valid-pool", time.Minute).Err())
	published, err := publishFeedPoolScript.Run(ctx, client, []string{feedPoolLeaseKey, feedPoolKey}, "expired-writer", "bad-pool", 1000).Int()
	require.NoError(t, err)
	require.Zero(t, published)
	require.Equal(t, "old-valid-pool", client.Get(ctx, feedPoolKey).Val())
}

func TestFeedContentSourceHardExpiryDoesNotSlideWithPool(t *testing.T) {
	_, client := feedTestRedis(t)
	ctx := context.Background()
	now := time.Now().UTC()
	pool := candidatePool{Generation: "fresh-pool", AsOf: now.Add(-time.Minute), Mode: "meili_lexical", ContentDataCutoff: now.Add(-6 * time.Minute), Lists: []ContentList{{BucketKey: "go", Family: "lexical", IDs: []uint64{1}}}}
	data, err := json.Marshal(pool)
	require.NoError(t, err)
	require.NoError(t, client.Set(ctx, feedPoolKey, data, PoolMaxAge).Err())
	loaded, err := loadFeedPool(ctx, now)
	require.NoError(t, err)
	require.Equal(t, "rules", loaded.Mode)
	require.Empty(t, loaded.Lists)
	pool.ContentDataCutoff = now.Add(-2 * time.Minute)
	data, err = json.Marshal(pool)
	require.NoError(t, err)
	require.NoError(t, client.Set(ctx, feedPoolKey, data, PoolMaxAge).Err())
	loaded, err = loadFeedPool(ctx, now)
	require.NoError(t, err)
	require.Equal(t, "meili_lexical", loaded.Mode)
	_, err = loadFeedPool(ctx, now.Add(5*time.Minute))
	require.ErrorIs(t, err, ErrUnavailable, "pool itself must also obey its fixed as_of expiry")
}

func feedTestDatabase(t *testing.T) (*gorm.DB, *models.Account) {
	t.Helper()
	config := &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true, Logger: logger.Default.LogMode(logger.Silent)}
	var conn *gorm.DB
	var err error
	if dsn := os.Getenv("DISCOVERY_TEST_MYSQL_DSN"); dsn != "" {
		conn, err = gorm.Open(mysql.Open(dsn), config)
	} else {
		conn, err = pgtest.GormOpen(t, sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), config)
	}
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	previous := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(previous); _ = sqlDB.Close() })
	tables := []any{&models.User{}, &models.UserSetting{}, &models.Account{}, &models.AccountExtra{}, &models.Status{}, &models.StatusContent{}, &models.Tag{}, &models.StatusTag{}, &models.DomainBlock{}, &models.Block{}, &models.MuteAccount{}, &models.MuteStatus{}, &models.Filter{}, &models.Follow{}, &models.RecommendationFeedback{}, &models.FavoriteStatus{}, &models.StatusReaction{}, &models.Application{}, &models.Attachment{}, &models.Emoji{}, &models.Poll{}, &models.PollOption{}}
	require.NoError(t, conn.AutoMigrate(tables...))
	for _, table := range tables {
		require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true, AllowGlobalUpdate: true}).Unscoped().Delete(table).Error)
	}
	require.NoError(t, conn.Exec("CREATE TABLE IF NOT EXISTS followed_tags (id bigint PRIMARY KEY,account_id bigint,tag_id bigint,created_at timestamp)").Error)
	require.NoError(t, conn.Exec("DELETE FROM followed_tags").Error)
	write := conn.Session(&gorm.Session{SkipHooks: true})
	for i := uint64(1); i <= 31; i++ {
		user := models.User{Id: i, UUID: fmt.Sprintf("feed-%d", i), UserName: fmt.Sprintf("feed-%d", i), EmailHash: fmt.Sprintf("feed-%d", i), Status: models.UserStatusNormal}
		require.NoError(t, write.Create(&user).Error)
		account := models.Account{Id: i, UserId: i, Username: user.UserName, Acct: user.UserName, Discoverable: true}
		require.NoError(t, write.Omit("Extra", "User").Create(&account).Error)
	}
	var viewer models.Account
	require.NoError(t, conn.Take(&viewer, 1).Error)
	created := time.Now().UTC().Add(-time.Hour)
	for i := uint64(1); i <= 60; i++ {
		status := models.Status{Id: i, AccountId: 2 + (i % 30), Visibility: models.StatusVisibilityPublic, CreatedAt: &created}
		require.NoError(t, write.Omit("Content").Create(&status).Error)
		require.NoError(t, write.Create(&models.StatusContent{StatusId: i, Content: fmt.Sprintf("post %d", i)}).Error)
	}
	return conn, &viewer
}

func TestFeedPagingCurrentSQLAndFallback(t *testing.T) {
	mini, _ := feedTestRedis(t)
	conn, viewer := feedTestDatabase(t)
	ctx := context.Background()
	require.NoError(t, RefreshFeedCandidates(ctx))
	first, err := StatusPage(ctx, viewer, "", 1)
	require.NoError(t, err)
	require.Equal(t, "rules", first.Mode)
	require.Len(t, first.Items, 1)
	require.NotNil(t, first.NextCursor)
	retry, err := StatusPage(ctx, viewer, *first.NextCursor, 5)
	require.NoError(t, err)
	repeat, err := StatusPage(ctx, viewer, *first.NextCursor, 5)
	require.NoError(t, err)
	ids := func(page Page) []uint64 {
		result := []uint64{}
		for _, item := range page.Items {
			result = append(result, item.Status.Id)
		}
		return result
	}
	require.Equal(t, ids(retry), ids(repeat))
	require.NotContains(t, ids(retry), first.Items[0].Status.Id)
	if len(retry.Items) > 0 {
		hidden := retry.Items[0].Status.Id
		require.NoError(t, conn.Model(&models.Status{}).Where("id = ?", hidden).UpdateColumn("visibility", models.StatusVisibilityDirect).Error)
		checked, err := StatusPage(ctx, viewer, *first.NextCursor, 5)
		require.NoError(t, err)
		require.NotContains(t, ids(checked), hidden)
	}
	decoded, err := decodeFeedCursor(*first.NextCursor, viewer.Id, first.PreferenceRevision, time.Now())
	require.NoError(t, err)
	frozen, err := loadSnapshot(ctx, decoded, time.Now())
	require.NoError(t, err)
	blockIDs := []uint64{}
	for _, slot := range frozen.Blocks[decoded.Block] {
		blockIDs = append(blockIDs, slot.ID)
	}
	require.NoError(t, conn.Model(&models.Status{}).Where("id IN ?", blockIDs).UpdateColumn("visibility", models.StatusVisibilityPrivate).Error)
	empty, err := StatusPage(ctx, viewer, *first.NextCursor, 20)
	require.NoError(t, err)
	require.Empty(t, empty.Items)
	require.NotNil(t, empty.NextCursor, "empty block must advance, not hide later blocks")
	continued, err := StatusPage(ctx, viewer, *empty.NextCursor, 20)
	require.NoError(t, err)
	require.NotEmpty(t, continued.Items)
	_, err = models.SetRecommendationPreference(conn, viewer.UserId, "personalized")
	require.NoError(t, err)
	_, err = StatusPage(ctx, viewer, *first.NextCursor, 5)
	require.ErrorIs(t, err, ErrResetRequired)
	_, err = models.SetRecommendationPreference(conn, viewer.UserId, "generic")
	require.NoError(t, err)
	mini.FlushAll()
	fallback, err := StatusPage(ctx, viewer, "", 20)
	require.NoError(t, err)
	require.Equal(t, "chronological_fallback", fallback.Mode)
	require.Nil(t, fallback.NextCursor)
	require.LessOrEqual(t, len(fallback.Items), 20)
	_, err = models.SetRecommendationPreference(conn, viewer.UserId, "off")
	require.NoError(t, err)
	off, err := StatusPage(ctx, viewer, "", 20)
	require.NoError(t, err)
	require.True(t, off.Disabled)
	require.Empty(t, off.Items)
}

func TestFeedLongTailTopicRecallAndGenericIsolation(t *testing.T) {
	feedTestRedis(t)
	conn, viewer := feedTestDatabase(t)
	ctx := context.Background()
	write := conn.Session(&gorm.Session{SkipHooks: true})
	require.NoError(t, write.Create(&models.Tag{Id: 1, Name: "Go", NameKey: "go"}).Error)
	require.NoError(t, write.Create(&models.Tag{Id: 2, Name: "Garden", NameKey: "garden"}).Error)
	for i := uint64(1); i <= 40; i++ {
		tagID := uint64(1)
		if i > 20 {
			tagID = 2
		}
		require.NoError(t, write.Create(&models.StatusTag{StatusId: i, TagId: tagID}).Error)
	}
	require.NoError(t, conn.Exec("INSERT INTO followed_tags (id,account_id,tag_id,created_at) VALUES (?,?,?,?)", 1, viewer.Id, 1, time.Now()).Error)
	pool := candidatePool{Generation: "sql-only", AsOf: time.Now(), Mode: "rules", Candidates: []Candidate{}}
	// The topic is absent from the global cache; bounded SQL exact recall still
	// gives this user a distinct relevant subset without creating arbitrary keys.
	personal, err := selectFeedCandidates(ctx, viewer, true, pool)
	require.NoError(t, err)
	require.Len(t, personal, 20)
	for _, candidate := range personal {
		require.Contains(t, candidate.Tags, "go")
		require.Greater(t, candidate.Interest, 0.0)
	}
	generic, err := selectFeedCandidates(ctx, viewer, false, pool)
	require.NoError(t, err)
	require.Empty(t, generic)
	item := RankedItem{ID: 1, ReasonCode: "followed_topic", ReasonTag: "go"}
	require.Nil(t, feedReason(item, map[string]bool{"go": true}, &models.Status{Id: 1, Tags: models.Tags{&models.Tag{Name: "Garden"}}}), "removed topic must not survive as cached explanation")
}

func TestFeedDiagnosticsDoesNotEnumeratePersonalSnapshots(t *testing.T) {
	_, client := feedTestRedis(t)
	ctx := context.Background()
	status, err := RuntimeStatus(ctx)
	require.NoError(t, err)
	require.True(t, status.RedisAvailable)
	require.Equal(t, "pool_not_built", status.Reason)
	require.NoError(t, client.Set(ctx, feedPoolKey, `{"generation":"pool","as_of":"2000-01-01T00:00:00Z","mode":"rules","candidates":[]}`, time.Minute).Err())
	status, err = RuntimeStatus(ctx)
	require.NoError(t, err)
	require.True(t, status.PoolStale)
	require.Equal(t, "pool_expired", status.Reason)
	require.Equal(t, "pool", status.PoolGeneration)
}

func TestFeedPolicyChangesInvalidateOldWindowsAndLanguageChanges(t *testing.T) {
	feedTestRedis(t)
	conn, viewer := feedTestDatabase(t)
	ctx := context.Background()
	policy := "policy-one"
	config := feedConfig()
	config.Policy = func(context.Context) (string, error) { return policy, nil }
	Configure(config)
	require.NoError(t, RefreshFeedCandidates(ctx))
	first, err := StatusPage(ctx, viewer, "", 1)
	require.NoError(t, err)
	require.NotNil(t, first.NextCursor)
	policy = "policy-after-off-on"
	_, err = StatusPage(ctx, viewer, *first.NextCursor, 1)
	require.ErrorIs(t, err, ErrResetRequired)
	newPage, err := StatusPage(ctx, viewer, "", 1)
	require.NoError(t, err)
	require.NotEqual(t, first.Generation, newPage.Generation, "coalescing must not revive a revoked policy window")
	require.NoError(t, conn.Model(&models.Status{}).Where("id <= ?", 30).UpdateColumn("language", "ja").Error)
	require.NoError(t, conn.Model(&models.Status{}).Where("id > ?", 30).UpdateColumn("language", "en-US").Error)
	languages := []string{"en-GB"}
	_, err = models.SetRecommendationPreferences(conn, viewer.UserId, "generic", &languages)
	require.NoError(t, err)
	page, err := StatusPage(ctx, viewer, "", 20)
	require.NoError(t, err)
	require.NotEmpty(t, page.Items)
	for _, item := range page.Items {
		require.Equal(t, "en-US", item.Status.Language)
	}
}

func TestFeedModeReportsConsumedEligibleSources(t *testing.T) {
	_, client := feedTestRedis(t)
	conn, viewer := feedTestDatabase(t)
	ctx := context.Background()
	require.NoError(t, RefreshFeedCandidates(ctx))
	pool, err := loadFeedPool(ctx, time.Now())
	require.NoError(t, err)
	pool.Mode = "meili_hybrid"
	pool.ContentDataCutoff = time.Now().UTC()
	pool.Lists = []ContentList{{BucketKey: "go", Family: "lexical", Generation: "meili-generation", IDs: []uint64{1, 2, 3}}}
	data, err := json.Marshal(pool)
	require.NoError(t, err)
	require.NoError(t, client.Set(ctx, feedPoolKey, data, PoolMaxAge).Err())
	write := conn.Session(&gorm.Session{SkipHooks: true})
	require.NoError(t, write.Create(&models.Tag{Id: 1, Name: "Go", NameKey: "go"}).Error)
	require.NoError(t, conn.Exec("INSERT INTO followed_tags (id,account_id,tag_id,created_at) VALUES (?,?,?,?)", 1, viewer.Id, 1, time.Now()).Error)
	generic, err := StatusPage(ctx, viewer, "", 20)
	require.NoError(t, err)
	require.Equal(t, "rules", generic.Mode, "healthy enhanced pool does not make generic feed personalized")
	_, err = models.SetRecommendationPreference(conn, viewer.UserId, "personalized")
	require.NoError(t, err)
	personalized, err := StatusPage(ctx, viewer, "", 20)
	require.NoError(t, err)
	require.Equal(t, "meili_lexical", personalized.Mode, "only lexical sources were consumed despite hybrid-capable pool")
	require.NoError(t, conn.Model(&models.Status{}).Where("id IN ?", []uint64{1, 2, 3}).UpdateColumn("visibility", models.StatusVisibilityDirect).Error)
	filtered, err := StatusPage(ctx, viewer, "", 20)
	require.NoError(t, err)
	require.Equal(t, "rules", filtered.Mode, "ineligible Meili sources must not advertise an enhanced window")
}

func feedAuthorIDs(page Page) map[uint64]bool {
	authors := map[uint64]bool{}
	for _, item := range page.Items {
		authors[item.Status.AccountId] = true
	}
	return authors
}

func TestFeedHideAuthorAppliesImmediatelyToRulesAndMeili(t *testing.T) {
	for _, mode := range []string{"rules", "meili"} {
		t.Run(mode, func(t *testing.T) {
			_, client := feedTestRedis(t)
			conn, viewer := feedTestDatabase(t)
			ctx := context.Background()
			require.NoError(t, RefreshFeedCandidates(ctx))
			if mode == "meili" {
				pool, err := loadFeedPool(ctx, time.Now())
				require.NoError(t, err)
				pool.Mode = "meili_lexical"
				pool.ContentDataCutoff = time.Now().UTC()
				pool.Lists = []ContentList{{BucketKey: "go", Family: "lexical", Generation: "meili-generation", IDs: []uint64{1, 31, 61, 2, 3}}}
				data, err := json.Marshal(pool)
				require.NoError(t, err)
				require.NoError(t, client.Set(ctx, feedPoolKey, data, PoolMaxAge).Err())
				_, err = models.SetRecommendationPreference(conn, viewer.UserId, "personalized")
				require.NoError(t, err)
			}
			first, err := StatusPage(ctx, viewer, "", 5)
			require.NoError(t, err)
			require.NotEmpty(t, first.Items)
			target := first.Items[0].Status
			require.NotNil(t, first.NextCursor)
			_, err = models.PutRecommendationFeedback(conn, viewer.Id, "hide_author", "account", fmt.Sprint(target.AccountId))
			require.NoError(t, err)
			_, err = models.PutRecommendationFeedback(conn, viewer.Id, "hide_status", "status", fmt.Sprint(first.Items[1%len(first.Items)].Status.Id))
			require.NoError(t, err)
			// Old cursor pages are invalidated by the revision bump.
			_, err = StatusPage(ctx, viewer, *first.NextCursor, 5)
			require.ErrorIs(t, err, ErrResetRequired)
			// Every page of the refreshed window omits the author and status.
			cursor := ""
			for i := 0; i < 20; i++ {
				page, err := StatusPage(ctx, viewer, cursor, 20)
				require.NoError(t, err)
				require.False(t, feedAuthorIDs(page)[target.AccountId], "hidden author served on page %d", i)
				for _, item := range page.Items {
					require.NotEqual(t, first.Items[1%len(first.Items)].Status.Id, item.Status.Id)
				}
				if page.NextCursor == nil {
					break
				}
				cursor = *page.NextCursor
			}
		})
	}
}

func TestFeedHideRemoteAuthorAcrossPoolsAndQuotes(t *testing.T) {
	for _, mode := range []string{"rules", "meili"} {
		t.Run(mode, func(t *testing.T) {
			_, client := feedTestRedis(t)
			conn, viewer := feedTestDatabase(t)
			ctx := context.Background()
			write := conn.Session(&gorm.Session{SkipHooks: true})
			domain, yes, checked := "bgme.me", true, time.Now().UTC().Add(-time.Hour)
			remote := models.Account{Id: 114, Username: "kumori", Acct: "kumori@bgme.me", Domain: &domain, Discoverable: true, RemoteDiscoverable: &yes, RemoteIndexable: &yes, RemoteDiscoveryCheckedAt: &checked}
			require.NoError(t, write.Omit("Extra", "User").Create(&remote).Error)
			duplicate := models.Account{Id: 115, Username: "Kumori", Acct: "Kumori@bgme.me", Domain: &domain, Discoverable: true, RemoteDiscoverable: &yes, RemoteIndexable: &yes, RemoteDiscoveryCheckedAt: &checked}
			require.NoError(t, write.Omit("Extra", "User").Create(&duplicate).Error)
			require.NoError(t, conn.Model(&models.Status{}).Where("id IN ?", []uint64{1, 2, 3, 4, 5, 6}).UpdateColumn("account_id", 114).Error)
			require.NoError(t, conn.Model(&models.Status{}).Where("id IN ?", []uint64{8, 9, 10}).UpdateColumn("account_id", 115).Error)
			quote := uint64(1)
			require.NoError(t, conn.Model(&models.Status{}).Where("id = ?", 7).UpdateColumn("quote_id", quote).Error)
			require.NoError(t, RefreshFeedCandidates(ctx))
			if mode == "meili" {
				pool, err := loadFeedPool(ctx, time.Now())
				require.NoError(t, err)
				pool.Mode = "meili_lexical"
				pool.ContentDataCutoff = time.Now().UTC()
				pool.Lists = []ContentList{{BucketKey: "go", Family: "lexical", Generation: "meili-generation", IDs: []uint64{1, 2, 3, 7, 8}}}
				data, err := json.Marshal(pool)
				require.NoError(t, err)
				require.NoError(t, client.Set(ctx, feedPoolKey, data, PoolMaxAge).Err())
				_, err = models.SetRecommendationPreference(conn, viewer.UserId, "personalized")
				require.NoError(t, err)
			}
			collect := func() (authors map[uint64]bool) {
				authors = map[uint64]bool{}
				cursor := ""
				for i := 0; i < 20; i++ {
					page, err := StatusPage(ctx, viewer, cursor, 20)
					require.NoError(t, err)
					for _, item := range page.Items {
						authors[item.Status.AccountId] = true
						if item.Status.Quote != nil {
							authors[item.Status.Quote.AccountId] = true
						}
					}
					if page.NextCursor == nil {
						break
					}
					cursor = *page.NextCursor
				}
				return authors
			}
			before := collect()
			require.True(t, before[114] && before[115], "fixture must surface both rows of the remote author before feedback")
			_, err := models.PutRecommendationFeedback(conn, viewer.Id, "hide_author", "account", "114")
			require.NoError(t, err)
			after := collect()
			require.False(t, after[114], "hidden remote author must not be served, including as a quote")
			require.False(t, after[115], "a duplicate row of the same actor (same acct) must be hidden too")
		})
	}
}
