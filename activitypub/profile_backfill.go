package activitypub

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kasuganosora/community/db"
	dbredis "github.com/kasuganosora/community/db/redis"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils"
	goredis "github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// Profile backfill: when someone opens a remote account's profile we usually
// only know the few posts that were delivered to us. RequestProfileBackfill
// imports the actor's featured (pinned) collection and the first outbox page
// in the background, signed as the instance actor. Imported posts carry the
// profile backfill marker, so they never reach home/public/tag timelines or
// notifications.

// Profile backfill states reported to API clients (X-Profile-Backfill).
const (
	ProfileBackfillPending     = "pending"
	ProfileBackfillDone        = "done"
	ProfileBackfillUnavailable = "unavailable"
)

var (
	// ProfileBackfillCooldown is how long a backfill attempt (successful or
	// not) suppresses the next one for the same account.
	ProfileBackfillCooldown = 30 * time.Minute
	// profileBackfillItems is how many non-reply original posts a run tries
	// to collect; it keeps following the outbox's "next" pages until then.
	profileBackfillItems = 20
	// profileBackfillMaxPages caps that walk. Reply-heavy accounts (kanna:
	// 100 items, 0 originals) need many pages, so the cap is on pages while
	// the stored items stay bounded: once profileBackfillReplyItems items
	// are collected, further replies are skipped and only originals and
	// boosts are kept, up to profileBackfillMaxItems.
	profileBackfillMaxPages   = 20
	profileBackfillReplyItems = 100
	profileBackfillMaxItems   = 200
	// profileBackfillPageDelay paces page fetches politely.
	profileBackfillPageDelay = 300 * time.Millisecond
	// profileBackfillPins bounds the featured items imported per run.
	profileBackfillPins = 10
	// profileBackfillPerHost bounds concurrent runs against one remote host.
	profileBackfillPerHost = 2
	// profileBackfillSlots bounds concurrent runs overall.
	profileBackfillSlots   = make(chan struct{}, 8)
	profileBackfillTimeout = 3 * time.Minute

	// profileBackfillClient signs every fetch as the instance actor so
	// servers that enforce authorized fetch answer too.
	profileBackfillClient = func(ctx context.Context) (*Client, error) { return instanceActorSigningClient(ctx) }
	// startProfileBackfill runs a backfill; tests replace it.
	startProfileBackfill = func(f func()) { go f() }
)

type profileBackfillRun struct {
	done  chan struct{}
	state string
}

var profileBackfills = struct {
	sync.Mutex
	runs     map[uint64]*profileBackfillRun
	hosts    map[string]int
	hostWait map[string]chan struct{}
	// local is the cooldown store used when Redis is not configured.
	local map[uint64]profileBackfillMark
	// pins records pin outcomes when Redis is not configured.
	pins map[uint64]string
}{pins: map[uint64]string{}, runs: map[uint64]*profileBackfillRun{}, hosts: map[string]int{}, hostWait: map[string]chan struct{}{}, local: map[uint64]profileBackfillMark{}}

type profileBackfillMark struct {
	state   string
	expires time.Time
}

func profileBackfillKey(id uint64) string {
	// v2: runs now paginate the outbox; v1 marks (page 1 only) are ignored
	// so those accounts are backfilled again.
	return "community:profile_backfill:v2:" + strconv.FormatUint(id, 10)
}

// claimProfileBackfill takes the cooldown slot for id. When the slot is taken
// it returns the state recorded by the run that holds it.
func claimProfileBackfill(ctx context.Context, id uint64) (bool, string) {
	if clients := dbredis.GetRedises(); len(clients) > 0 {
		ok, err := clients[0].SetNX(ctx, profileBackfillKey(id), ProfileBackfillPending, ProfileBackfillCooldown).Result()
		if err == nil {
			if ok {
				return true, ""
			}
			state, _ := clients[0].Get(ctx, profileBackfillKey(id)).Result()
			return false, state
		}
		log.Warn("profile backfill: redis cooldown unavailable, using local cooldown: %v", err)
	}
	profileBackfills.Lock()
	defer profileBackfills.Unlock()
	if mark, ok := profileBackfills.local[id]; ok && time.Now().Before(mark.expires) {
		return false, mark.state
	}
	for k, mark := range profileBackfills.local {
		if time.Now().After(mark.expires) {
			delete(profileBackfills.local, k)
		}
	}
	profileBackfills.local[id] = profileBackfillMark{state: ProfileBackfillPending, expires: time.Now().Add(ProfileBackfillCooldown)}
	return true, ""
}

// recordProfileBackfill stores the outcome without extending the cooldown.
func recordProfileBackfill(id uint64, state string) {
	if clients := dbredis.GetRedises(); len(clients) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := clients[0].Set(ctx, profileBackfillKey(id), state, goredis.KeepTTL).Err(); err == nil {
			return
		}
	}
	profileBackfills.Lock()
	defer profileBackfills.Unlock()
	if mark, ok := profileBackfills.local[id]; ok {
		mark.state = state
		profileBackfills.local[id] = mark
	}
}

// ResetProfileBackfillCooldown forgets the cooldown state, local and in
// Redis (tests only).
func ResetProfileBackfillCooldown() {
	for _, c := range dbredis.GetRedises() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		iter := c.Scan(ctx, 0, "community:profile_backfill:*", 100).Iterator()
		for iter.Next(ctx) {
			c.Del(ctx, iter.Val())
		}
		cancel()
	}
	profileBackfills.Lock()
	defer profileBackfills.Unlock()
	profileBackfills.local = map[uint64]profileBackfillMark{}
	profileBackfills.pins = map[uint64]string{}
}

// RequestProfileBackfill starts (or joins) a background backfill of a remote
// account. It returns "" for accounts that are never backfilled, the state of
// the last run while the cooldown holds, or pending with a channel that is
// closed when the run started by this process finishes.
func RequestProfileBackfill(ctx context.Context, account *models.Account) (string, <-chan struct{}) {
	if account == nil || account.Id == 0 || account.Domain == nil || *account.Domain == "" || account.OriginId == "" || account.IsBan {
		return "", nil
	}
	host := profileBackfillHost(account)
	if host == "" || validateFederationURI(account.OriginId) != nil {
		return "", nil
	}
	if suspended, err := hostSuspended(host); err != nil || suspended {
		return "", nil
	}
	profileBackfills.Lock()
	if run, ok := profileBackfills.runs[account.Id]; ok {
		profileBackfills.Unlock()
		return ProfileBackfillPending, run.done
	}
	profileBackfills.Unlock()
	claimed, state := claimProfileBackfill(ctx, account.Id)
	if !claimed {
		return state, nil
	}
	profileBackfills.Lock()
	if run, ok := profileBackfills.runs[account.Id]; ok {
		profileBackfills.Unlock()
		return ProfileBackfillPending, run.done
	}
	run := &profileBackfillRun{done: make(chan struct{})}
	profileBackfills.runs[account.Id] = run
	profileBackfills.Unlock()

	acct := *account
	startProfileBackfill(func() {
		state := ProfileBackfillUnavailable
		defer func() {
			if rec := recover(); rec != nil {
				log.Error("panic backfilling profile %d: %v", acct.Id, rec)
			}
			recordProfileBackfill(acct.Id, state)
			profileBackfills.Lock()
			delete(profileBackfills.runs, acct.Id)
			profileBackfills.Unlock()
			close(run.done)
		}()
		bctx, cancel := context.WithTimeout(context.Background(), profileBackfillTimeout)
		defer cancel()
		release, err := acquireProfileBackfillSlot(bctx, host)
		if err != nil {
			return
		}
		defer release()
		var berr error
		state, berr = backfillProfile(bctx, &acct)
		if berr != nil {
			log.Info("profile backfill of %s: %v", acct.OriginId, berr)
		}
	})
	return ProfileBackfillPending, run.done
}

func profileBackfillHost(account *models.Account) string {
	u, err := url.Parse(account.OriginId)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// hostSuspended reports whether host or one of its parent domains is
// suspended by an instance domain block.
func hostSuspended(host string) (bool, error) {
	suspend, _, err := models.DomainSeverities(db.GetDB())
	if err != nil {
		return false, err
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for host != "" {
		if suspend[host] {
			return true, nil
		}
		i := strings.IndexByte(host, '.')
		if i < 0 {
			break
		}
		host = host[i+1:]
	}
	return false, nil
}

// acquireProfileBackfillSlot waits for a per-host and a global slot.
func acquireProfileBackfillSlot(ctx context.Context, host string) (func(), error) {
	for {
		profileBackfills.Lock()
		if profileBackfills.hosts[host] < profileBackfillPerHost {
			profileBackfills.hosts[host]++
			profileBackfills.Unlock()
			break
		}
		wait, ok := profileBackfills.hostWait[host]
		if !ok {
			wait = make(chan struct{})
			profileBackfills.hostWait[host] = wait
		}
		profileBackfills.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	releaseHost := func() {
		profileBackfills.Lock()
		defer profileBackfills.Unlock()
		if profileBackfills.hosts[host]--; profileBackfills.hosts[host] <= 0 {
			delete(profileBackfills.hosts, host)
		}
		if wait, ok := profileBackfills.hostWait[host]; ok {
			delete(profileBackfills.hostWait, host)
			close(wait)
		}
	}
	select {
	case profileBackfillSlots <- struct{}{}:
	case <-ctx.Done():
		releaseHost()
		return nil, ctx.Err()
	}
	return func() { <-profileBackfillSlots; releaseHost() }, nil
}

// errOutboxUnavailable means the remote hides or refuses the outbox.
var errOutboxUnavailable = errors.New("outbox unavailable")

var (
	// profileBackfillPinsTimeout bounds the featured (pins) fetch. It runs
	// on its own context so slow outbox work cannot starve it.
	profileBackfillPinsTimeout = 30 * time.Second
	// profileBackfillPinsRetryDelay is when a pins fetch that failed with a
	// transient error is retried once, independently of the cooldown.
	profileBackfillPinsRetryDelay = time.Minute
	// profileBackfillFetchPerHost bounds concurrent object fetches per
	// remote host within one backfill run.
	profileBackfillFetchPerHost = 4
	// scheduleProfilePinsRetry runs f after d; tests replace it.
	scheduleProfilePinsRetry = func(d time.Duration, f func()) { time.AfterFunc(d, f) }
)

// Pin outcomes recorded next to the cooldown state.
const (
	profilePinsDone   = "done"
	profilePinsRetry  = "retry"
	profilePinsFailed = "failed"
	profilePinsNone   = "none"
)

func profilePinsKey(id uint64) string { return profileBackfillKey(id) + ":pins" }

// recordProfilePins stores the pins outcome for the cooldown period.
func recordProfilePins(id uint64, state string) {
	if clients := dbredis.GetRedises(); len(clients) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := clients[0].Set(ctx, profilePinsKey(id), state, ProfileBackfillCooldown).Err(); err == nil {
			return
		}
	}
	profileBackfills.Lock()
	defer profileBackfills.Unlock()
	if profileBackfills.pins == nil {
		profileBackfills.pins = map[uint64]string{}
	}
	profileBackfills.pins[id] = state
}

// profilePinsState returns the recorded pins outcome (tests and debugging).
func profilePinsState(id uint64) string {
	if clients := dbredis.GetRedises(); len(clients) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if v, err := clients[0].Get(ctx, profilePinsKey(id)).Result(); err == nil {
			return v
		}
	}
	profileBackfills.Lock()
	defer profileBackfills.Unlock()
	return profileBackfills.pins[id]
}

// hostLimiter bounds concurrent work per host.
type hostLimiter struct {
	mu    sync.Mutex
	limit int
	sems  map[string]chan struct{}
}

func newHostLimiter(limit int) *hostLimiter {
	if limit < 1 {
		limit = 1
	}
	return &hostLimiter{limit: limit, sems: map[string]chan struct{}{}}
}

func (l *hostLimiter) acquire(ctx context.Context, uri string) (func(), error) {
	host := strings.ToLower(utils.ExtractDomainFromURI(uri))
	l.mu.Lock()
	sem, ok := l.sems[host]
	if !ok {
		sem = make(chan struct{}, l.limit)
		l.sems[host] = sem
	}
	l.mu.Unlock()
	select {
	case sem <- struct{}{}:
		return func() { <-sem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// fetchResult caches one fetch (or failure) for the run.
type fetchResult struct {
	obj map[string]any
	err error
}

type announceResult struct {
	status *models.Status
	err    error
}

// backfillCache is shared by the concurrent prefetch and the ordered import
// phases of one run.
type backfillCache struct {
	mu        sync.Mutex
	objects   map[string]fetchResult
	originals map[string]announceResult
	limiter   *hostLimiter
}

func newBackfillCache() *backfillCache {
	return &backfillCache{objects: map[string]fetchResult{}, originals: map[string]announceResult{}, limiter: newHostLimiter(profileBackfillFetchPerHost)}
}

// parallel runs f for every index with the per-host limiter keyed by key(i),
// waiting for all of them.
func (c *backfillCache) parallel(ctx context.Context, n int, key func(int) string, f func(int)) {
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		k := key(i)
		if k == "" {
			continue
		}
		wg.Add(1)
		go func(i int, k string) {
			defer wg.Done()
			release, err := c.limiter.acquire(ctx, k)
			if err != nil {
				return
			}
			defer release()
			f(i)
		}(i, k)
	}
	wg.Wait()
}

// backfillProfile imports the featured collection and the newest outbox
// items of account. It returns ProfileBackfillUnavailable when the outbox
// cannot be read.
func backfillProfile(ctx context.Context, account *models.Account) (string, error) {
	client, err := profileBackfillClient(ctx)
	if err != nil || client == nil {
		log.Warn("profile backfill: instance actor signing unavailable: %v", err)
		client = NewFetchClient()
	}
	r := &Resolver{client: client, cacheTTL: defaultCacheTTL, noQuoteFetch: true, profileBackfill: true}
	actor, err := client.FetchObject(ctx, account.OriginId)
	if err != nil {
		return ProfileBackfillUnavailable, fmt.Errorf("fetching actor: %w", err)
	}
	if id, _ := actor["id"].(string); id != account.OriginId {
		return ProfileBackfillUnavailable, errors.New("actor identity mismatch")
	}
	cache := newBackfillCache()
	b := &profileBackfiller{ctx: ctx, r: r, account: account, cache: cache}

	// Pins run concurrently on their own deadline.
	featuredURI := objectID(actor["featured"])
	type pinsOutcome struct {
		items []any
		err   error
	}
	pinsCh := make(chan pinsOutcome, 1)
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				pinsCh <- pinsOutcome{err: fmt.Errorf("panic: %v", rec)}
			}
		}()
		if featuredURI == "" {
			pinsCh <- pinsOutcome{}
			return
		}
		items, err := fetchFeatured(r, account, cache, featuredURI)
		pinsCh <- pinsOutcome{items: items, err: err}
	}()

	var items []any
	total := -1
	outbox := objectID(actor["outbox"])
	outErr := errOutboxUnavailable
	if outbox != "" {
		if items, total, err = b.outboxItems(outbox); err != nil {
			outErr = fmt.Errorf("%w: %w", errOutboxUnavailable, err)
		} else {
			outErr = nil
		}
	}
	b.prefetchOutbox(items)

	pins := <-pinsCh
	pinsOK := pins.err == nil
	if pins.err != nil {
		if isTransientFetchError(pins.err) {
			log.Info("profile backfill: pins of %s: %v (retrying once)", account.OriginId, pins.err)
			recordProfilePins(account.Id, profilePinsRetry)
			schedulePinsRetry(account, featuredURI)
		} else {
			log.Info("profile backfill: pins of %s: %v", account.OriginId, pins.err)
			recordProfilePins(account.Id, profilePinsFailed)
		}
	}

	// Pinned posts are usually older than the outbox page: import those not
	// on it first so the profile still reads newest first by id.
	onOutbox := map[string]bool{}
	for _, item := range items {
		if m, ok := item.(map[string]any); ok && stringField(m, "type") == "Create" {
			onOutbox[objectID(m["object"])] = true
		}
	}
	// Pin writes get their own deadline as well: a slow outbox must not
	// leave them with an expired context.
	pctx, pcancel := context.WithTimeout(context.Background(), profileBackfillPinsTimeout)
	defer pcancel()
	pb := &profileBackfiller{ctx: pctx, r: r, account: account, cache: cache}
	if pinsOK {
		for i := len(pins.items) - 1; i >= 0; i-- {
			if !onOutbox[objectID(pins.items[i])] {
				if _, err := pb.importNote(pins.items[i]); err != nil {
					log.Debug("profile backfill: skipping featured item: %v", err)
				}
			}
		}
	}
	// Oldest first: statuses sort by id, so the newest import gets the
	// highest id and the profile reads newest first.
	for i := len(items) - 1; i >= 0; i-- {
		if ctx.Err() != nil {
			break
		}
		if err := b.importOutboxItem(items[i]); err != nil {
			log.Debug("profile backfill: skipping outbox item of %s: %v", account.OriginId, err)
		}
	}
	if pinsOK {
		if featuredURI == "" {
			recordProfilePins(account.Id, profilePinsNone)
		} else if err := pb.syncPins(pins.items); err != nil {
			log.Info("profile backfill: pins of %s: %v", account.OriginId, err)
			recordProfilePins(account.Id, profilePinsFailed)
		} else {
			recordProfilePins(account.Id, profilePinsDone)
		}
	}
	if outErr != nil {
		return ProfileBackfillUnavailable, outErr
	}
	if total >= 0 {
		// Imports bump statuses_count; the outbox total is authoritative.
		_ = db.GetDB().WithContext(ctx).Model(&models.AccountExtra{}).Where("account_id = ?", account.Id).UpdateColumn("statuses_count", total).Error
	}
	return ProfileBackfillDone, nil
}

// fetchFeatured reads the featured collection and prefetches its notes on
// an independent deadline. A transient failure of any fetch fails the whole
// call, so existing pins are never replaced by a partial list.
func fetchFeatured(r *Resolver, account *models.Account, cache *backfillCache, uri string) ([]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), profileBackfillPinsTimeout)
	defer cancel()
	pb := &profileBackfiller{ctx: ctx, r: r, account: account, cache: cache}
	items, _, err := pb.collectionItems(uri, profileBackfillPins)
	if err != nil {
		return nil, err
	}
	var mu sync.Mutex
	var transient error
	cache.parallel(ctx, len(items), func(i int) string { return objectID(items[i]) }, func(i int) {
		if _, _, err := pb.prefetchNote(items[i]); err != nil && isTransientFetchError(err) {
			mu.Lock()
			if transient == nil {
				transient = err
			}
			mu.Unlock()
		}
	})
	if transient == nil && ctx.Err() != nil {
		transient = ctx.Err()
	}
	return items, transient
}

// schedulePinsRetry retries just the pins once after a transient failure.
func schedulePinsRetry(account *models.Account, featuredURI string) {
	acct := *account
	scheduleProfilePinsRetry(profileBackfillPinsRetryDelay, func() {
		defer func() {
			if rec := recover(); rec != nil {
				log.Error("panic retrying pins of %d: %v", acct.Id, rec)
			}
		}()
		state := profilePinsFailed
		defer func() { recordProfilePins(acct.Id, state) }()
		host := profileBackfillHost(&acct)
		if suspended, err := hostSuspended(host); err != nil || suspended {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), profileBackfillPinsTimeout+10*time.Second)
		defer cancel()
		release, err := acquireProfileBackfillSlot(ctx, host)
		if err != nil {
			return
		}
		defer release()
		client, err := profileBackfillClient(ctx)
		if err != nil || client == nil {
			client = NewFetchClient()
		}
		r := &Resolver{client: client, cacheTTL: defaultCacheTTL, noQuoteFetch: true, profileBackfill: true}
		cache := newBackfillCache()
		items, err := fetchFeatured(r, &acct, cache, featuredURI)
		if err != nil {
			log.Info("profile backfill: pins retry of %s: %v", acct.OriginId, err)
			return
		}
		b := &profileBackfiller{ctx: ctx, r: r, account: &acct, cache: cache}
		for i := len(items) - 1; i >= 0; i-- {
			if _, err := b.importNote(items[i]); err != nil {
				log.Debug("profile backfill: skipping featured item: %v", err)
			}
		}
		if err := b.syncPins(items); err != nil {
			log.Info("profile backfill: pins retry of %s: %v", acct.OriginId, err)
			return
		}
		state = profilePinsDone
	})
}

// prefetchOutbox fetches what the outbox items refer to (bare activities,
// referenced notes, boosted posts) concurrently, bounded per host. The
// ordered import afterwards reads the results from the cache.
func (b *profileBackfiller) prefetchOutbox(items []any) {
	// Bare activity references first; they decide what to fetch next.
	b.cache.parallel(b.ctx, len(items), func(i int) string {
		if s, ok := items[i].(string); ok {
			return s
		}
		return ""
	}, func(i int) { _, _ = b.fetchOwn(items[i].(string)) })
	acts := make([]map[string]any, len(items))
	for i, item := range items {
		if m, ok := item.(map[string]any); ok {
			acts[i] = m
		} else if s, ok := item.(string); ok {
			if obj, err := b.fetchOwn(s); err == nil && objectID(obj) == s {
				acts[i] = obj
			}
		}
	}
	// Announce targets are deduplicated so one post is fetched once.
	seen := map[string]bool{}
	key := func(i int) string {
		a := acts[i]
		if a == nil {
			return ""
		}
		switch stringField(a, "type") {
		case "Create":
			return objectID(a["object"])
		case "Announce":
			t := objectID(a["object"])
			if t == "" || seen[t] || !sameURIOrigin(objectID(a), b.account.OriginId) ||
				firstActorID(a["actor"]) != b.account.OriginId || getVisibilityFromActivity(a) > models.StatusVisibilityUnlisted {
				return ""
			}
			seen[t] = true
			return t
		}
		return ""
	}
	keys := make([]string, len(acts))
	for i := range acts {
		keys[i] = key(i)
	}
	b.cache.parallel(b.ctx, len(acts), func(i int) string { return keys[i] }, func(i int) {
		a := acts[i]
		switch stringField(a, "type") {
		case "Create":
			if firstActorID(a["actor"]) == b.account.OriginId {
				_, _, _ = b.prefetchNote(a["object"])
			}
		case "Announce":
			_, _ = b.announceTarget(a)
		}
	})
}

// prefetchNote warms the cache for an own note unless it is stored already.
func (b *profileBackfiller) prefetchNote(item any) (map[string]any, string, error) {
	if id := objectID(item); id != "" {
		if existing, err := findInboundParent(b.ctx, id); err == nil && existing != nil && existing.Id != 0 {
			return nil, id, nil
		}
	}
	return b.ownNote(item)
}

// announceTarget resolves (and caches) the boosted post of an Announce by
// the account.
func (b *profileBackfiller) announceTarget(activity map[string]any) (*models.Status, error) {
	target := objectID(activity["object"])
	if target == "" || validateFederationURI(target) != nil {
		return nil, errors.New("announce without object")
	}
	if b.cache != nil {
		b.cache.mu.Lock()
		res, ok := b.cache.originals[target]
		b.cache.mu.Unlock()
		if ok {
			return res.status, res.err
		}
	}
	if suspended, err := hostSuspended(utils.ExtractDomainFromURI(target)); err != nil || suspended {
		return nil, errors.New("announced post is on a suspended domain")
	}
	original, err := fetchPublicStatus(b.ctx, b.r, target)
	// Cache successes and remote answers; local failures (for example two
	// concurrent imports racing on the same author) are retried by the
	// ordered import.
	if b.cache != nil && (err == nil || fetchStatusCode(err) != 0 || errors.Is(err, errStatusNotPublic)) {
		b.cache.mu.Lock()
		b.cache.originals[target] = announceResult{status: original, err: err}
		b.cache.mu.Unlock()
	}
	return original, err
}

type profileBackfiller struct {
	ctx     context.Context
	r       *Resolver
	account *models.Account
	cache   *backfillCache
}

// objectID returns the id of an object reference: a string or {"id": ...}.
func objectID(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]any:
		id, _ := t["id"].(string)
		return id
	}
	return ""
}

// fetchOwn fetches uri, which must be hosted by the account's server.
func (b *profileBackfiller) fetchOwn(uri string) (map[string]any, error) {
	if !sameURIOrigin(uri, b.account.OriginId) {
		return nil, fmt.Errorf("%s is not hosted by the actor's server", uri)
	}
	if b.cache == nil {
		return b.r.client.FetchObject(b.ctx, uri)
	}
	b.cache.mu.Lock()
	res, ok := b.cache.objects[uri]
	b.cache.mu.Unlock()
	if ok {
		return res.obj, res.err
	}
	obj, err := b.r.client.FetchObject(b.ctx, uri)
	// Do not cache failures caused by this caller's own deadline: another
	// phase with a live context may still succeed.
	if err == nil || b.ctx.Err() == nil {
		b.cache.mu.Lock()
		b.cache.objects[uri] = fetchResult{obj: obj, err: err}
		b.cache.mu.Unlock()
	}
	return obj, err
}

// collectionItems reads up to limit items from a collection, following
// "first" for paged collections. total is -1 when unknown.
func (b *profileBackfiller) collectionItems(uri string, limit int) ([]any, int, error) {
	coll, err := b.fetchOwn(uri)
	if err != nil {
		return nil, -1, err
	}
	total := -1
	if n, ok := coll["totalItems"].(float64); ok && n >= 0 && n < 1<<31 {
		total = int(n)
	}
	items := collectionPageItems(coll)
	if items == nil {
		switch first := coll["first"].(type) {
		case map[string]any:
			items = collectionPageItems(first)
			if items == nil {
				if next := objectID(first["next"]); next != "" {
					page, err := b.fetchOwn(next)
					if err != nil {
						return nil, total, err
					}
					items = collectionPageItems(page)
				}
			}
		case string:
			page, err := b.fetchOwn(first)
			if err != nil {
				return nil, total, err
			}
			items = collectionPageItems(page)
		}
	}
	if len(items) > limit {
		items = items[:limit]
	}
	return items, total, nil
}

// outboxItems walks the outbox newest first, following "next", until it has
// profileBackfillItems non-reply original posts or hits the page/item caps.
// Accounts whose recent activity is all replies otherwise get an empty
// Posts tab. Errors after the first page keep what was collected.
func (b *profileBackfiller) outboxItems(uri string) ([]any, int, error) {
	coll, err := b.fetchOwn(uri)
	if err != nil {
		return nil, -1, err
	}
	total := -1
	if n, ok := coll["totalItems"].(float64); ok && n >= 0 && n < 1<<31 {
		total = int(n)
	}
	var items []any
	originals := 0
	add := func(page []any) {
		for _, it := range page {
			if len(items) >= profileBackfillMaxItems {
				return
			}
			original := isOriginalCreate(it)
			if len(items) >= profileBackfillReplyItems && !original && !isAnnounce(it) {
				continue
			}
			items = append(items, it)
			if original {
				originals++
			}
		}
	}
	page := coll
	pageItems := collectionPageItems(coll)
	next := ""
	if pageItems == nil {
		switch first := coll["first"].(type) {
		case map[string]any:
			page, pageItems = first, collectionPageItems(first)
			if pageItems == nil {
				next = objectID(first["next"])
			}
		case string:
			next = first
		}
	}
	fetched := 0
	if pageItems != nil {
		add(pageItems)
		fetched = 1
		next = objectID(page["next"])
	}
	seen := map[string]bool{uri: true}
	for next != "" && !seen[next] && fetched < profileBackfillMaxPages && originals < profileBackfillItems && len(items) < profileBackfillMaxItems {
		seen[next] = true
		if fetched > 0 {
			select {
			case <-b.ctx.Done():
				return items, total, nil
			case <-time.After(profileBackfillPageDelay):
			}
		}
		p, err := b.fetchOwn(next)
		if err != nil {
			if fetched == 0 {
				return nil, total, err
			}
			break
		}
		fetched++
		add(collectionPageItems(p))
		next = objectID(p["next"])
	}
	return items, total, nil
}

func isAnnounce(item any) bool {
	m, ok := item.(map[string]any)
	return ok && stringField(m, "type") == "Announce"
}

// isOriginalCreate reports a Create whose embedded object is not a reply.
func isOriginalCreate(item any) bool {
	m, ok := item.(map[string]any)
	if !ok || stringField(m, "type") != "Create" {
		return false
	}
	obj, ok := m["object"].(map[string]any)
	if !ok {
		return false
	}
	return objectID(obj["inReplyTo"]) == ""
}

func collectionPageItems(page map[string]any) []any {
	for _, key := range []string{"orderedItems", "items"} {
		switch v := page[key].(type) {
		case []any:
			return v
		case map[string]any, string:
			return []any{v}
		}
	}
	return nil
}

// ownNote returns the Note an item refers to, fetching it when the item is
// only a reference. The note must be public or unlisted and written by the
// account.
func (b *profileBackfiller) ownNote(item any) (map[string]any, string, error) {
	obj, _ := item.(map[string]any)
	id := objectID(item)
	if id == "" || validateFederationURI(id) != nil || !sameURIOrigin(id, b.account.OriginId) {
		return nil, "", errors.New("note is not hosted by the actor's server")
	}
	if obj == nil || !statusObjectTypes[stringField(obj, "type")] || obj["attributedTo"] == nil {
		fetched, err := b.fetchOwn(id)
		if err != nil {
			return nil, "", err
		}
		if objectID(fetched) != id {
			return nil, "", errors.New("note identity mismatch")
		}
		obj = fetched
	}
	if !statusObjectTypes[stringField(obj, "type")] {
		return nil, "", fmt.Errorf("unsupported object type %q", stringField(obj, "type"))
	}
	if firstActorID(obj["attributedTo"]) != b.account.OriginId {
		return nil, "", errors.New("note is not written by the actor")
	}
	if getVisibilityFromActivity(obj) > models.StatusVisibilityUnlisted {
		return nil, "", errStatusNotPublic
	}
	return obj, id, nil
}

func stringField(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// importNote stores the account's own note unless it is already known.
func (b *profileBackfiller) importNote(item any) (*models.Status, error) {
	if id := objectID(item); id != "" {
		if existing, err := findInboundParent(b.ctx, id); err == nil && existing != nil && existing.Id != 0 {
			if existing.Visibility > models.StatusVisibilityUnlisted || existing.AccountId != b.account.Id {
				return nil, errStatusNotPublic
			}
			return existing, nil
		}
	}
	obj, id, err := b.ownNote(item)
	if err != nil {
		return nil, err
	}
	status, err := b.r.createStatusFromObject(obj, id) //nolint:contextcheck
	if err != nil {
		return nil, err
	}
	return &status, nil
}

func (b *profileBackfiller) importOutboxItem(item any) error {
	activity, _ := item.(map[string]any)
	if activity == nil {
		// A bare reference: fetch the activity from the actor's server.
		uri, _ := item.(string)
		fetched, err := b.fetchOwn(uri)
		if err != nil {
			return err
		}
		if objectID(fetched) != uri {
			return errors.New("activity identity mismatch")
		}
		activity = fetched
	}
	switch stringField(activity, "type") {
	case "Create":
		if firstActorID(activity["actor"]) != b.account.OriginId {
			return errors.New("activity actor mismatch")
		}
		_, err := b.importNote(activity["object"])
		return err
	case "Announce":
		return b.importAnnounce(activity)
	default:
		if statusObjectTypes[stringField(activity, "type")] {
			_, err := b.importNote(activity)
			return err
		}
		return fmt.Errorf("unsupported activity %q", stringField(activity, "type"))
	}
}

// importAnnounce stores a boost by the account of a public post.
func (b *profileBackfiller) importAnnounce(activity map[string]any) error {
	activityID := objectID(activity)
	if activityID == "" || !sameURIOrigin(activityID, b.account.OriginId) || firstActorID(activity["actor"]) != b.account.OriginId {
		return errors.New("announce is not by the actor")
	}
	if getVisibilityFromActivity(activity) > models.StatusVisibilityUnlisted {
		return errStatusNotPublic
	}
	conn := db.GetDB().WithContext(b.ctx)
	var existing int64
	if err := conn.Model(&models.Status{}).Where("origin_uri = ?", activityID).Count(&existing).Error; err != nil || existing > 0 {
		return err
	}
	original, err := b.announceTarget(activity)
	if err != nil {
		return err
	}
	if original.LocalOnly || original.DeletedAt != nil || (original.ReblogId != nil && *original.ReblogId != 0) {
		return errors.New("announced post cannot be boosted")
	}
	var author models.Account
	if err := conn.Select("id", "domain", "is_ban").First(&author, original.AccountId).Error; err != nil {
		return err
	}
	if author.IsBan {
		return errors.New("announced post author is banned")
	}
	if author.Domain != nil {
		if suspended, err := hostSuspended(*author.Domain); err != nil || suspended {
			return errors.New("announced post author is suspended")
		}
	}
	now := time.Now()
	published := utils.ParseRFC3339(stringField(activity, "published"))
	if published == nil {
		published = &now
	}
	reblog := models.Status{
		AccountId:  b.account.Id,
		ReblogId:   &original.Id,
		Visibility: getVisibilityFromActivity(activity),
		OriginUri:  &activityID,
		Domain:     b.account.Domain,
		CreatedAt:  published,
		UpdatedAt:  &now,
	}
	reblog.MarkProfileBackfill()
	return conn.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&reblog).Error; err != nil {
			return err
		}
		return tx.Model(&models.Status{}).Where("id = ?", original.Id).UpdateColumn("reblogs_count", gorm.Expr("reblogs_count + ?", 1)).Error
	})
}

// syncPins replaces the account's pins with the featured collection items.
func (b *profileBackfiller) syncPins(items []any) error {
	ids := make([]uint64, 0, len(items))
	for _, item := range items {
		status, err := b.importNote(item)
		if err != nil {
			log.Debug("profile backfill: skipping featured item: %v", err)
			continue
		}
		ids = append(ids, status.Id)
	}
	return db.GetDB().WithContext(b.ctx).Transaction(func(tx *gorm.DB) error {
		stale := tx.Where("account_id = ?", b.account.Id)
		if len(ids) > 0 {
			stale = stale.Where("status_id NOT IN ?", ids)
		}
		if err := stale.Delete(&models.PinStatus{}).Error; err != nil {
			return err
		}
		var have []uint64
		if err := tx.Model(&models.PinStatus{}).Where("account_id = ?", b.account.Id).Pluck("status_id", &have).Error; err != nil {
			return err
		}
		known := map[uint64]bool{}
		for _, id := range have {
			known[id] = true
		}
		// Featured lists the newest pin first; create in reverse so
		// created_at keeps that order.
		for i := len(ids) - 1; i >= 0; i-- {
			if known[ids[i]] {
				continue
			}
			known[ids[i]] = true
			if err := tx.Create(&models.PinStatus{AccountId: b.account.Id, StatusId: ids[i]}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
