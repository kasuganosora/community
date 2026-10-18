package database

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// The default GORM logger prints every statement with its bound parameters
// inlined (logger.ExplainSQL). A failed Matrix session lookup therefore wrote
// `WHERE access_token = '<the client's bearer token>'` to the log, and a
// duplicate-key failure echoes the offending value in the driver error. This
// logger keeps GORM's formatting and levels but masks the value of every
// credential-looking column before anything is printed.

const redacted = "[REDACTED]"

// sensitiveColumn matches column names that carry credentials. It is a
// substring match on purpose (refresh_token, device_token, password_hash...).
const sensitiveColumn = `(?:access_token|refresh_token|token|password|passwd|secret|passphrase|api_key|private_key|authorization|code_hash)`

var (
	// col = 'value' | col IN ('a','b') | col LIKE 'x' | col = ?
	sqlSensitiveCondition = regexp.MustCompile("(?i)([`\"]?\\w*" + sensitiveColumn + "\\w*[`\"]?\\s*(?:=|!=|<>|<=|>=|\\bLIKE|\\bILIKE|\\bIN)\\s*)" + `('(?:[^'\\]|\\.|'')*'|"(?:[^"\\]|\\.|"")*"|\([^)]*\)|[^\s,;)]+)`)
	// INSERT INTO t (a, access_token) VALUES (...): positions are not tracked,
	// the whole VALUES list is dropped when any column is sensitive.
	sqlInsert = regexp.MustCompile(`(?is)^(\s*INSERT\s+(?:IGNORE\s+)?INTO\s+\S+\s*\()([^)]*)(\)\s*VALUES\s*)(.*?)(\s+ON\s+(?:CONFLICT|DUPLICATE)\b.*|\s+RETURNING\b.*)?$`)
	sqlColumn = regexp.MustCompile("(?i)" + sensitiveColumn)
	// "access_token":"..." inside inlined JSON documents.
	jsonSensitive = regexp.MustCompile(`(?i)(\\?"\w*` + sensitiveColumn + `\w*\\?"\s*:\s*)\\?"(?:[^"\\]|\\.)*?\\?"`)
	bearerToken   = regexp.MustCompile(`(?i)\b(Bearer\s+)[A-Za-z0-9._~+/=\-]+`)
	// Driver errors echo the offending value.
	duplicateEntry = regexp.MustCompile(`(?i)(Duplicate entry )'(?:[^']|'')*'`)
	pgKeyValue     = regexp.MustCompile(`(?i)(Key \([^)]*` + sensitiveColumn + `[^)]*\)=)\([^)]*\)`)
	// Tokens in URL query strings (?access_token=...).
	queryToken = regexp.MustCompile(`(?i)([?&]\w*` + sensitiveColumn + `\w*=)[^&\s"']+`)
)

// RedactSensitive masks credentials in a SQL statement or log message.
func RedactSensitive(s string) string {
	if s == "" {
		return s
	}
	if m := sqlInsert.FindStringSubmatch(s); m != nil && sqlColumn.MatchString(m[2]) {
		s = m[1] + m[2] + m[3] + "(" + redacted + ")" + m[5]
	}
	s = sqlSensitiveCondition.ReplaceAllString(s, "${1}'"+redacted+"'")
	s = jsonSensitive.ReplaceAllString(s, `${1}"`+redacted+`"`)
	s = bearerToken.ReplaceAllString(s, "${1}"+redacted)
	s = duplicateEntry.ReplaceAllString(s, "${1}'"+redacted+"'")
	s = pgKeyValue.ReplaceAllString(s, "${1}("+redacted+")")
	s = queryToken.ReplaceAllString(s, "${1}"+redacted)
	return s
}

type redactedError struct{ err error }

func (e redactedError) Error() string { return RedactSensitive(e.err.Error()) }
func (e redactedError) Unwrap() error { return e.err }

type redactingLogger struct{ inner logger.Interface }

func init() {
	// GORM's Scan temporarily substitutes its own recorder for the configured
	// logger. Protect that path too, before it formats parameters as SQL.
	logger.RecorderParamsFilter = omitLoggedParams
}

func omitLoggedParams(_ context.Context, sql string, _ ...any) (string, []any) {
	return sql, nil
}

// NewRedactingLogger wraps a GORM logger so bound credentials never reach it.
// Failed lookups of Matrix sessions (an unknown/expired bearer token is a
// normal 401, not a database fault) are not logged at all.
func NewRedactingLogger(inner logger.Interface) logger.Interface {
	if inner == nil {
		inner = logger.Default
	}
	return &redactingLogger{inner: inner}
}

func (l *redactingLogger) LogMode(level logger.LogLevel) logger.Interface {
	return &redactingLogger{inner: l.inner.LogMode(level)}
}

// ParamsFilter prevents the ORM from interpolating bound values in the first
// place. SQL-shaped regexes cannot safely parse every dialect's string, JSON,
// binary or array literal. Keep placeholders for diagnostics instead.
func (l *redactingLogger) ParamsFilter(ctx context.Context, sql string, params ...any) (string, []any) {
	return omitLoggedParams(ctx, sql, params...)
}

func (l *redactingLogger) Info(ctx context.Context, msg string, args ...any) {
	l.inner.Info(ctx, "%s", RedactSensitive(fmt.Sprintf(msg, args...)))
}

func (l *redactingLogger) Warn(ctx context.Context, msg string, args ...any) {
	l.inner.Warn(ctx, "%s", RedactSensitive(fmt.Sprintf(msg, args...)))
}

func (l *redactingLogger) Error(ctx context.Context, msg string, args ...any) {
	l.inner.Error(ctx, "%s", RedactSensitive(fmt.Sprintf(msg, args...)))
}

func (l *redactingLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	if err != nil && errors.Is(err, gorm.ErrRecordNotFound) {
		sql, _ := fc()
		if strings.Contains(sql, "matrix_user_sessions") {
			return
		}
	}
	if err != nil {
		err = redactedError{err}
	}
	l.inner.Trace(ctx, begin, func() (string, int64) {
		sql, rows := fc()
		return RedactSensitive(sql), rows
	}, err)
}
