package setup

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/models"
	"github.com/redis/go-redis/v9"
	gmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Error is a setup failure with a stable code. The frontend translates
// "setup.errors.<code>"; Detail is an optional safe English hint (never a
// password, DSN or raw driver error that could contain one).
type Error struct {
	Code   string `json:"error_code"`
	Detail string `json:"detail,omitempty"`
}

func (e *Error) Error() string {
	if e.Detail != "" {
		return e.Code + ": " + e.Detail
	}
	return e.Code
}

func fail(code string, detail ...string) *Error {
	return &Error{Code: code, Detail: strings.Join(detail, " ")}
}

// DatabaseParams is the target database chosen in the wizard.
type DatabaseParams struct {
	Type     string `json:"type"` // postgres, mysql, sqlite; builtin is resolved before use
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Name     string `json:"name"`
	User     string `json:"user"`
	Password string `json:"password"`
	SSLMode  string `json:"sslmode"`
	Path     string `json:"path"` // sqlite file
}

// RedisParams is the Redis server address.
type RedisParams struct {
	Address  string `json:"address"`
	Password string `json:"password"`
	DB       int    `json:"db"`
}

// AdminParams is the first administrator account.
type AdminParams struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

// ObjectStorageParams describes the S3-compatible bucket holding Misskey's
// drive files (e.g. Cloudflare R2).
type ObjectStorageParams struct {
	Endpoint  string `json:"endpoint"`
	Bucket    string `json:"bucket"`
	Region    string `json:"region"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
	PublicURL string `json:"public_url"`
	PathStyle bool   `json:"path_style"`
}

// MisskeyParams is the source Misskey PostgreSQL database and media.
type MisskeyParams struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Name     string `json:"name"`
	User     string `json:"user"`
	Password string `json:"password"`
	SSLMode  string `json:"sslmode"`
	IDMethod string `json:"id_method"`
	// MediaSource is "local" (FilesPath is Misskey's files directory) or
	// "s3" (object storage, see ObjectStorage and MediaMode).
	MediaSource string `json:"media_source"`
	FilesPath   string `json:"files_path"`
	// MediaMode for object storage: "reference" keeps the original object
	// URLs (the bucket keeps serving them; nothing is copied), "copy"
	// downloads every object (resumable) and imports the bytes.
	MediaMode     string              `json:"media_mode"`
	ObjectStorage ObjectStorageParams `json:"object_storage"`
}

func (p *DatabaseParams) normalize() *Error {
	p.Type = strings.ToLower(strings.TrimSpace(p.Type))
	if p.Type == "postgresql" || p.Type == "pgsql" {
		p.Type = "postgres"
	}
	p.Host = strings.TrimSpace(p.Host)
	p.Name = strings.TrimSpace(p.Name)
	p.User = strings.TrimSpace(p.User)
	switch p.Type {
	case "postgres", "mysql":
		if p.Host == "" || p.Name == "" || p.User == "" {
			return fail("database_fields_required")
		}
		if strings.ContainsAny(p.Host, "/@?# \t\r\n") {
			return fail("invalid_database_host")
		}
		if p.Port == 0 {
			p.Port = map[string]int{"postgres": 5432, "mysql": 3306}[p.Type]
		}
		if p.Port < 1 || p.Port > 65535 {
			return fail("invalid_database_port")
		}
		if p.Type == "postgres" {
			switch p.SSLMode {
			case "":
				p.SSLMode = "disable"
			case "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
			default:
				return fail("invalid_database_sslmode")
			}
		}
	case "sqlite":
		p.Path = strings.TrimSpace(p.Path)
		if p.Path == "" || strings.ContainsAny(p.Path, "?\x00") {
			return fail("database_fields_required")
		}
	default:
		return fail("invalid_database_type")
	}
	return nil
}

func postgresKeywordDSN(host string, port int, user, password, name, sslmode string) string {
	q := database.QuoteDSNValue
	return strings.Join([]string{
		"host=" + q(host), "port=" + strconv.Itoa(port), "user=" + q(user), "password=" + q(password),
		"dbname=" + q(name), "sslmode=" + q(sslmode), "connect_timeout=5",
	}, " ")
}

// openDatabase opens (and pings) the target database. Errors are reduced to
// codes: driver messages can echo connection parameters.
func openDatabase(p DatabaseParams) (*gorm.DB, error) {
	cfg := &gorm.Config{Logger: logger.Discard}
	var (
		conn *gorm.DB
		err  error
	)
	switch p.Type {
	case "postgres":
		conn, err = gorm.Open(dialect.OpenPostgres(postgresKeywordDSN(p.Host, p.Port, p.User, p.Password, p.Name, p.SSLMode)), cfg)
	case "mysql":
		mc := mysqldriver.NewConfig()
		mc.User, mc.Passwd, mc.Net, mc.Addr, mc.DBName = p.User, p.Password, "tcp", net.JoinHostPort(p.Host, strconv.Itoa(p.Port)), p.Name
		mc.Timeout = 5 * time.Second
		mc.ParseTime = true
		conn, err = gorm.Open(gmysql.Open(mc.FormatDSN()), cfg)
	case "sqlite":
		if dir := filepath.Dir(p.Path); dir != "" {
			if err = os.MkdirAll(dir, 0o700); err != nil {
				return nil, fail("database_connect_failed", "the SQLite directory cannot be created")
			}
		}
		conn, err = gorm.Open(dialect.OpenSQLite(p.Path), cfg)
	default:
		return nil, fail("invalid_database_type")
	}
	if err != nil {
		return nil, fail("database_connect_failed", safeConnectHint(err))
	}
	sqlDB, err := conn.DB()
	if err != nil {
		return nil, fail("database_connect_failed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err = sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fail("database_connect_failed", safeConnectHint(err))
	}
	return conn, nil
}

// safeConnectHint classifies a connection error without echoing it.
func safeConnectHint(err error) string {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "password") || strings.Contains(msg, "authentication") || strings.Contains(msg, "access denied"):
		return "authentication failed"
	case strings.Contains(msg, "does not exist") || strings.Contains(msg, "unknown database"):
		return "database does not exist"
	case strings.Contains(msg, "refused"):
		return "connection refused"
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline"):
		return "connection timed out"
	case strings.Contains(msg, "no such host"):
		return "host not found"
	case strings.Contains(msg, "ssl") || strings.Contains(msg, "tls"):
		return "TLS negotiation failed"
	}
	return ""
}

// appTables are tables only a Community installation creates. A target
// that has any of them (or the installed marker) is refused: the wizard
// never re-initializes or overwrites an existing database.
var appTables = []string{"system_settings", "accounts", "users", "statuses", "migrations"}

// databaseHasCommunityData reports whether the target already holds (part
// of) a Community installation.
func databaseHasCommunityData(conn *gorm.DB) bool {
	for _, t := range appTables {
		if conn.Migrator().HasTable(t) {
			return true
		}
	}
	return false
}

func closeDB(conn *gorm.DB) {
	if conn == nil {
		return
	}
	if s, err := conn.DB(); err == nil {
		_ = s.Close()
	}
}

func (p *RedisParams) normalize() *Error {
	p.Address = strings.TrimSpace(p.Address)
	if p.Address == "" {
		return fail("redis_fields_required")
	}
	if !strings.Contains(p.Address, ":") {
		p.Address += ":6379"
	}
	host, port, err := net.SplitHostPort(p.Address)
	if err != nil || host == "" {
		return fail("invalid_redis_address")
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return fail("invalid_redis_address")
	}
	if p.DB < 0 || p.DB > 15 {
		return fail("invalid_redis_address")
	}
	return nil
}

func pingRedis(p RedisParams) *Error {
	client := redis.NewClient(&redis.Options{Addr: p.Address, Password: p.Password, DB: p.DB, DialTimeout: 4 * time.Second, ReadTimeout: 4 * time.Second, MaxRetries: 0})
	defer func() { _ = client.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		hint := ""
		if strings.Contains(strings.ToLower(err.Error()), "auth") || strings.Contains(err.Error(), "NOAUTH") {
			hint = "authentication failed"
		}
		return fail("redis_connect_failed", hint)
	}
	return nil
}

// normalizeBaseURL turns "example.org" or "https://example.org/" into an
// origin. Plain HTTP is accepted only for localhost (development).
func normalizeBaseURL(input string) (string, *Error) {
	s := strings.TrimSpace(input)
	if s == "" {
		return "", fail("invalid_domain")
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", fail("invalid_domain")
	}
	host := strings.ToLower(u.Hostname())
	local := host == "localhost" || host == "127.0.0.1" || host == "::1"
	switch u.Scheme {
	case "https":
	case "http":
		if !local {
			return "", fail("invalid_domain", "HTTPS is required")
		}
	default:
		return "", fail("invalid_domain")
	}
	if !local && !strings.Contains(host, ".") {
		return "", fail("invalid_domain")
	}
	return u.Scheme + "://" + strings.ToLower(u.Host), nil
}

func (a *AdminParams) normalize() *Error {
	a.Username = strings.TrimSpace(a.Username)
	a.Email = strings.TrimSpace(a.Email)
	if err := models.ValidateUsername(a.Username); err != nil {
		return fail("invalid_username")
	}
	if err := models.ValidateEmail(a.Email); err != nil {
		return fail("invalid_email")
	}
	if err := models.ValidatePassword(a.Password); err != nil {
		return fail("weak_password")
	}
	return nil
}

// envValues returns the core .env settings for an installation request.
func envValues(baseURL string, d DatabaseParams, r RedisParams) map[string]string {
	v := map[string]string{"APP_BASE_URL": baseURL, "DB_CONNECTION": d.Type}
	switch d.Type {
	case "sqlite":
		v["DB_HOST"] = d.Path
	default:
		v["DB_HOST"] = d.Host
		v["DB_PORT"] = strconv.Itoa(d.Port)
		v["DB_DATABASE"] = d.Name
		v["DB_USERNAME"] = d.User
		v["DB_PASSWORD"] = d.Password
		if d.Type == "postgres" {
			v["DB_SSLMODE"] = d.SSLMode
		}
	}
	host, port, _ := net.SplitHostPort(r.Address)
	v["REDIS_0_HOST"] = host
	v["REDIS_0_PORT"] = port
	v["REDIS_0_PASSWORD"] = r.Password
	v["REDIS_0_DB"] = fmt.Sprint(r.DB)
	return v
}
