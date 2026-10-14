// Package discovery supplies bounded, disposable recommendation candidates.
// SQL remains authoritative for every object returned to a reader.
package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/kasuganosora/community/models"
)

const (
	AlgorithmVersion = "rules-v1"
	CandidateLimit   = 400
	SnapshotLimit    = 200
	BlockSize        = 20
	SnapshotTTL      = 10 * time.Minute
	PoolMaxAge       = 5 * time.Minute
	CandidateWindow  = 7 * 24 * time.Hour
)

var (
	ErrResetRequired  = errors.New("recommendation window expired; refresh to continue")
	ErrUnavailable    = errors.New("recommendations are temporarily unavailable")
	ErrInvalidRequest = errors.New("invalid recommendation request")
)

type Config struct {
	Enabled                  func() bool
	CheckEnabled             func(context.Context) (bool, error)
	Policy                   func(context.Context) (string, error)
	TrendsEnabled            func() bool
	RefreshRemotePreferences func(context.Context) error
	SigningKeys              [][]byte
	MaxActiveSnapshots       int
	MaxCacheBytes            int64
}

var feedRuntime struct {
	sync.RWMutex
	config   Config
	provider ContentProvider
}

// Configure is called after shared settings and Redis have been initialized.
// The first key signs new cursors; additional keys verify existing cursors.
func Configure(config Config) {
	if config.MaxActiveSnapshots <= 0 {
		config.MaxActiveSnapshots = 512
	}
	if config.MaxCacheBytes <= 0 {
		config.MaxCacheBytes = 64 << 20
	}
	keys := make([][]byte, 0, len(config.SigningKeys))
	for _, key := range config.SigningKeys {
		if len(key) >= 32 {
			keys = append(keys, append([]byte(nil), key...))
		}
	}
	config.SigningKeys = keys
	feedRuntime.Lock()
	feedRuntime.config = config
	feedRuntime.Unlock()
}

func feedConfig() Config { feedRuntime.RLock(); defer feedRuntime.RUnlock(); return feedRuntime.config }
func Enabled() bool {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	enabled, err := EnabledContext(ctx)
	return err == nil && enabled
}
func EnabledContext(ctx context.Context) (bool, error) {
	config := feedConfig()
	if config.CheckEnabled != nil {
		return config.CheckEnabled(ctx)
	}
	return config.Enabled != nil && config.Enabled(), nil
}
func feedPolicy(ctx context.Context) (string, error) {
	feedRuntime.RLock()
	config, provider := feedRuntime.config, feedRuntime.provider
	feedRuntime.RUnlock()
	var policy string
	if config.Policy != nil {
		var err error
		policy, err = config.Policy(ctx)
		if err != nil {
			return "", err
		}
	}
	// Deployment model/configuration changes must also invalidate windows and
	// shared pools, even when the SQL approval value has not changed. This is a
	// local fingerprint only: timeline reads never contact the search service.
	if versioned, ok := provider.(interface{ PolicyVersion() string }); ok {
		hash := sha256.Sum256([]byte("discovery-policy-v2\x00" + policy + "\x00" + versioned.PolicyVersion()))
		return hex.EncodeToString(hash[:]), nil
	}
	return policy, nil
}
func CursorSigningKeys() [][]byte {
	config := feedConfig()
	keys := make([][]byte, len(config.SigningKeys))
	for i, key := range config.SigningKeys {
		keys[i] = append([]byte(nil), key...)
	}
	return keys
}

// ContentProvider is invoked only by the bounded background builder, never by
// an HTTP timeline read. Lists contain public IDs, not user-specific queries.
type ContentProvider interface {
	BuildBuckets(context.Context, time.Time, []ContentBucket) (ContentBatch, error)
}
type ContentBucket struct {
	Key      string
	Query    string
	Language string
}
type ContentBatch struct {
	Mode       string
	Generation string
	DataCutoff time.Time
	Lists      []ContentList
}
type ContentList struct {
	BucketKey  string
	Family     string
	Key        string
	Generation string
	IDs        []uint64
}

func SetContentProvider(provider ContentProvider) {
	feedRuntime.Lock()
	feedRuntime.provider = provider
	feedRuntime.Unlock()
}

type Source struct {
	Family     string `json:"family"`
	Key        string `json:"key"`
	Rank       int    `json:"rank"`
	Generation string `json:"generation"`
}

type Candidate struct {
	StatusID  uint64    `json:"id"`
	AuthorID  uint64    `json:"author"`
	Domain    string    `json:"domain,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Tags      []string  `json:"tags,omitempty"`
	Language  string    `json:"language,omitempty"`
	Heat      float64   `json:"heat"`
	Social    float64   `json:"social,omitempty"`
	Interest  float64   `json:"interest,omitempty"`
	Explore   bool      `json:"explore,omitempty"`
	Sources   []Source  `json:"sources,omitempty"`
}

type Reason struct {
	Code  string `json:"code"`
	Label string `json:"label"`
}
type Item struct {
	Status *models.Status
	Reason *Reason
}
type Page struct {
	Items              []Item
	NextCursor         *string
	Generation         string
	Mode               string
	Preference         string
	PreferenceRevision uint64
	Disabled           bool
}

type RankedItem struct {
	ID         uint64 `json:"id"`
	ReasonCode string `json:"reason,omitempty"`
	ReasonTag  string `json:"tag,omitempty"`
}

type Snapshot struct {
	Generation     string         `json:"generation"`
	UserID         uint64         `json:"user"`
	Revision       uint64         `json:"revision"`
	Preference     string         `json:"preference"`
	Algorithm      string         `json:"algorithm"`
	Mode           string         `json:"mode"`
	PoolGeneration string         `json:"pool_generation"`
	PolicyVersion  string         `json:"policy_version"`
	CreatedAt      time.Time      `json:"created_at"`
	ExpiresAt      time.Time      `json:"expires_at"`
	Blocks         [][]RankedItem `json:"blocks"`
}
