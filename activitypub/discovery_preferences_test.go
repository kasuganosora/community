package activitypub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

func decodeDiscoveryActor(t *testing.T, fields string) *Actor {
	t.Helper()
	var actor Actor
	require.NoError(t, json.Unmarshal([]byte(`{"id":"https://remote.test/users/alice","type":"Person","preferredUsername":"alice"`+fields+`}`), &actor))
	return &actor
}

func TestActorDiscoveryParsingNeverCoercesInvalidOrMissingValues(t *testing.T) {
	for _, tc := range []struct {
		input        string
		discoverable *bool
		invalid      bool
	}{
		{"", nil, false},
		{`,"discoverable":true`, discoveryBool(true), false},
		{`,"discoverable":false`, discoveryBool(false), false},
		{`,"discoverable":null`, nil, true},
		{`,"discoverable":"true"`, nil, true},
		{`,"discoverable":1`, nil, true},
		{`,"discoverable":{}`, nil, true},
	} {
		t.Run(tc.input, func(t *testing.T) {
			actor := decodeDiscoveryActor(t, tc.input)
			require.Equal(t, tc.discoverable, actor.Discoverable)
			require.Equal(t, tc.invalid, actor.discoveryInvalid)
			account := models.Account{Domain: new("remote.test")}
			applyInitialActorDiscovery(&account, actor, time.Now())
			require.Equal(t, tc.discoverable != nil && *tc.discoverable && !tc.invalid, account.AllowsRecommendation(time.Now()))
		})
	}
	for _, input := range []string{`,"discoverable":true,"indexable":null`, `,"discoverable":true,"indexable":"true"`, `,"discoverable":true,"hideCollections":1`} {
		actor := decodeDiscoveryActor(t, input)
		account := models.Account{Domain: new("remote.test")}
		applyInitialActorDiscovery(&account, actor, time.Now())
		require.False(t, account.AllowsRecommendation(time.Now()))
	}
}

func TestActorDiscoveryUpdatesPreserveRefusalsAndDoNotRenewPartialConsent(t *testing.T) {
	old := time.Now().Add(-2 * time.Hour)
	account := &models.Account{RemoteDiscoverable: discoveryBool(false), RemoteIndexable: discoveryBool(false), RemoteHideCollections: discoveryBool(true), RemoteDiscoveryCheckedAt: &old}
	for _, partial := range []bool{false, true} {
		updates := actorDiscoveryUpdates(account, decodeDiscoveryActor(t, ""), partial, time.Now())
		if partial {
			require.Empty(t, updates)
			continue
		}
		require.Equal(t, discoveryBool(false), updates["remote_discoverable"])
		require.Equal(t, discoveryBool(false), updates["remote_indexable"])
		require.Equal(t, discoveryBool(true), updates["remote_hide_collections"])
	}
	legacy := actorDiscoveryUpdates(&models.Account{NoIndex: true, HideCollections: true}, decodeDiscoveryActor(t, `,"discoverable":true`), false, time.Now())
	require.Equal(t, discoveryBool(false), legacy["remote_indexable"])
	require.Equal(t, discoveryBool(true), legacy["remote_hide_collections"])
	updates := actorDiscoveryUpdates(account, decodeDiscoveryActor(t, `,"discoverable":true`), true, time.Now())
	require.Equal(t, discoveryBool(true), updates["remote_discoverable"])
	require.NotContains(t, updates, "remote_indexable")
	require.NotContains(t, updates, "remote_discovery_checked_at", "partial update cannot extend missing old permissions")
	updates = actorDiscoveryUpdates(account, decodeDiscoveryActor(t, `,"indexable":null`), true, time.Now())
	require.Equal(t, discoveryBool(false), updates["remote_indexable"])
	require.Contains(t, updates, "remote_discovery_checked_at")
	require.Nil(t, updates["remote_discovery_checked_at"])
	updates = actorDiscoveryUpdates(&models.Account{RemoteDiscoverable: discoveryBool(true)}, decodeDiscoveryActor(t, ""), false, time.Now())
	require.Nil(t, updates["remote_discoverable"].(*bool), "full refresh missing opt-in withdraws old permission")
}

func TestActorDiscoveryPersistsCreateRefreshAndAuthenticatedUpdate(t *testing.T) {
	conn := emojiDB(t)
	actor := decodeDiscoveryActor(t, `,"discoverable":true,"indexable":true,"hideCollections":false`)
	account, err := createRemoteAccount(conn, actor)
	require.NoError(t, err)
	require.True(t, account.AllowsRecommendation(time.Now()))
	var saved models.Account
	require.NoError(t, conn.First(&saved, account.Id).Error)
	require.True(t, saved.AllowsRecommendation(time.Now()))
	actor.Indexable = discoveryBool(false)
	require.NoError(t, RefreshRemoteAccount(context.Background(), conn, staticActorFetcher{actor: actor}, &saved, false))
	require.False(t, saved.AllowsRecommendation(time.Now()))
	// A profile-only update may come from cached data. The persisted refusal
	// is reloaded under the transaction lock, not inferred from cache defaults.
	resolver := getDefaultResolver()
	resolver.storeAccountCache(account.OriginId, &account)
	ctx := context.WithValue(context.Background(), actorKey, actor.ID)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/inbox", nil)
	processInboxTransaction(ctx, c, "", func(txCtx context.Context) {
		handleUpdateActivity(txCtx, c, map[string]any{"object": map[string]any{"id": actor.ID, "type": "Person", "name": "New name"}}, nil)
	})
	require.Equal(t, 200, w.Code, w.Body.String())
	require.NoError(t, conn.First(&saved, account.Id).Error)
	require.False(t, saved.AllowsRecommendation(time.Now()))
	require.Equal(t, discoveryBool(false), saved.RemoteIndexable)
	// A complete actor document withdrawing discoverable is different from
	// an incremental update which simply did not include that field.
	require.NoError(t, RefreshRemoteAccount(context.Background(), conn, staticActorFetcher{actor: decodeDiscoveryActor(t, "")}, &saved, false))
	require.Nil(t, saved.RemoteDiscoverable)
	require.False(t, saved.Discoverable)
	require.Equal(t, discoveryBool(false), saved.RemoteIndexable, "complete omission still cannot clear explicit noindex")
	require.False(t, saved.AllowsRecommendation(time.Now()))
}

type discoveryCountingFetcher struct {
	calls int
	fail  bool
}

func (f *discoveryCountingFetcher) FetchActor(_ context.Context, uri string) (*Actor, error) {
	f.calls++
	if f.fail {
		return nil, errors.New("temporary unavailable")
	}
	return &Actor{ID: uri, Type: "Person", Discoverable: discoveryBool(true)}, nil
}
func (f *discoveryCountingFetcher) FetchObject(context.Context, string) (map[string]any, error) {
	panic("discovery refresh must not fetch collections")
}

func TestRemoteDiscoveryRefreshIsBoundedAndFailuresNeverRenewConsent(t *testing.T) {
	conn := emojiDB(t)
	old := time.Now().Add(-25 * time.Hour)
	for _, name := range []string{"one", "two", "three"} {
		a := models.Account{Username: name, Domain: new("remote.test"), OriginId: "https://remote.test/users/" + name, RemoteDiscoveryCheckedAt: &old}
		require.NoError(t, conn.Create(&a).Error)
	}
	fetcher := &discoveryCountingFetcher{fail: true}
	n, err := RefreshRemoteDiscoveryPreferences(context.Background(), conn, fetcher, 2)
	require.NoError(t, err, "remote failures are non-fatal and retried later")
	require.Equal(t, 2, n)
	require.Equal(t, 2, fetcher.calls)
	var accounts []models.Account
	require.NoError(t, conn.Find(&accounts).Error)
	for _, account := range accounts {
		require.False(t, account.AllowsRecommendation(time.Now()))
		require.WithinDuration(t, old, *account.RemoteDiscoveryCheckedAt, time.Millisecond)
	}
	fetcher.fail = false
	n, err = RefreshRemoteDiscoveryPreferences(context.Background(), conn, fetcher, 2)
	require.NoError(t, err)
	require.Equal(t, 1, n, "failed accounts back off so another account can make progress")
	require.NoError(t, conn.Find(&accounts).Error)
	eligible := 0
	for _, account := range accounts {
		if account.AllowsRecommendation(time.Now()) {
			eligible++
		}
	}
	require.Equal(t, 1, eligible)
}

func TestLocalActorPublishesExplicitDiscoveryPreferences(t *testing.T) {
	conn := outboundDB(t)
	account := outboundAccount(t, conn, "consent", false)
	account.Discoverable, account.NoIndex, account.HideCollections = true, true, true
	actor, err := buildActorProfile(account, nil)
	require.NoError(t, err)
	encoded, err := json.Marshal(actor)
	require.NoError(t, err)
	var object map[string]any
	require.NoError(t, json.Unmarshal(encoded, &object))
	require.Equal(t, true, object["discoverable"])
	require.Equal(t, false, object["indexable"])
	require.Equal(t, true, object["hideCollections"])
	require.Contains(t, string(encoded), `"discoverable":"toot:discoverable"`)
}

func TestRemoteDiscoveryRefreshPrioritisesRecentPosters(t *testing.T) {
	conn := emojiDB(t)
	recentPost := time.Now().Add(-time.Hour)
	var poster models.Account
	for _, name := range []string{"idle1", "idle2", "poster"} {
		created := time.Now().Add(-2 * time.Hour)
		a := models.Account{Username: name, Domain: new("remote.test"), OriginId: "https://remote.test/users/" + name, CreatedAt: &created}
		require.NoError(t, conn.Create(&a).Error)
		if name == "poster" {
			poster = a
		}
	}
	require.NoError(t, conn.Create(&models.Status{AccountId: poster.Id, Visibility: models.StatusVisibilityPublic, CreatedAt: &recentPost}).Error)
	fetcher := &discoveryCountingFetcher{}
	n, err := RefreshRemoteDiscoveryPreferences(context.Background(), conn, fetcher, 1)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	var saved models.Account
	require.NoError(t, conn.First(&saved, poster.Id).Error)
	require.NotNil(t, saved.RemoteDiscoveryCheckedAt, "the recent poster is checked first")
	require.True(t, saved.AllowsRecommendation(time.Now()))
}

type domainFailingFetcher struct {
	mu       sync.Mutex
	failHost string
	calls    map[string]int
	status   int
}

func (f *domainFailingFetcher) FetchActor(_ context.Context, uri string) (*Actor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[uri]++
	if strings.Contains(uri, f.failHost) {
		status := f.status
		if status == 0 {
			status = 429
		}
		return nil, &FetchError{StatusCode: status}
	}
	return &Actor{ID: uri, Type: "Person", Discoverable: discoveryBool(true)}, nil
}
func (f *domainFailingFetcher) FetchObject(context.Context, string) (map[string]any, error) {
	panic("unused")
}

func TestRemoteDiscoveryRefreshDoesNotBlockOnRateLimitedDomain(t *testing.T) {
	conn := emojiDB(t)
	for i := range 4 {
		for _, host := range []string{"busy.test", "ok.test"} {
			a := models.Account{Username: fmt.Sprintf("u%d", i), Domain: new(host), OriginId: fmt.Sprintf("https://%s/users/u%d", host, i)}
			require.NoError(t, conn.Create(&a).Error)
		}
	}
	fetcher := &domainFailingFetcher{failHost: "busy.test", calls: map[string]int{}}
	n, err := RefreshRemoteDiscoveryPreferences(context.Background(), conn, fetcher, 8)
	require.NoError(t, err)
	require.Equal(t, 5, n, "one 429 on busy.test, then all of ok.test")
	var busyAttempted, okChecked int64
	require.NoError(t, conn.Model(&models.Account{}).Where("domain = ? AND remote_discovery_attempted_at IS NOT NULL", "busy.test").Count(&busyAttempted).Error)
	require.NoError(t, conn.Model(&models.Account{}).Where("domain = ? AND remote_discovery_checked_at IS NOT NULL", "ok.test").Count(&okChecked).Error)
	require.EqualValues(t, 1, busyAttempted, "the rest of a rate-limited domain stays unclaimed, not backed off")
	require.EqualValues(t, 4, okChecked)
	// Next cycle: the unclaimed busy.test rows are picked again.
	fetcher.failHost = "none"
	n, err = RefreshRemoteDiscoveryPreferences(context.Background(), conn, fetcher, 8)
	require.NoError(t, err)
	require.Equal(t, 3, n)
}

func TestSpreadRemoteDiscoveryByDomainCapsEachDomain(t *testing.T) {
	var in []models.Account
	for i := range 10 {
		in = append(in, models.Account{Id: uint64(i), Domain: new("big.test")})
	}
	in = append(in, models.Account{Id: 99, Domain: new("small.test")})
	out := spreadRemoteDiscoveryByDomain(in, 5, 3)
	require.Len(t, out, 4)
	require.Equal(t, uint64(99), out[3].Id)
}

func TestRemoteDiscoveryRefreshMarksInvalidDocuments(t *testing.T) {
	conn := emojiDB(t)
	a := models.Account{Username: "bad", Domain: new("remote.test"), OriginId: "https://remote.test/users/bad"}
	require.NoError(t, conn.Create(&a).Error)
	actor := decodeDiscoveryActor(t, `,"discoverable":null`)
	actor.ID = a.OriginId
	_, err := RefreshRemoteDiscoveryPreferences(context.Background(), conn, staticActorFetcher{actor: actor}, 1)
	require.NoError(t, err)
	var saved models.Account
	require.NoError(t, conn.First(&saved, a.Id).Error)
	require.Equal(t, "invalid", saved.RemoteDiscoverySource)
	require.Nil(t, saved.RemoteDiscoveryCheckedAt)
	require.NotNil(t, saved.RemoteDiscoveryAttemptedAt)
}

// The backfill must not write accounts before network fetches, and failed rows
// are backed off in one narrow statement: only attempted_at, by primary key,
// no updated_at bump and no hook-driven columns (acct, matrix_alias).
func TestRemoteDiscoveryRefreshEmitsNarrowAttemptedUpdate(t *testing.T) {
	conn := emojiDB(t)
	var ids []uint64
	for i := range 3 {
		a := models.Account{Username: fmt.Sprintf("u%d", i), Domain: new("down.test"), OriginId: fmt.Sprintf("https://down.test/users/u%d", i)}
		require.NoError(t, conn.Create(&a).Error)
		ids = append(ids, a.Id)
	}
	var before models.Account
	require.NoError(t, conn.First(&before, ids[0]).Error)
	var updates []string
	require.NoError(t, conn.Callback().Update().After("gorm:update").Register("test:capture_sql", func(db *gorm.DB) {
		updates = append(updates, db.Statement.SQL.String())
	}))
	// 404 is a non-transient failure, so all three rows are tried.
	fetcher := &domainFailingFetcher{failHost: "down.test", calls: map[string]int{}}
	fetcher.status = 404
	n, err := RefreshRemoteDiscoveryPreferences(context.Background(), conn, fetcher, 8)
	require.NoError(t, err)
	require.Equal(t, 3, n)
	require.Len(t, updates, 1, "one batched UPDATE per cycle, got %v", updates)
	sql := strings.ToLower(updates[0])
	require.Contains(t, sql, "remote_discovery_attempted_at")
	require.Contains(t, sql, " in (")
	for _, col := range []string{"updated_at", "acct", "matrix_alias", "remote_discovery_checked_at"} {
		require.NotContains(t, sql, "`"+col+"`", sql)
	}
	var after models.Account
	require.NoError(t, conn.First(&after, ids[0]).Error)
	require.NotNil(t, after.RemoteDiscoveryAttemptedAt)
	require.Nil(t, after.RemoteDiscoveryCheckedAt)
	require.Equal(t, before.UpdatedAt.Unix(), after.UpdatedAt.Unix(), "updated_at untouched")
	var attempted int64
	require.NoError(t, conn.Model(&models.Account{}).Where("id IN ? AND remote_discovery_attempted_at IS NOT NULL", ids).Count(&attempted).Error)
	require.EqualValues(t, 3, attempted)
}

func TestInvalidPartialDiscoveryUpdateIsNotTreatedAsUnread(t *testing.T) {
	conn := emojiDB(t)
	account := models.Account{Username: "legacy", Domain: new("remote.test"), OriginId: "https://remote.test/users/legacy"}
	require.NoError(t, conn.Create(&account).Error)
	require.True(t, account.AllowsTrendAggregation(time.Now()))
	updates := actorDiscoveryUpdates(&account, decodeDiscoveryActor(t, `,"discoverable":null`), true, time.Now())
	require.NoError(t, conn.Model(&account).Updates(updates).Error)
	var saved models.Account
	require.NoError(t, conn.First(&saved, account.Id).Error)
	require.Equal(t, "invalid", saved.RemoteDiscoverySource)
	require.False(t, saved.RemoteDiscoveryUnknown())
	require.False(t, saved.AllowsRecommendation(time.Now()))
	require.False(t, saved.AllowsTrendAggregation(time.Now()))
}

type discoveryCallbackFetcher struct {
	fetch func(context.Context, string) (*Actor, error)
}

func (f discoveryCallbackFetcher) FetchActor(ctx context.Context, uri string) (*Actor, error) {
	return f.fetch(ctx, uri)
}
func (discoveryCallbackFetcher) FetchObject(context.Context, string) (map[string]any, error) {
	panic("discovery refresh must not fetch collections")
}

func TestRemoteDiscoveryRefreshDoesNotOverwriteNewerPrivacyUpdate(t *testing.T) {
	for _, fields := range []string{`,"discoverable":false`, `,"indexable":false`, `,"discoverable":null`} {
		t.Run(fields, func(t *testing.T) {
			conn := emojiDB(t)
			old := time.Now().Add(-25 * time.Hour)
			account := models.Account{Username: "changing", Domain: new("remote.test"), OriginId: "https://remote.test/users/changing", RemoteDiscoverable: discoveryBool(true), RemoteDiscoveryCheckedAt: &old, RemoteDiscoverySource: "actor"}
			require.NoError(t, conn.Create(&account).Error)
			incoming := decodeDiscoveryActor(t, fields)
			fetcher := discoveryCallbackFetcher{fetch: func(_ context.Context, uri string) (*Actor, error) {
				// Simulate a verified partial inbox Update completing while the
				// older HTTP actor response is still pending.
				updates := actorDiscoveryUpdates(&account, incoming, true, time.Now())
				if err := conn.Model(&models.Account{}).Where("id = ?", account.Id).Updates(updates).Error; err != nil {
					return nil, err
				}
				return &Actor{ID: uri, Type: "Person", Discoverable: discoveryBool(true), Indexable: discoveryBool(true)}, nil
			}}
			n, err := RefreshRemoteDiscoveryPreferences(context.Background(), conn, fetcher, 1)
			require.NoError(t, err)
			require.Equal(t, 1, n)
			var saved models.Account
			require.NoError(t, conn.First(&saved, account.Id).Error)
			require.False(t, saved.AllowsRecommendation(time.Now()), "late fetch must not reinstate consent")
			require.False(t, saved.AllowsTrendAggregation(time.Now()))
			if incoming.Discoverable != nil {
				require.Equal(t, incoming.Discoverable, saved.RemoteDiscoverable)
			}
			if incoming.Indexable != nil {
				require.Equal(t, incoming.Indexable, saved.RemoteIndexable)
			}
		})
	}
}

func TestRemoteDiscoveryRefreshRefillsBeyondDominantDomain(t *testing.T) {
	conn := emojiDB(t)
	for i := range 40 {
		account := models.Account{Username: fmt.Sprintf("busy%d", i), Domain: new("busy.test"), OriginId: fmt.Sprintf("https://busy.test/users/u%d", i)}
		require.NoError(t, conn.Create(&account).Error)
	}
	other := models.Account{Username: "small", Domain: new("small.test"), OriginId: "https://small.test/users/small"}
	require.NoError(t, conn.Create(&other).Error)
	fetcher := &domainFailingFetcher{failHost: "busy.test", calls: map[string]int{}}
	n, err := RefreshRemoteDiscoveryPreferences(context.Background(), conn, fetcher, 8)
	require.NoError(t, err)
	require.Equal(t, 2, n, "one rate-limited big-domain request must not starve the smaller domain beyond the first 32 rows")
	var saved models.Account
	require.NoError(t, conn.First(&saved, other.Id).Error)
	require.True(t, saved.AllowsRecommendation(time.Now()))
}

func TestRemoteDiscoveryRefreshTimesOutOneDomainWithoutClaimingItsQueue(t *testing.T) {
	conn := emojiDB(t)
	for _, name := range []string{"slow1", "slow2"} {
		account := models.Account{Username: name, Domain: new("slow.test"), OriginId: "https://slow.test/users/" + name}
		require.NoError(t, conn.Create(&account).Error)
	}
	other := models.Account{Username: "fast", Domain: new("fast.test"), OriginId: "https://fast.test/users/fast"}
	require.NoError(t, conn.Create(&other).Error)
	oldTimeout := remoteDiscoveryFetchTimeout
	remoteDiscoveryFetchTimeout = 20 * time.Millisecond
	t.Cleanup(func() { remoteDiscoveryFetchTimeout = oldTimeout })
	fetcher := discoveryCallbackFetcher{fetch: func(ctx context.Context, uri string) (*Actor, error) {
		if strings.Contains(uri, "slow.test") {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return &Actor{ID: uri, Type: "Person", Discoverable: discoveryBool(true)}, nil
	}}
	n, err := RefreshRemoteDiscoveryPreferences(context.Background(), conn, fetcher, 3)
	require.NoError(t, err, "an offline domain is an expected maintenance outcome")
	require.Equal(t, 2, n)
	var skipped, saved models.Account
	require.NoError(t, conn.Where("username = ?", "slow2").First(&skipped).Error)
	require.Nil(t, skipped.RemoteDiscoveryAttemptedAt, "unattempted accounts must remain eligible next cycle")
	require.NoError(t, conn.First(&saved, other.Id).Error)
	require.True(t, saved.AllowsRecommendation(time.Now()))
}
