package avatarcache

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/utils"
)

var (
	defaultOnce  sync.Once
	defaultCache *Cache
)

// Enabled reports AVATAR_CACHE_ENABLED (default true).
func Enabled() bool {
	v := strings.ToLower(strings.TrimSpace(config.C.AvatarCacheEnabled))
	return v == "" || !(v == "false" || v == "0" || v == "off" || v == "no")
}

// RemoteTTL is AVATAR_CACHE_REMOTE_TTL (default 24h).
func RemoteTTL() time.Duration {
	d, err := time.ParseDuration(strings.TrimSpace(config.C.AvatarCacheRemoteTTL))
	if err != nil || d <= 0 {
		return 24 * time.Hour
	}
	return d
}

// Default returns the process-wide cache, or nil when disabled or the
// directory cannot be created (callers then serve uncached). The janitor
// starts with it.
func Default() *Cache {
	if !Enabled() {
		return nil
	}
	defaultOnce.Do(func() {
		dir := strings.TrimSpace(config.C.AvatarCacheDir)
		if dir == "" {
			dir = "avatar-cache"
		}
		limit, _ := strconv.ParseInt(strings.TrimSpace(config.C.AvatarCacheMaxBytes), 10, 64)
		c, err := New(utils.ResolveAppPath(dir), limit)
		if err != nil {
			log.Warn("avatar cache disabled: %v", err)
			return
		}
		defaultCache = c
		go c.Janitor(context.Background())
	})
	return defaultCache
}
