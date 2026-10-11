package database

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type captureLogger struct {
	traces []string
	errs   []string
}

func (c *captureLogger) LogMode(logger.LogLevel) logger.Interface { return c }
func (c *captureLogger) Info(context.Context, string, ...any)     {}
func (c *captureLogger) Warn(context.Context, string, ...any)     {}
func (c *captureLogger) Error(context.Context, string, ...any)    {}
func (c *captureLogger) Trace(_ context.Context, _ time.Time, fc func() (string, int64), err error) {
	sql, _ := fc()
	c.traces = append(c.traces, sql)
	if err != nil {
		c.errs = append(c.errs, err.Error())
	}
}

const secretToken = "syt_SUPERSECRET_abc123"

func TestRedactSensitiveStatements(t *testing.T) {
	cases := []string{
		"SELECT * FROM `matrix_user_sessions` WHERE access_token = 'syt_SUPERSECRET_abc123' ORDER BY `matrix_user_sessions`.`id` LIMIT 1",
		`SELECT * FROM "matrix_user_sessions" WHERE "matrix_user_sessions"."access_token" = 'syt_SUPERSECRET_abc123' LIMIT 1`,
		"SELECT * FROM t WHERE refresh_token IN ('syt_SUPERSECRET_abc123','other')",
		"UPDATE `matrix_user_sessions` SET `access_token`='syt_SUPERSECRET_abc123',`last_active_ts`=5 WHERE id = 3",
		"INSERT INTO `matrix_user_sessions` (`user_id`,`access_token`,`device_id`) VALUES ('@a:b','syt_SUPERSECRET_abc123','D1') RETURNING `id`",
		`UPDATE accounts SET password_hash = 'syt_SUPERSECRET_abc123' WHERE id = 1`,
		`INSERT INTO t (content) VALUES ('{"access_token":"syt_SUPERSECRET_abc123"}')`,
		"SELECT 1 -- Authorization: Bearer syt_SUPERSECRET_abc123",
		"Error 1062: Duplicate entry 'syt_SUPERSECRET_abc123' for key 'access_token'",
		"GET /sync?access_token=syt_SUPERSECRET_abc123&since=1",
	}
	for _, in := range cases {
		out := RedactSensitive(in)
		if strings.Contains(out, secretToken) {
			t.Errorf("secret leaked:\n in: %s\nout: %s", in, out)
		}
		if !strings.Contains(out, redacted) {
			t.Errorf("no redaction marker: %s", out)
		}
	}
	keep := "SELECT * FROM rooms WHERE room_id = '!abc:example.org' AND creator = '@u:example.org'"
	if got := RedactSensitive(keep); got != keep {
		t.Errorf("non-sensitive statement altered: %s", got)
	}
	if got := RedactSensitive("UPDATE t SET last_active_ts=5 WHERE user_id = '@u:x'"); strings.Contains(got, redacted) {
		t.Errorf("unexpected redaction: %s", got)
	}
}

func TestRedactingLoggerTrace(t *testing.T) {
	inner := &captureLogger{}
	l := NewRedactingLogger(inner)
	sql := "SELECT * FROM `matrix_user_sessions` WHERE access_token = '" + secretToken + "' LIMIT 1"

	// An unknown session token is an ordinary 401: nothing is logged.
	l.Trace(context.Background(), time.Now(), func() (string, int64) { return sql, 0 }, gorm.ErrRecordNotFound)
	if len(inner.traces) != 0 {
		t.Fatalf("record-not-found on matrix_user_sessions should be ignored, got %v", inner.traces)
	}

	// Other failures are still logged, but without the bound token.
	dup := errors.New("Error 1062: Duplicate entry '" + secretToken + "' for key 'access_token'")
	l.Trace(context.Background(), time.Now(), func() (string, int64) { return sql, 0 }, dup)
	if len(inner.traces) != 1 {
		t.Fatalf("expected one trace, got %d", len(inner.traces))
	}
	if strings.Contains(inner.traces[0], secretToken) || strings.Contains(strings.Join(inner.errs, " "), secretToken) {
		t.Fatalf("token leaked: %v %v", inner.traces, inner.errs)
	}

	// Record-not-found elsewhere keeps its normal logging (redacted).
	l.Trace(context.Background(), time.Now(), func() (string, int64) {
		return "SELECT * FROM users WHERE token = '" + secretToken + "'", 0
	}, gorm.ErrRecordNotFound)
	if len(inner.traces) != 2 || strings.Contains(inner.traces[1], secretToken) {
		t.Fatalf("unexpected: %v", inner.traces)
	}
}

func TestRedactedErrorKeepsIdentity(t *testing.T) {
	if !errors.Is(redactedError{gorm.ErrRecordNotFound}, gorm.ErrRecordNotFound) {
		t.Fatal("redactedError must unwrap")
	}
}
