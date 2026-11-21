package history

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/resilience"
	"github.com/redis/go-redis/v9"
)

const (
	// MaxHistoryItems bounds per-account browsing history length.
	MaxHistoryItems = 500
	historyTTL      = 30 * 24 * time.Hour
	maxMemAccounts  = 1024
)

// Entry represents a single status view event.
type Entry struct {
	StatusID uint64
	ViewedAt int64 // Unix milliseconds
	seq      int64 // Strictly monotonic microsecond sequence (< 2^53, exact in Lua double)
}

var lastMonoMicro atomic.Int64

func nextMonoMicro() int64 {
	now := time.Now().UnixMicro()
	for {
		prev := lastMonoMicro.Load()
		next := now
		if next <= prev {
			next = prev + 1
		}
		if lastMonoMicro.CompareAndSwap(prev, next) {
			return next
		}
	}
}

// FormatCursor encodes a tie-safe pagination cursor "<viewedAtMs>:<statusId>".
func FormatCursor(e Entry) string {
	return strconv.FormatInt(e.ViewedAt, 10) + ":" + strconv.FormatUint(e.StatusID, 10)
}

// ParseCursor parses "<viewedAtMs>:<statusId>" or legacy "<viewedAtMs>".
func ParseCursor(raw string) (maxScoreMs int64, afterStatusID uint64) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, 0
	}
	if left, right, ok := strings.Cut(raw, ":"); ok {
		ms, err1 := strconv.ParseInt(left, 10, 64)
		sid, err2 := strconv.ParseUint(right, 10, 64)
		if err1 == nil && err2 == nil && ms > 0 {
			return ms, sid
		}
		return 0, 0
	}
	ms, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || ms <= 0 {
		return 0, 0
	}
	return ms, 0
}

var (
	pipelineMu sync.Mutex
	batcher    *resilience.Pipeline[Entry]

	memMu       sync.Mutex
	memAccounts = make(map[uint64]*memAccountHistory)
)

type memAccountHistory struct {
	paused     bool
	clearedSeq int64
	items      map[uint64]int64
}

// ResetForTest clears in-memory history state and flushes/resets the batcher (tests only).
func ResetForTest() {
	Close()
	memMu.Lock()
	clear(memAccounts)
	memMu.Unlock()
}

func getOrCreateMemAccountLocked(accountID uint64) *memAccountHistory {
	acct := memAccounts[accountID]
	if acct != nil {
		return acct
	}
	if len(memAccounts) >= maxMemAccounts {
		for k := range memAccounts {
			delete(memAccounts, k)
			break
		}
	}
	acct = &memAccountHistory{items: make(map[uint64]int64)}
	memAccounts[accountID] = acct
	return acct
}

func historyKey(accountID uint64) string {
	return "history:status:" + strconv.FormatUint(accountID, 10)
}

func pausedKey(accountID uint64) string {
	return "history:paused:" + strconv.FormatUint(accountID, 10)
}

func clearedKey(accountID uint64) string {
	return "history:cleared:" + strconv.FormatUint(accountID, 10)
}

func safeRedis() (c redis.UniversalClient) {
	defer func() {
		if recover() != nil {
			c = nil
		}
	}()
	return db.GetUniversalRedis()
}

func getBatcher() *resilience.Pipeline[Entry] {
	pipelineMu.Lock()
	defer pipelineMu.Unlock()
	if batcher == nil {
		batcher = resilience.NewPipeline[Entry](resilience.PipelineConfig{
			MaxSize:  128,
			Interval: 200 * time.Millisecond,
			Buffer:   2048,
			Workers:  4,
			Smooth:   true,
		}, flushHistoryBatch)
	}
	return batcher
}

// Close flushes all pending asynchronous browsing history writes (called on shutdown/tests).
func Close() {
	pipelineMu.Lock()
	p := batcher
	batcher = nil
	pipelineMu.Unlock()
	if p != nil {
		_ = p.Close()
	}
}

// flushBatchLua atomically skips writing if history recording is paused (KEYS[2])
// or if an entry predates the account's last Clear sequence (KEYS[3]).
const flushBatchLua = `
if redis.call('EXISTS', KEYS[2]) == 1 then
	return 0
end
local cleared = tonumber(redis.call('GET', KEYS[3]) or '0')
local added = 0
for i = 3, #ARGV, 3 do
	local ts = tonumber(ARGV[i])
	local seq = tonumber(ARGV[i+1])
	local member = ARGV[i+2]
	local cur = tonumber(redis.call('ZSCORE', KEYS[1], member) or '0')
	if seq > cleared and ts >= cur then
		redis.call('ZADD', KEYS[1], ts, member)
		added = added + 1
	end
end
if added > 0 then
	redis.call('ZREMRANGEBYRANK', KEYS[1], 0, -(tonumber(ARGV[1]) + 1))
	redis.call('PEXPIRE', KEYS[1], tonumber(ARGV[2]))
end
return added
`

func flushHistoryBatch(_ context.Context, _ int, batch map[string][]Entry) {
	if len(batch) == 0 {
		return
	}
	client := safeRedis()
	if client == nil {
		memMu.Lock()
		for rawAcctID, entries := range batch {
			accountID, err := strconv.ParseUint(rawAcctID, 10, 64)
			if err != nil || accountID == 0 {
				continue
			}
			acct := getOrCreateMemAccountLocked(accountID)
			if acct.paused {
				continue
			}
			for _, e := range entries {
				seq := e.seq
				if seq == 0 {
					seq = e.ViewedAt * 1000
				}
				if e.StatusID == 0 || seq <= acct.clearedSeq {
					continue
				}
				if prev, ok := acct.items[e.StatusID]; !ok || e.ViewedAt >= prev {
					acct.items[e.StatusID] = e.ViewedAt
				}
			}
			trimMemAccountLocked(acct)
		}
		memMu.Unlock()
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	pipe := client.Pipeline()
	queued := 0
	for rawAcctID, entries := range batch {
		accountID, err := strconv.ParseUint(rawAcctID, 10, 64)
		if err != nil || accountID == 0 {
			continue
		}
		latest := make(map[uint64]Entry, len(entries))
		for _, e := range entries {
			if e.StatusID == 0 {
				continue
			}
			if e.seq == 0 {
				e.seq = e.ViewedAt * 1000
			}
			if prev, ok := latest[e.StatusID]; !ok || e.seq > prev.seq {
				latest[e.StatusID] = e
			}
		}
		if len(latest) == 0 {
			continue
		}
		argv := make([]any, 0, 2+len(latest)*3)
		argv = append(argv, MaxHistoryItems, historyTTL.Milliseconds())
		for sid, e := range latest {
			argv = append(argv, e.ViewedAt, e.seq, strconv.FormatUint(sid, 10))
		}
		pipe.Eval(ctx, flushBatchLua, []string{
			historyKey(accountID),
			pausedKey(accountID),
			clearedKey(accountID),
		}, argv...)
		queued++
	}
	if queued > 0 {
		_, _ = pipe.Exec(ctx)
	}
}

func trimMemAccountLocked(acct *memAccountHistory) {
	if len(acct.items) <= MaxHistoryItems {
		return
	}
	all := make([]Entry, 0, len(acct.items))
	for sid, ts := range acct.items {
		all = append(all, Entry{StatusID: sid, ViewedAt: ts})
	}
	sortEntriesDesc(all)
	for _, stale := range all[MaxHistoryItems:] {
		delete(acct.items, stale.StatusID)
	}
}

func sortEntriesDesc(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].ViewedAt != entries[j].ViewedAt {
			return entries[i].ViewedAt > entries[j].ViewedAt
		}
		return entries[i].StatusID > entries[j].StatusID
	})
}

// IsPaused reports whether the user has paused browsing history recording.
func IsPaused(ctx context.Context, accountID uint64) (bool, error) {
	if accountID == 0 {
		return true, nil
	}
	client := safeRedis()
	if client == nil {
		memMu.Lock()
		acct := memAccounts[accountID]
		paused := acct != nil && acct.paused
		memMu.Unlock()
		return paused, nil
	}
	val, err := client.Get(ctx, pausedKey(accountID)).Result()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return val == "1", nil
}

// SetPaused toggles browsing history recording for accountID.
func SetPaused(ctx context.Context, accountID uint64, paused bool) error {
	if accountID == 0 {
		return nil
	}
	client := safeRedis()
	if client == nil {
		memMu.Lock()
		getOrCreateMemAccountLocked(accountID).paused = paused
		memMu.Unlock()
		return nil
	}
	if paused {
		return client.Set(ctx, pausedKey(accountID), "1", 0).Err()
	}
	return client.Del(ctx, pausedKey(accountID)).Err()
}

// RecordAsync enqueues a status view into the sharded micro-batching pipeline
// with zero network I/O on the HTTP response path. Pause and clear checks are
// enforced atomically when the micro-batch flushes.
func RecordAsync(accountID, statusID uint64) {
	if accountID == 0 || statusID == 0 {
		return
	}
	seq := nextMonoMicro()
	_ = getBatcher().Add(strconv.FormatUint(accountID, 10), Entry{
		StatusID: statusID,
		ViewedAt: time.Now().UnixMilli(),
		seq:      seq,
	})
}

// RecordSync immediately persists a status view (used by explicit POST /api/v1/history).
func RecordSync(ctx context.Context, accountID, statusID uint64) error {
	if accountID == 0 || statusID == 0 {
		return nil
	}
	seq := nextMonoMicro()
	nowMs := time.Now().UnixMilli()
	client := safeRedis()
	if client == nil {
		memMu.Lock()
		acct := getOrCreateMemAccountLocked(accountID)
		if !acct.paused && seq > acct.clearedSeq {
			acct.items[statusID] = nowMs
			trimMemAccountLocked(acct)
		}
		memMu.Unlock()
		return nil
	}
	return client.Eval(ctx, flushBatchLua, []string{
		historyKey(accountID),
		pausedKey(accountID),
		clearedKey(accountID),
	}, MaxHistoryItems, historyTTL.Milliseconds(), nowMs, seq, strconv.FormatUint(statusID, 10)).Err()
}

// List returns recently viewed status IDs (newest first) with legacy millisecond cursor.
func List(ctx context.Context, accountID uint64, maxScoreMs int64, limit int) ([]Entry, error) {
	return ListCursor(ctx, accountID, maxScoreMs, 0, limit)
}

// ListCursor returns recently viewed status IDs ordered by (ViewedAt DESC, StatusID DESC)
// strictly after (maxScoreMs, afterStatusID) when maxScoreMs > 0.
func ListCursor(ctx context.Context, accountID uint64, maxScoreMs int64, afterStatusID uint64, limit int) ([]Entry, error) {
	if accountID == 0 || limit <= 0 {
		return []Entry{}, nil
	}
	client := safeRedis()
	if client == nil {
		memMu.Lock()
		acct := memAccounts[accountID]
		if acct == nil || len(acct.items) == 0 {
			memMu.Unlock()
			return []Entry{}, nil
		}
		all := make([]Entry, 0, len(acct.items))
		for sid, ts := range acct.items {
			all = append(all, Entry{StatusID: sid, ViewedAt: ts})
		}
		memMu.Unlock()
		sortEntriesDesc(all)
		return filterEntriesByCursor(all, maxScoreMs, afterStatusID, limit), nil
	}

	maxStr := "+inf"
	fetchCount := int64(limit)
	if maxScoreMs > 0 {
		if afterStatusID > 0 {
			maxStr = strconv.FormatInt(maxScoreMs, 10)
			fetchCount = int64(limit + 32)
		} else {
			maxStr = fmt.Sprintf("(%d", maxScoreMs)
		}
	}
	zs, err := client.ZRevRangeByScoreWithScores(ctx, historyKey(accountID), &redis.ZRangeBy{
		Min:    "-inf",
		Max:    maxStr,
		Offset: 0,
		Count:  fetchCount,
	}).Result()
	if err != nil {
		return nil, err
	}
	parsed := make([]Entry, 0, len(zs))
	for _, z := range zs {
		id, parseErr := strconv.ParseUint(fmt.Sprint(z.Member), 10, 64)
		if parseErr != nil || id == 0 {
			continue
		}
		parsed = append(parsed, Entry{StatusID: id, ViewedAt: int64(z.Score)})
	}
	sortEntriesDesc(parsed)
	return filterEntriesByCursor(parsed, maxScoreMs, afterStatusID, limit), nil
}

func filterEntriesByCursor(sorted []Entry, maxScoreMs int64, afterStatusID uint64, limit int) []Entry {
	out := make([]Entry, 0, min(limit, len(sorted)))
	for _, e := range sorted {
		if maxScoreMs > 0 {
			if afterStatusID > 0 {
				if e.ViewedAt > maxScoreMs || (e.ViewedAt == maxScoreMs && e.StatusID >= afterStatusID) {
					continue
				}
			} else if e.ViewedAt >= maxScoreMs {
				continue
			}
		}
		out = append(out, e)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// Remove deletes a single status from the account's browsing history.
func Remove(ctx context.Context, accountID, statusID uint64) error {
	if accountID == 0 || statusID == 0 {
		return nil
	}
	client := safeRedis()
	if client == nil {
		memMu.Lock()
		if acct := memAccounts[accountID]; acct != nil {
			delete(acct.items, statusID)
		}
		memMu.Unlock()
		return nil
	}
	return client.ZRem(ctx, historyKey(accountID), strconv.FormatUint(statusID, 10)).Err()
}

// Clear removes all browsing history entries for accountID and records a
// watermark so earlier in-flight async batches cannot resurrect cleared views.
func Clear(ctx context.Context, accountID uint64) error {
	if accountID == 0 {
		return nil
	}
	seq := nextMonoMicro()
	client := safeRedis()
	if client == nil {
		memMu.Lock()
		acct := getOrCreateMemAccountLocked(accountID)
		acct.clearedSeq = seq
		clear(acct.items)
		memMu.Unlock()
		return nil
	}
	pipe := client.Pipeline()
	pipe.Del(ctx, historyKey(accountID))
	pipe.Set(ctx, clearedKey(accountID), strconv.FormatInt(seq, 10), historyTTL)
	_, err := pipe.Exec(ctx)
	return err
}
