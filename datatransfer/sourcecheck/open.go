package sourcecheck

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"
)

// OpenPG opens a single read-only connection to the source with
// application_name = AppName (so the sessions check ignores it).
func OpenPG(dsn string) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("invalid source DSN")
	}
	if cfg.RuntimeParams == nil {
		cfg.RuntimeParams = map[string]string{}
	}
	cfg.RuntimeParams["application_name"] = AppName
	cfg.RuntimeParams["default_transaction_read_only"] = "on"
	db := stdlib.OpenDB(*cfg)
	db.SetMaxOpenConns(1)
	return db, nil
}

// OpenRedis parses redis://[:password@]host:port/db; "" returns nil (check skipped).
func OpenRedis(raw string) (redis.UniversalClient, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if !strings.Contains(raw, "://") {
		raw = "redis://" + raw
	}
	opt, err := redis.ParseURL(raw)
	if err != nil {
		return nil, errors.New("invalid source Redis URL")
	}
	return redis.NewClient(opt), nil
}
