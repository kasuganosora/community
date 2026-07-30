package testutil

import (
	"testing"

	cRedis "github.com/kasuganosora/community/db/redis"
	goredis "github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// TestSetup provides a unified test setup with both DB and Redis.
type TestSetup struct {
	DB       *TestDB
	Redis    *RedisTestClient
	Fixtures *FixtureBuilder
	t        *testing.T
}

// SetupOptions configures the test setup.
type SetupOptions struct {
	// DBMode determines how the database is created
	DBMode DBMode
	// RedisMode determines how Redis is created
	RedisMode RedisTestMode
	// Models to auto-migrate
	Models []any
}

// DefaultSetupOptions returns the default setup options.
func DefaultSetupOptions() *SetupOptions {
	return &SetupOptions{
		DBMode:    ModeIsolated,
		RedisMode: RedisModeMock,
		Models:    nil,
	}
}

// SetupOption is a function that modifies SetupOptions.
type SetupOption func(*SetupOptions)

// WithDBMode sets the database mode.
func WithDBMode(mode DBMode) SetupOption {
	return func(o *SetupOptions) {
		o.DBMode = mode
	}
}

// WithRedisMode sets the Redis mode.
func WithRedisMode(mode RedisTestMode) SetupOption {
	return func(o *SetupOptions) {
		o.RedisMode = mode
	}
}

// WithModels sets the models to auto-migrate.
func WithModels(models ...any) SetupOption {
	return func(o *SetupOptions) {
		o.Models = models
	}
}

// WithRealRedis forces real Redis (skips test if unavailable).
func WithRealRedis() SetupOption {
	return WithRedisMode(RedisModeReal)
}

// WithMockRedis forces mock Redis.
func WithMockRedis() SetupOption {
	return WithRedisMode(RedisModeMock)
}

// WithSharedDB uses shared database (not safe for parallel tests).
func WithSharedDB() SetupOption {
	return WithDBMode(ModeShared)
}

// WithIsolatedDB uses isolated database (safe for parallel tests).
func WithIsolatedDB() SetupOption {
	return WithDBMode(ModeIsolated)
}

// NewTestSetup creates a new unified test setup.
// All resources are cleaned up automatically via t.Cleanup.
func NewTestSetup(t *testing.T, opts ...SetupOption) *TestSetup {
	t.Helper()

	options := DefaultSetupOptions()
	for _, opt := range opts {
		opt(options)
	}

	setup := &TestSetup{
		t: t,
	}

	// Setup database (without individual t.Cleanup to avoid double-release)
	switch options.DBMode {
	case ModeShared:
		db, err := SetupSharedTestDB(t)
		if err != nil {
			t.Fatalf("Failed to create shared test DB: %v", err)

			return nil
		}

		setup.DB = db
	default:
		db, err := newTestDBNoCleanup()
		if err != nil {
			t.Fatalf("Failed to create isolated test DB: %v", err)

			return nil
		}

		setup.DB = db
	}

	// Auto-migrate models if specified
	if len(options.Models) > 0 {
		err := setup.DB.AutoMigrate(options.Models...)
		if err != nil {
			t.Fatalf("Failed to auto-migrate models: %v", err)
		}
	}

	// Setup Redis (without individual t.Cleanup to avoid double-release)
	setup.Redis = NewRedisWithMode(t, options.RedisMode)

	// Inject the Redis client into the global redis client pool so that
	// db.GetRedis() returns our mock/real client instead of panicking.
	// This prevents feed/cache/middleware code from timing out on connection
	// attempts when no real Redis is available.
	if setup.Redis != nil && setup.Redis.Client != nil {
		cRedis.SetRedisesForTest([]*goredis.Client{setup.Redis.Client})
	}

	// Create fixture builder
	setup.Fixtures = NewFixtureBuilder(t, setup.DB.GormDB)

	// Register single cleanup point for all resources
	t.Cleanup(func() {
		setup.Close()
	})

	return setup
}

// GormDB returns the Gorm DB for convenience.
func (s *TestSetup) GormDB() *gorm.DB {
	return s.DB.GormDB
}

// RedisClient returns the Redis client for convenience.
func (s *TestSetup) RedisClient() *RedisTestClient {
	return s.Redis
}

// Close cleans up all resources.
func (s *TestSetup) Close() {
	if s.Redis != nil {
		s.Redis.AssertExpectations(s.t)
		_ = s.Redis.Close()
	}

	if s.DB != nil {
		s.DB.Close()
	}
}

// QuickDBSetup creates an isolated test DB with auto-migration.
// This is the simplest way to get a test database.
func QuickDBSetup(t *testing.T, models ...any) *TestDB {
	t.Helper()

	db, err := NewIsolatedTestDB(t)
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)

		return nil
	}

	if len(models) > 0 {
		err := db.AutoMigrate(models...)
		if err != nil {
			t.Fatalf("Failed to auto-migrate models: %v", err)
		}
	}

	return db
}

// QuickRedisSetup creates an auto-detected Redis client
// Uses real Redis if available, mock otherwise.
func QuickRedisSetup(t *testing.T) *RedisTestClient {
	return SetupAutoRedisTest(t)
}

// QuickMockRedisSetup creates a mock Redis client.
func QuickMockRedisSetup(t *testing.T) *RedisTestClient {
	return SetupMockRedisTest(t)
}

// QuickRealRedisSetup creates a real Redis client
// Skips test if Redis is not available.
func QuickRealRedisSetup(t *testing.T) *RedisTestClient {
	return SetupRealRedisTest(t)
}

// FullSetup creates a complete test setup with DB, Redis, and fixtures
// This is the recommended way to set up integration tests.
func FullSetup(t *testing.T, models []any, opts ...SetupOption) *TestSetup {
	t.Helper()

	opts = append(opts, WithModels(models...))

	return NewTestSetup(t, opts...)
}

// DBOnlySetup creates a test setup with only database.
func DBOnlySetup(t *testing.T, models ...any) *TestSetup {
	t.Helper()

	return NewTestSetup(t,
		WithModels(models...),
		WithRedisMode(RedisModeMock),
	)
}

// RedisOnlySetup creates a test setup with only Redis.
func RedisOnlySetup(t *testing.T, mode RedisTestMode) *TestSetup {
	t.Helper()

	return NewTestSetup(t,
		WithRedisMode(mode),
	)
}
