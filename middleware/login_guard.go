package middleware

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Login failure throttling: failed password attempts are counted per
// submitted account identifier and per client IP in a fixed window. Once
// either counter reaches the limit, further login attempts for that
// account/IP are refused with 429 until the window expires. Successful logins
// are never counted.
const (
	LoginFailureLimit  = 10
	LoginFailureWindow = 5 * time.Minute
)

type loginFailEntry struct {
	count int
	reset time.Time
}

var (
	loginFailMu  sync.Mutex
	loginFailMem = map[string]*loginFailEntry{}
)

func loginFailKeys(identifier, ip string) []string {
	return []string{
		"loginfail:acct:" + strings.ToLower(strings.TrimSpace(identifier)),
		"loginfail:ip:" + ip,
	}
}

// LoginBlocked reports whether the account or IP exceeded the failure limit
// and, if so, the seconds until the block lifts.
func LoginBlocked(identifier, ip string) (bool, int) {
	client := GetRedisClient()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, k := range loginFailKeys(identifier, ip) {
		if client != nil {
			n, err := client.Get(ctx, k).Int()
			if err == nil && n >= LoginFailureLimit {
				ttl, _ := client.TTL(ctx, k).Result()
				return true, retrySeconds(ttl)
			}
			continue
		}
		loginFailMu.Lock()
		e := loginFailMem[k]
		if e != nil && time.Now().Before(e.reset) && e.count >= LoginFailureLimit {
			ttl := time.Until(e.reset)
			loginFailMu.Unlock()
			return true, retrySeconds(ttl)
		}
		loginFailMu.Unlock()
	}

	return false, 0
}

// RecordLoginFailure counts one failed attempt for the account and the IP.
func RecordLoginFailure(identifier, ip string) {
	client := GetRedisClient()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, k := range loginFailKeys(identifier, ip) {
		if client != nil {
			pipe := client.TxPipeline()
			pipe.Incr(ctx, k)
			pipe.ExpireNX(ctx, k, LoginFailureWindow)
			if _, err := pipe.Exec(ctx); err == nil || err == redis.Nil {
				continue
			}
		}
		loginFailMu.Lock()
		now := time.Now()
		e := loginFailMem[k]
		if e == nil || now.After(e.reset) {
			e = &loginFailEntry{reset: now.Add(LoginFailureWindow)}
			loginFailMem[k] = e
		}
		e.count++
		if len(loginFailMem) > 100000 {
			for mk, me := range loginFailMem {
				if now.After(me.reset) {
					delete(loginFailMem, mk)
				}
			}
		}
		loginFailMu.Unlock()
	}
}

func retrySeconds(d time.Duration) int {
	s := int(d.Seconds()) + 1
	if s < 1 || s > int(LoginFailureWindow.Seconds())+1 {
		s = int(LoginFailureWindow.Seconds())
	}

	return s
}
