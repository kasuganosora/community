package testutil

import (
	"testing"

	"gorm.io/gorm"
)

// TestSetup provides a unified test setup with both DB and Redis
type TestSetup struct {
	DB       *TestDB
	Redis    *RedisTestClient
	Fixtures *FixtureBuilder
	t        *testing.T
}

// SetupOptions configures the test setup
type SetupOptions struct {
	// DBMode determines how the database is created
	DBMode DBMode
	// RedisMode determines how Redis is created
	RedisMode RedisTestMode
	// Models to auto-migrate
	Models []interface{}
}

// DefaultSetupOptions returns the default setup options
func DefaultSetupOptions() *SetupOptions {
	return &SetupOptions{
		DBMode:    ModeIsolated,
		RedisMode: RedisModeAuto,
		Models:    nil,
	}
}

// SetupOption is a function that modifies SetupOptions
type SetupOption func(*SetupOptions)

// WithDBMode sets the database mode
func WithDBMode(mode DBMode) SetupOption {
	return func(o *SetupOptions) {
		o.DBMode = mode
	}
}

// WithRedisMode sets the Redis mode
func WithRedisMode(mode RedisTestMode) SetupOption {
	return func(o *SetupOptions) {
		o.RedisMode = mode
	}
}

// WithModels sets the models to auto-migrate
func WithModels(models ...interface{}) SetupOption {
	return func(o *SetupOptions) {
		o.Models = models
	}
}

// WithRealRedis forces real Redis (skips test if unavailable)
func WithRealRedis() SetupOption {
	return WithRedisMode(RedisModeReal)
}

// WithMockRedis forces mock Redis
func WithMockRedis() SetupOption {
	return WithRedisMode(RedisModeMock)
}

// WithSharedDB uses shared database (not safe for parallel tests)
func WithSharedDB() SetupOption {
	return WithDBMode(ModeShared)
}

// WithIsolatedDB uses isolated database (safe for parallel tests)
func WithIsolatedDB() SetupOption {
	return WithDBMode(ModeIsolated)
}

// NewTestSetup creates a new unified test setup
func NewTestSetup(t *testing.T, opts ...SetupOption) *TestSetup {
	options := DefaultSetupOptions()
	for _, opt := range opts {
		opt(options)
	}

	setup := &TestSetup{
		t: t,
	}

	// Setup database
	switch options.DBMode {
	case ModeShared:
		db, err := SetupSharedTestDB(t)
		if err != nil {
			t.Fatalf("Failed to create shared test DB: %v", err)
			return nil
		}
		setup.DB = db
	default:
		db, err := NewIsolatedTestDB(t)
		if err != nil {
			t.Fatalf("Failed to create isolated test DB: %v", err)
			return nil
		}
		setup.DB = db
	}

	// Auto-migrate models if specified
	if len(options.Models) > 0 {
		if err := setup.DB.AutoMigrate(options.Models...); err != nil {
			t.Fatalf("Failed to auto-migrate models: %v", err)
		}
	}

	// Setup Redis
	setup.Redis = SetupRedisTest(t, options.RedisMode)

	// Create fixture builder
	setup.Fixtures = NewFixtureBuilder(setup.DB.GormDB)

	return setup
}

// GormDB returns the Gorm DB for convenience
func (s *TestSetup) GormDB() *gorm.DB {
	return s.DB.GormDB
}

// RedisClient returns the Redis client for convenience
func (s *TestSetup) RedisClient() *RedisTestClient {
	return s.Redis
}

// RawRedisClient returns the underlying Redis client
func (s *TestSetup) RawRedisClient() *RedisTestClient {
	return s.Redis
}

// Close cleans up all resources
func (s *TestSetup) Close() {
	if s.Redis != nil {
		s.Redis.AssertExpectations(s.t)
		s.Redis.Close()
	}
	if s.DB != nil {
		s.DB.Close()
	}
}

// QuickDBSetup creates an isolated test DB with auto-migration
// This is the simplest way to get a test database
func QuickDBSetup(t *testing.T, models ...interface{}) *TestDB {
	db, err := NewIsolatedTestDB(t)
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
		return nil
	}

	if len(models) > 0 {
		if err := db.AutoMigrate(models...); err != nil {
			t.Fatalf("Failed to auto-migrate models: %v", err)
		}
	}

	return db
}

// QuickRedisSetup creates an auto-detected Redis client
// Uses real Redis if available, mock otherwise
func QuickRedisSetup(t *testing.T) *RedisTestClient {
	return SetupAutoRedisTest(t)
}

// QuickMockRedisSetup creates a mock Redis client
func QuickMockRedisSetup(t *testing.T) *RedisTestClient {
	return SetupMockRedisTest(t)
}

// QuickRealRedisSetup creates a real Redis client
// Skips test if Redis is not available
func QuickRealRedisSetup(t *testing.T) *RedisTestClient {
	return SetupRealRedisTest(t)
}

// FullSetup creates a complete test setup with DB, Redis, and fixtures
// This is the recommended way to set up integration tests
func FullSetup(t *testing.T, models []interface{}, opts ...SetupOption) *TestSetup {
	opts = append(opts, WithModels(models...))
	return NewTestSetup(t, opts...)
}

// DBOnlySetup creates a test setup with only database
func DBOnlySetup(t *testing.T, models ...interface{}) *TestSetup {
	return NewTestSetup(t,
		WithModels(models...),
		WithRedisMode(RedisModeMock), // Mock Redis but not used
	)
}

// RedisOnlySetup creates a test setup with only Redis
func RedisOnlySetup(t *testing.T, mode RedisTestMode) *TestSetup {
	return NewTestSetup(t,
		WithRedisMode(mode),
	)
}
