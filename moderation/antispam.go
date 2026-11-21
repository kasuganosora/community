package moderation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"sync"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/redis/go-redis/v9"
)

// ErrSpamDuplicate is returned when an account posts identical normalized text
// too many times within the deduplication window.
var ErrSpamDuplicate = errors.New("duplicate content posted too frequently, please slow down")

// ErrSpamFlood is returned when an account or reply target exceeds its
// behavioral anti-spam velocity limit.
var ErrSpamFlood = errors.New("posting too frequently, please slow down")

// AntiSpamConfig configures behavioral duplicate-content and reply-flood protection
// (inspired by openBshi/app/service/main/antispam).
type AntiSpamConfig struct {
	Enabled           bool
	DuplicateWindow   time.Duration // Window for identical normalized content per account
	DuplicateMaxCount int           // Allowed identical posts inside DuplicateWindow
	ReplyFloodWindow  time.Duration // Window for replies targeting the same parent status
	ReplyFloodMax     int           // Allowed replies per account to the same parent status inside ReplyFloodWindow
}

// DefaultAntiSpamConfig returns conservative defaults that stop automated spam
// bursts without interfering with normal posting or integration tests.
func DefaultAntiSpamConfig() AntiSpamConfig {
	return AntiSpamConfig{
		Enabled:           true,
		DuplicateWindow:   3 * time.Minute,
		DuplicateMaxCount: 3,
		ReplyFloodWindow:  1 * time.Minute,
		ReplyFloodMax:     10,
	}
}

type spamCounterEntry struct {
	count     int
	expiresAt int64
}

type spamMemShard struct {
	mu      sync.Mutex
	entries map[string]spamCounterEntry
}

// AntiSpamGuard enforces behavioral duplicate-content and flood limits backed
// by Redis with an automatic 64-shard in-memory fallback when Redis is absent.
type AntiSpamGuard struct {
	cfg    AntiSpamConfig
	shards [64]spamMemShard
}

// NewAntiSpamGuard creates an AntiSpamGuard with the given configuration.
func NewAntiSpamGuard(cfg AntiSpamConfig) *AntiSpamGuard {
	g := &AntiSpamGuard{cfg: cfg}
	for i := range g.shards {
		g.shards[i].entries = make(map[string]spamCounterEntry)
	}
	return g
}

var defaultAntiSpamGuard = NewAntiSpamGuard(DefaultAntiSpamConfig())

// ResetAntiSpamForTest clears in-memory anti-spam counters (tests only).
func ResetAntiSpamForTest() {
	for i := range defaultAntiSpamGuard.shards {
		s := &defaultAntiSpamGuard.shards[i]
		s.mu.Lock()
		clear(s.entries)
		s.mu.Unlock()
	}
}

// CheckStatusAntiSpam checks whether accountID posting text (optionally replying
// to replyToID) violates duplicate-content or reply-flood limits.
func CheckStatusAntiSpam(ctx context.Context, accountID uint64, replyToID uint64, text string) error {
	return defaultAntiSpamGuard.CheckStatus(ctx, accountID, replyToID, text)
}

const antiSpamIncrScript = `
local c = redis.call('INCR', KEYS[1])
if c == 1 then
	redis.call('PEXPIRE', KEYS[1], tonumber(ARGV[1]))
end
return c
`

// CheckStatus evaluates duplicate content and reply-target flood rules.
func (g *AntiSpamGuard) CheckStatus(ctx context.Context, accountID uint64, replyToID uint64, text string) error {
	if g == nil || !g.cfg.Enabled || accountID == 0 {
		return nil
	}

	// 1. Duplicate normalized content fingerprint check (skip very short texts like "ok").
	norm := NormalizeForFilter(text)
	if g.cfg.DuplicateMaxCount > 0 && len([]rune(norm)) >= 6 {
		sum := sha256.Sum256([]byte(norm))
		fp := hex.EncodeToString(sum[:8])
		key := fmt.Sprintf("antispam:dup:%d:%s", accountID, fp)
		count := g.increment(ctx, key, g.cfg.DuplicateWindow)
		if count > g.cfg.DuplicateMaxCount {
			return ErrSpamDuplicate
		}
	}

	// 2. Per-parent reply flood check.
	if replyToID > 0 && g.cfg.ReplyFloodMax > 0 {
		key := fmt.Sprintf("antispam:reply:%d:%d", accountID, replyToID)
		count := g.increment(ctx, key, g.cfg.ReplyFloodWindow)
		if count > g.cfg.ReplyFloodMax {
			return ErrSpamFlood
		}
	}

	return nil
}

func (g *AntiSpamGuard) increment(ctx context.Context, key string, window time.Duration) int {
	if window <= 0 {
		window = time.Minute
	}
	client := safeUniversalRedis()
	if client != nil {
		if ctx == nil {
			ctx = context.Background()
		}
		rctx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
		defer cancel()
		val, err := client.Eval(rctx, antiSpamIncrScript, []string{key}, window.Milliseconds()).Int()
		if err == nil {
			return val
		}
	}
	return g.incrementMemory(key, window)
}

func safeUniversalRedis() (c redis.UniversalClient) {
	defer func() {
		if recover() != nil {
			c = nil
		}
	}()
	return db.GetUniversalRedis()
}

func (g *AntiSpamGuard) incrementMemory(key string, window time.Duration) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	s := &g.shards[h.Sum32()&63]

	now := time.Now().UnixNano()
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.entries[key]
	if !ok || now >= entry.expiresAt {
		if len(s.entries) > 2048 {
			for k, v := range s.entries {
				if now >= v.expiresAt {
					delete(s.entries, k)
				}
			}
		}
		s.entries[key] = spamCounterEntry{count: 1, expiresAt: now + int64(window)}
		return 1
	}
	entry.count++
	s.entries[key] = entry
	return entry.count
}
