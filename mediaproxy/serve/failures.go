package serve

import (
	"context"
	"io"
	"time"

	"github.com/kasuganosora/community/cache"
	"github.com/kasuganosora/community/db/redis"
)

// failureStore shares the failure memo between processes and restarts.
type failureStore interface {
	mark(key string)
	failed(key string) bool
}

const redisFailureTimeout = 100 * time.Millisecond

type redisFailureStore struct{ c *cache.Cache }

func (s redisFailureStore) mark(key string) {
	ctx, cancel := context.WithTimeout(context.Background(), redisFailureTimeout)
	defer cancel()
	_ = s.c.SetString(ctx, "mediaproxy:fail:"+key, "1", failureTTL)
}

func (s redisFailureStore) failed(key string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), redisFailureTimeout)
	defer cancel()
	ok, err := s.c.Exists(ctx, "mediaproxy:fail:"+key)
	return err == nil && ok
}

func redisFailures() failureStore {
	if len(redis.GetRedises()) == 0 {
		return nil
	}
	return redisFailureStore{c: cache.GetCache()}
}

// stallReader re-arms timer on every read, so a relay is cancelled only
// when the upstream stops sending, not when a long video simply plays.
type stallReader struct {
	r     io.Reader
	timer *time.Timer
}

func (s *stallReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if n > 0 {
		s.timer.Reset(stallTimeout)
	}
	return n, err
}
