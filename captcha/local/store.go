package local

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/redis/go-redis/v9"
)

// ErrNotFound is a missing or expired key.
var ErrNotFound = errors.New("captcha key not found")

// Store is the challenge and pass-token backend.
type Store interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Put(ctx context.Context, key string, val []byte, ttl time.Duration) error
	Del(ctx context.Context, key string) (int64, error)
	Incr(ctx context.Context, key string, window time.Duration) (int64, error)
	// Update read-modify-writes one key. Returning nil bytes deletes it.
	// A non-nil error from fn aborts the write.
	Update(ctx context.Context, key string, ttl time.Duration, fn func([]byte) ([]byte, error)) error
}

var (
	storeMu sync.RWMutex
	store   Store
)

func getStore() Store {
	storeMu.RLock()
	current := store
	storeMu.RUnlock()
	if current != nil {
		return current
	}
	storeMu.Lock()
	defer storeMu.Unlock()
	if store == nil {
		store = newRedisStore(db.GetRedis())
	}
	return store
}

// UseStore replaces the backend. Tests use a memory store.
func UseStore(s Store) {
	storeMu.Lock()
	store = s
	storeMu.Unlock()
}

type redisStore struct{ c *redis.Client }

func newRedisStore(c *redis.Client) Store { return &redisStore{c: c} }

func (s *redisStore) Get(ctx context.Context, key string) ([]byte, error) {
	val, err := s.c.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrNotFound
	}
	return val, err
}

func (s *redisStore) Put(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	return s.c.Set(ctx, key, val, ttl).Err()
}

func (s *redisStore) Del(ctx context.Context, key string) (int64, error) {
	return s.c.Del(ctx, key).Result()
}

func (s *redisStore) Incr(ctx context.Context, key string, window time.Duration) (int64, error) {
	// INCR and EXPIRE have to be one script. A crash between them used to leave a counter that never expired.
	n, err := incrExpire.Run(ctx, s.c, []string{key}, window.Milliseconds()).Int64()
	return n, err
}

var incrExpire = redis.NewScript(`
local n = redis.call('INCR', KEYS[1])
if n == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
return n
`)

func (s *redisStore) Update(ctx context.Context, key string, ttl time.Duration, fn func([]byte) ([]byte, error)) error {
	var fnErr error
	for range 8 {
		err := s.c.Watch(ctx, func(tx *redis.Tx) error {
			prev, err := tx.Get(ctx, key).Bytes()
			if errors.Is(err, redis.Nil) {
				prev = nil
			} else if err != nil {
				return err
			}
			next, err := fn(prev)
			if err != nil {
				fnErr = err
				return err
			}
			_, err = tx.TxPipelined(ctx, func(p redis.Pipeliner) error {
				if next == nil {
					p.Del(ctx, key)
					return nil
				}
				p.Set(ctx, key, next, ttl)
				return nil
			})
			return err
		}, key)
		if err == nil {
			return nil
		}
		if errors.Is(err, redis.TxFailedErr) {
			fnErr = nil
			continue
		}
		if fnErr != nil {
			return fnErr
		}
		return err
	}
	return errors.New("captcha store busy")
}

type memItem struct {
	val []byte
	exp time.Time
}

// MemoryStore is an in-process store for tests and for a missing Redis.
type MemoryStore struct {
	mu   sync.Mutex
	data map[string]memItem
	now  func() time.Time
}

// NewMemoryStore returns an empty store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{data: map[string]memItem{}, now: time.Now}
}

func (m *MemoryStore) alive(it memItem) bool {
	return it.exp.IsZero() || m.now().Before(it.exp)
}

func (m *MemoryStore) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	it, ok := m.data[key]
	if !ok || !m.alive(it) {
		delete(m.data, key)
		return nil, ErrNotFound
	}
	return append([]byte(nil), it.val...), nil
}

func (m *MemoryStore) Put(_ context.Context, key string, val []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = memItem{val: append([]byte(nil), val...), exp: m.now().Add(ttl)}
	return nil
}

func (m *MemoryStore) Del(_ context.Context, key string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.data[key]; !ok {
		return 0, nil
	}
	delete(m.data, key)
	return 1, nil
}

func (m *MemoryStore) Incr(_ context.Context, key string, window time.Duration) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	it, ok := m.data[key]
	n := int64(0)
	if ok && m.alive(it) && len(it.val) == 8 {
		n = int64(it.val[0])<<56 | int64(it.val[1])<<48 | int64(it.val[2])<<40 | int64(it.val[3])<<32 |
			int64(it.val[4])<<24 | int64(it.val[5])<<16 | int64(it.val[6])<<8 | int64(it.val[7])
	}
	n++
	var buf [8]byte
	for i := range buf {
		buf[7-i] = byte(n >> (8 * i))
	}
	exp := m.now().Add(window)
	if ok && m.alive(it) {
		exp = it.exp
	}
	m.data[key] = memItem{val: buf[:], exp: exp}
	return n, nil
}

func (m *MemoryStore) Update(_ context.Context, key string, ttl time.Duration, fn func([]byte) ([]byte, error)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var prev []byte
	if it, ok := m.data[key]; ok && m.alive(it) {
		prev = it.val
	}
	next, err := fn(prev)
	if err != nil {
		return err
	}
	if next == nil {
		delete(m.data, key)
		return nil
	}
	m.data[key] = memItem{val: append([]byte(nil), next...), exp: m.now().Add(ttl)}
	return nil
}
