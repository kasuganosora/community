package activitypub

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const asPublic = "https://www.w3.org/ns/activitystreams#Public"

// fakeRemote serves an actor, its outbox and featured collection. Every GET
// must carry an HTTP signature (authorized fetch).
type fakeRemote struct {
	server   *httptest.Server
	docs     map[string]func(base string) any
	hits     atomic.Int64
	unsigned atomic.Int64
	deny     map[string]int
	// hook, when set, runs before answering and may return a status code.
	hook     func(key string) int
	inflight atomic.Int64
	peak     atomic.Int64
}

func newFakeRemote(t *testing.T) *fakeRemote {
	f := &fakeRemote{docs: map[string]func(string) any{}, deny: map[string]int{}}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		if r.Header.Get("Signature") == "" {
			f.unsigned.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		key := r.URL.Path
		if r.URL.RawQuery != "" {
			key += "?" + r.URL.RawQuery
		}
		n := f.inflight.Add(1)
		defer f.inflight.Add(-1)
		for {
			p := f.peak.Load()
			if n <= p || f.peak.CompareAndSwap(p, n) {
				break
			}
		}
		if f.hook != nil {
			if code := f.hook(key); code != 0 {
				w.WriteHeader(code)
				return
			}
		}
		if code := f.deny[key]; code != 0 {
			w.WriteHeader(code)
			return
		}
		doc, ok := f.docs[key]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/activity+json")
		_ = json.NewEncoder(w).Encode(doc(f.server.URL))
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeRemote) person(name string, extra map[string]any) {
	f.docs["/users/"+name] = func(base string) any {
		doc := map[string]any{"id": base + "/users/" + name, "type": "Person", "preferredUsername": name, "inbox": base + "/users/" + name + "/inbox",
			"outbox": base + "/users/" + name + "/outbox", "featured": base + "/users/" + name + "/collections/featured"}
		for k, v := range extra {
			doc[k] = v
		}
		return doc
	}
}

func note(base, author, id, text string, to ...any) map[string]any {
	if len(to) == 0 {
		to = []any{asPublic}
	}
	return map[string]any{"id": base + id, "type": "Note", "attributedTo": base + "/users/" + author, "content": "<p>" + text + "</p>",
		"published": "2026-01-02T03:04:05Z", "to": to, "cc": []any{base + "/users/" + author + "/followers"}}
}

func setupProfileBackfill(t *testing.T) (*gorm.DB, *fakeRemote) {
	conn := persistenceDB(t, false)
	require.NoError(t, conn.AutoMigrate(&models.PinStatus{}, &models.DomainBlock{}))
	remote := newFakeRemote(t)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	prevClient, prevShared, prevStart := profileBackfillClient, sharedHTTPClient, startProfileBackfill
	profileBackfillClient = func(context.Context) (*Client, error) {
		c := NewClient(key, "https://local.example/actor")
		c.httpClient = ssrfHTTPClientForTesting()
		return c, nil
	}
	sharedHTTPClient = ssrfHTTPClientForTesting()
	startProfileBackfill = func(f func()) { go f() }
	ResetProfileBackfillCooldown()
	t.Cleanup(func() {
		profileBackfillClient, sharedHTTPClient, startProfileBackfill = prevClient, prevShared, prevStart
		ResetProfileBackfillCooldown()
	})
	return conn, remote
}

func remoteAccount(t *testing.T, conn *gorm.DB, base, name string) *models.Account {
	host := strings.TrimPrefix(base, "http://")
	a := &models.Account{Username: name, Domain: &host, OriginId: base + "/users/" + name}
	require.NoError(t, conn.Create(a).Error)
	return a
}

func runProfileBackfill(t *testing.T, account *models.Account) string {
	state, done := RequestProfileBackfill(context.Background(), account)
	require.Equal(t, ProfileBackfillPending, state)
	require.NotNil(t, done)
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("backfill did not finish")
	}
	state, done = RequestProfileBackfill(context.Background(), account)
	require.Nil(t, done, "cooldown suppresses an immediate second run")
	return state
}

func profileTexts(t *testing.T, conn *gorm.DB, accountID uint64) []string {
	var rows []models.Status
	require.NoError(t, conn.Preload("Content").Where("account_id = ?", accountID).Order("id DESC").Find(&rows).Error)
	out := []string{}
	for _, s := range rows {
		require.True(t, s.IsProfileBackfill() && s.IsBackfilled(), "status %d must be marked as backfill", s.Id)
		if s.ReblogId != nil {
			var orig models.Status
			require.NoError(t, conn.Preload("Content").First(&orig, *s.ReblogId).Error)
			out = append(out, "RT "+orig.Content.Content)
			continue
		}
		out = append(out, s.Content.Content)
	}
	return out
}

func TestProfileBackfillMastodonOutboxFeaturedAndAnnounce(t *testing.T) {
	conn, remote := setupProfileBackfill(t)
	remote.person("alice", nil)
	remote.person("bob", nil)
	remote.docs["/users/bob/statuses/9"] = func(b string) any { return note(b, "bob", "/users/bob/statuses/9", "boosted") }
	remote.docs["/users/alice/statuses/3"] = func(b string) any { return note(b, "alice", "/users/alice/statuses/3", "fetched by reference") }
	remote.docs["/users/alice/outbox"] = func(b string) any {
		return map[string]any{"id": b + "/users/alice/outbox", "type": "OrderedCollection", "totalItems": 42, "first": b + "/users/alice/outbox?page=true"}
	}
	remote.docs["/users/alice/outbox?page=true"] = func(b string) any {
		return map[string]any{"id": b + "/users/alice/outbox?page=true", "type": "OrderedCollectionPage", "orderedItems": []any{
			map[string]any{"id": b + "/users/alice/statuses/5/activity", "type": "Create", "actor": b + "/users/alice", "to": []any{asPublic}, "object": note(b, "alice", "/users/alice/statuses/5", "newest")},
			map[string]any{"id": b + "/users/alice/statuses/4/activity", "type": "Announce", "actor": b + "/users/alice", "to": []any{asPublic}, "published": "2026-01-01T00:00:00Z", "object": b + "/users/bob/statuses/9"},
			map[string]any{"id": b + "/users/alice/statuses/6/activity", "type": "Create", "actor": b + "/users/alice", "to": []any{b + "/users/alice/followers"}, "object": note(b, "alice", "/users/alice/statuses/6", "followers only", b+"/users/alice/followers")},
			map[string]any{"id": b + "/users/alice/statuses/7/activity", "type": "Announce", "actor": b + "/users/alice", "to": []any{b + "/users/alice/followers"}, "object": b + "/users/bob/statuses/9"},
			map[string]any{"id": b + "/users/alice/statuses/3/activity", "type": "Create", "actor": b + "/users/alice", "to": []any{asPublic}, "object": b + "/users/alice/statuses/3"},
			map[string]any{"id": b + "/users/alice/statuses/8/activity", "type": "Create", "actor": b + "/users/alice", "to": []any{asPublic}, "object": note(b, "mallory", "/users/alice/statuses/8", "forged author")},
		}}
	}
	remote.docs["/users/alice/collections/featured"] = func(b string) any {
		return map[string]any{"id": b + "/users/alice/collections/featured", "type": "OrderedCollection", "orderedItems": []any{note(b, "alice", "/users/alice/statuses/1", "pinned")}}
	}
	alice := remoteAccount(t, conn, remote.server.URL, "alice")
	// A stale pin from an earlier import is replaced.
	stale := models.Status{AccountId: alice.Id, Visibility: models.StatusVisibilityPublic, Content: models.StatusContent{Content: "<p>old pin</p>"}}
	stale.MarkProfileBackfill()
	require.NoError(t, conn.Create(&stale).Error)
	require.NoError(t, conn.Create(&models.PinStatus{AccountId: alice.Id, StatusId: stale.Id}).Error)

	require.Equal(t, ProfileBackfillDone, runProfileBackfill(t, alice))
	require.Equal(t, []string{"<p>newest</p>", "RT <p>boosted</p>", "<p>fetched by reference</p>", "<p>pinned</p>", "<p>old pin</p>"}, profileTexts(t, conn, alice.Id))

	var pins []models.PinStatus
	require.NoError(t, conn.Where("account_id = ?", alice.Id).Find(&pins).Error)
	require.Len(t, pins, 1)
	var pinned models.Status
	require.NoError(t, conn.First(&pinned, pins[0].StatusId).Error)
	require.Equal(t, remote.server.URL+"/users/alice/statuses/1", *pinned.OriginUri)

	var boosted models.Status
	require.NoError(t, conn.Where("origin_uri = ?", remote.server.URL+"/users/bob/statuses/9").First(&boosted).Error)
	require.EqualValues(t, 1, boosted.ReblogsCount)
	require.True(t, boosted.IsProfileBackfill(), "the boosted original stays out of timelines too")
	var extra models.AccountExtra
	require.NoError(t, conn.Where("account_id = ?", alice.Id).First(&extra).Error)
	require.EqualValues(t, 42, extra.StatusesCount)
	require.Zero(t, remote.unsigned.Load(), "every fetch is signed by the instance actor")

	var notifications int64
	require.NoError(t, conn.Model(&models.Notification{}).Count(&notifications).Error)
	require.Zero(t, notifications)

	// After the cooldown a rerun deduplicates.
	ResetProfileBackfillCooldown()
	require.Equal(t, ProfileBackfillDone, runProfileBackfill(t, alice))
	require.Len(t, profileTexts(t, conn, alice.Id), 5)
}

func TestProfileBackfillMisskeyEmbeddedFirstPage(t *testing.T) {
	conn, remote := setupProfileBackfill(t)
	remote.person("luna", nil)
	remote.person("other", nil)
	remote.docs["/notes/b1"] = func(b string) any { return note(b, "other", "/notes/b1", "renoted") }
	remote.docs["/users/luna/outbox"] = func(b string) any {
		return map[string]any{"id": b + "/users/luna/outbox", "type": "OrderedCollection", "totalItems": 2, "first": map[string]any{
			"type": "OrderedCollectionPage", "orderedItems": []any{
				map[string]any{"id": b + "/notes/a2/activity", "type": "Create", "actor": b + "/users/luna", "to": []any{asPublic}, "object": note(b, "luna", "/notes/a2", "misskey note")},
				map[string]any{"id": b + "/notes/r1/activity", "type": "Announce", "actor": b + "/users/luna", "to": []any{asPublic}, "object": b + "/notes/b1"},
			}}}
	}
	remote.docs["/users/luna/collections/featured"] = func(b string) any {
		return map[string]any{"id": b + "/users/luna/collections/featured", "type": "OrderedCollection", "orderedItems": []any{b + "/notes/a2"}}
	}
	remote.docs["/notes/a2"] = func(b string) any { return note(b, "luna", "/notes/a2", "misskey note") }
	luna := remoteAccount(t, conn, remote.server.URL, "luna")

	require.Equal(t, ProfileBackfillDone, runProfileBackfill(t, luna))
	require.Equal(t, []string{"<p>misskey note</p>", "RT <p>renoted</p>"}, profileTexts(t, conn, luna.Id))
	var pins int64
	require.NoError(t, conn.Model(&models.PinStatus{}).Where("account_id = ?", luna.Id).Count(&pins).Error)
	require.EqualValues(t, 1, pins)
}

func TestProfileBackfillHiddenOutboxIsUnavailable(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
		conn, remote := setupProfileBackfill(t)
		remote.person("gts", nil)
		remote.deny["/users/gts/outbox"] = code
		remote.deny["/users/gts/collections/featured"] = code
		account := remoteAccount(t, conn, remote.server.URL, "gts")
		require.Equal(t, ProfileBackfillUnavailable, runProfileBackfill(t, account), "HTTP %d", code)
		var count int64
		require.NoError(t, conn.Model(&models.Status{}).Count(&count).Error)
		require.Zero(t, count)
	}
}

func TestProfileBackfillCooldownAndConcurrentRequestsShareOneRun(t *testing.T) {
	conn, remote := setupProfileBackfill(t)
	remote.person("alice", nil)
	remote.docs["/users/alice/outbox"] = func(b string) any {
		return map[string]any{"id": b + "/users/alice/outbox", "type": "OrderedCollection", "orderedItems": []any{}}
	}
	alice := remoteAccount(t, conn, remote.server.URL, "alice")
	gate := make(chan struct{})
	var started atomic.Int64
	startProfileBackfill = func(f func()) { started.Add(1); go func() { <-gate; f() }() }

	s1, d1 := RequestProfileBackfill(context.Background(), alice)
	s2, d2 := RequestProfileBackfill(context.Background(), alice)
	require.Equal(t, ProfileBackfillPending, s1)
	require.Equal(t, ProfileBackfillPending, s2)
	require.True(t, d1 == d2, "concurrent viewers join the same run")
	close(gate)
	<-d1
	hits := remote.hits.Load()
	for i := 0; i < 3; i++ {
		state, done := RequestProfileBackfill(context.Background(), alice)
		require.Nil(t, done)
		require.Equal(t, ProfileBackfillDone, state)
	}
	require.EqualValues(t, 1, started.Load())
	require.Equal(t, hits, remote.hits.Load(), "no fetches during the cooldown")
}

func TestProfileBackfillSkipsSuspendedDomainsAndLocalAccounts(t *testing.T) {
	conn, remote := setupProfileBackfill(t)
	remote.person("alice", nil)
	alice := remoteAccount(t, conn, remote.server.URL, "alice")
	require.NoError(t, conn.Create(&models.DomainBlock{Domain: "127.0.0.1", Severity: "suspend"}).Error)
	state, done := RequestProfileBackfill(context.Background(), alice)
	require.Empty(t, state)
	require.Nil(t, done)
	state, done = RequestProfileBackfill(context.Background(), &models.Account{Id: 99, Username: "local"})
	require.Empty(t, state)
	require.Nil(t, done)
	require.Zero(t, remote.hits.Load())
}

func TestProfileBackfillHostLimit(t *testing.T) {
	prev := profileBackfillPerHost
	profileBackfillPerHost = 1
	t.Cleanup(func() { profileBackfillPerHost = prev })
	release, err := acquireProfileBackfillSlot(context.Background(), "a.example")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = acquireProfileBackfillSlot(ctx, "a.example")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	other, err := acquireProfileBackfillSlot(context.Background(), "b.example")
	require.NoError(t, err)
	other()
	got := make(chan struct{})
	go func() {
		r, err := acquireProfileBackfillSlot(context.Background(), "a.example")
		if err == nil {
			r()
		}
		close(got)
	}()
	release()
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("waiter not woken")
	}
}

func pinCount(t *testing.T, conn *gorm.DB, accountID uint64) int64 {
	var n int64
	require.NoError(t, conn.Model(&models.PinStatus{}).Where("account_id = ?", accountID).Count(&n).Error)
	return n
}

func featuredOne(remote *fakeRemote, name string) {
	remote.docs["/users/"+name+"/collections/featured"] = func(b string) any {
		return map[string]any{"id": b + "/users/" + name + "/collections/featured", "type": "OrderedCollection", "orderedItems": []any{b + "/users/" + name + "/statuses/1"}}
	}
	remote.docs["/users/"+name+"/statuses/1"] = func(b string) any { return note(b, name, "/users/"+name+"/statuses/1", "pinned") }
}

func TestProfileBackfillSlowOutboxDoesNotStarvePins(t *testing.T) {
	conn, remote := setupProfileBackfill(t)
	prev := profileBackfillTimeout
	profileBackfillTimeout = 800 * time.Millisecond
	t.Cleanup(func() { profileBackfillTimeout = prev })
	remote.person("slow", nil)
	featuredOne(remote, "slow")
	remote.docs["/users/slow/outbox"] = func(b string) any {
		return map[string]any{"id": b + "/users/slow/outbox", "type": "OrderedCollection", "orderedItems": []any{}}
	}
	remote.hook = func(key string) int {
		if key == "/users/slow/outbox" {
			time.Sleep(2 * time.Second)
		}
		return 0
	}
	account := remoteAccount(t, conn, remote.server.URL, "slow")
	require.Equal(t, ProfileBackfillUnavailable, runProfileBackfill(t, account))
	require.EqualValues(t, 1, pinCount(t, conn, account.Id), "pins use their own deadline")
	require.Equal(t, profilePinsDone, profilePinsState(account.Id))
}

func TestProfileBackfillPinsTimeoutRetriesOnce(t *testing.T) {
	conn, remote := setupProfileBackfill(t)
	prevTimeout, prevSchedule := profileBackfillPinsTimeout, scheduleProfilePinsRetry
	profileBackfillPinsTimeout = 300 * time.Millisecond
	var retries []func()
	scheduleProfilePinsRetry = func(d time.Duration, f func()) { retries = append(retries, f) }
	t.Cleanup(func() { profileBackfillPinsTimeout, scheduleProfilePinsRetry = prevTimeout, prevSchedule })
	remote.person("pin", nil)
	featuredOne(remote, "pin")
	remote.docs["/users/pin/outbox"] = func(b string) any {
		return map[string]any{"id": b + "/users/pin/outbox", "type": "OrderedCollection", "orderedItems": []any{}}
	}
	var slow atomic.Bool
	slow.Store(true)
	remote.hook = func(key string) int {
		if key == "/users/pin/collections/featured" && slow.Load() {
			time.Sleep(time.Second)
		}
		return 0
	}
	account := remoteAccount(t, conn, remote.server.URL, "pin")
	old := models.Status{AccountId: account.Id, Visibility: models.StatusVisibilityPublic, Content: models.StatusContent{Content: "<p>old pin</p>"}}
	old.MarkProfileBackfill()
	require.NoError(t, conn.Create(&old).Error)
	require.NoError(t, conn.Create(&models.PinStatus{AccountId: account.Id, StatusId: old.Id}).Error)

	require.Equal(t, ProfileBackfillDone, runProfileBackfill(t, account))
	require.Len(t, retries, 1, "one retry is scheduled")
	require.Equal(t, profilePinsRetry, profilePinsState(account.Id))
	var pins []models.PinStatus
	require.NoError(t, conn.Where("account_id = ?", account.Id).Find(&pins).Error)
	require.Len(t, pins, 1)
	require.Equal(t, old.Id, pins[0].StatusId, "existing pins are kept on timeout")

	slow.Store(false)
	retries[0]()
	require.Len(t, retries, 1, "the retry does not schedule another")
	require.Equal(t, profilePinsDone, profilePinsState(account.Id))
	require.NoError(t, conn.Where("account_id = ?", account.Id).Find(&pins).Error)
	require.Len(t, pins, 1)
	var pinned models.Status
	require.NoError(t, conn.First(&pinned, pins[0].StatusId).Error)
	require.Equal(t, remote.server.URL+"/users/pin/statuses/1", *pinned.OriginUri)
}

func TestProfileBackfillConcurrentFetchKeepsOrder(t *testing.T) {
	conn, remote := setupProfileBackfill(t)
	remote.person("many", nil)
	remote.person("bob", nil)
	featuredOne(remote, "many")
	const n = 12
	var items []any
	for i := n; i >= 2; i-- { // newest first, like Mastodon
		i := i
		path := fmt.Sprintf("/users/many/statuses/%d", i)
		if i%3 == 0 {
			target := fmt.Sprintf("/users/bob/statuses/%d", i)
			remote.docs[target] = func(b string) any { return note(b, "bob", target, fmt.Sprintf("b%d", i)) }
			items = append(items, func(b string) any {
				return map[string]any{"id": b + path + "/activity", "type": "Announce", "actor": b + "/users/many", "to": []any{asPublic}, "object": b + target}
			})
			continue
		}
		remote.docs[path] = func(b string) any { return note(b, "many", path, fmt.Sprintf("n%d", i)) }
		items = append(items, func(b string) any {
			return map[string]any{"id": b + path + "/activity", "type": "Create", "actor": b + "/users/many", "to": []any{asPublic}, "object": b + path}
		})
	}
	remote.docs["/users/many/outbox"] = func(b string) any {
		out := []any{}
		for _, f := range items {
			out = append(out, f.(func(string) any)(b))
		}
		return map[string]any{"id": b + "/users/many/outbox", "type": "OrderedCollection", "orderedItems": out}
	}
	remote.hook = func(key string) int {
		if strings.Contains(key, "/statuses/") {
			time.Sleep(150 * time.Millisecond)
		}
		return 0
	}
	account := remoteAccount(t, conn, remote.server.URL, "many")
	start := time.Now()
	require.Equal(t, ProfileBackfillDone, runProfileBackfill(t, account))
	elapsed := time.Since(start)
	want := []string{}
	for i := n; i >= 2; i-- {
		if i%3 == 0 {
			want = append(want, fmt.Sprintf("RT <p>b%d</p>", i))
		} else {
			want = append(want, fmt.Sprintf("<p>n%d</p>", i))
		}
	}
	want = append(want, "<p>pinned</p>")
	require.Equal(t, want, profileTexts(t, conn, account.Id), "pins first, then outbox oldest to newest")
	require.EqualValues(t, 1, pinCount(t, conn, account.Id))
	peak := remote.peak.Load()
	require.Greater(t, peak, int64(1), "fetches run concurrently")
	require.LessOrEqual(t, peak, int64(profileBackfillFetchPerHost+1), "bounded per host (+1 for the pins fetch)")
	require.Less(t, elapsed, time.Duration(n)*150*time.Millisecond, "faster than sequential")
}
