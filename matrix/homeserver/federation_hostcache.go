package homeserver

import (
	"sync"
	"time"
)

// penaltyBox is a small bounded negative cache keyed by host (or host+key).
// It lets repeated attempts skip peers that just failed instead of paying a
// connect/DNS/TLS timeout every time.
type penaltyBox struct {
	mu      sync.Mutex
	entries map[string]penaltyEntry
	max     int
}

type penaltyEntry struct {
	until  time.Time
	reason string
}

func newPenaltyBox(max int) *penaltyBox {
	return &penaltyBox{entries: make(map[string]penaltyEntry), max: max}
}

func (p *penaltyBox) mark(key string, ttl time.Duration, reason string) {
	if ttl <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.entries) >= p.max {
		now := time.Now()
		for k, e := range p.entries {
			if now.After(e.until) {
				delete(p.entries, k)
			}
		}
		if len(p.entries) >= p.max {
			clear(p.entries)
		}
	}
	p.entries[key] = penaltyEntry{until: time.Now().Add(ttl), reason: reason}
}

// active reports whether key is still penalised, and why.
func (p *penaltyBox) active(key string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.entries[key]
	if !ok {
		return "", false
	}
	if time.Now().After(e.until) {
		delete(p.entries, key)
		return "", false
	}
	return e.reason, true
}

func (p *penaltyBox) clear(key string) {
	p.mu.Lock()
	delete(p.entries, key)
	p.mu.Unlock()
}

func (p *penaltyBox) reset() {
	p.mu.Lock()
	clear(p.entries)
	p.mu.Unlock()
}

// deadFederationHosts remembers servers whose federation endpoint recently
// failed at the network level (DNS, refused, TLS, timeouts, 5xx/Cloudflare
// 52x), so room joins stop re-trying them for deadHostTTL.
var deadFederationHosts = newPenaltyBox(2048)

// keyFetchFailures remembers servers whose signing keys could not be fetched
// (directly and through notaries) so a join response citing hundreds of
// events from one dead server costs one failure, not hundreds.
var keyFetchFailures = newPenaltyBox(4096)
