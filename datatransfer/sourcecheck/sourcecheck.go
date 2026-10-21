// Package sourcecheck decides whether a migration source (Misskey or
// Mastodon) is really stopped before a final sync. It is shared by the
// community-transfer CLI (direct mode) and the setup wizard.
//
// Checks (each pass / fail / skipped):
//   - sessions: pg_stat_activity has no other client sessions on the source
//     database (our own backend and any application_name starting with
//     "community-transfer" are excluded).
//   - write_activity: a write watermark sampled twice Interval apart is
//     identical: max(id) of the main post table (index) and its
//     pg_stat_user_tables n_tup_ins/upd/del, plus pg_stat_database
//     tup_inserted/updated/deleted as extra evidence. No count(*).
//   - web: the LOCAL source web process (default
//     http://127.0.0.1:3000/api/meta) does not answer. Never point this at
//     the public domain: a CDN/cache can answer while the origin is down.
//   - queues (Misskey): Bull job queues in the source Redis have no active
//     jobs. Skipped when no Redis is configured.
//
// Output never contains DSNs, passwords or Redis URLs.
package sourcecheck

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	Stopped = "STOPPED"
	Running = "RUNNING"

	Pass    = "pass"
	Fail    = "fail"
	Skipped = "skipped"

	// AppName is the application_name our own connections use; sessions
	// whose application_name starts with it are not counted.
	AppName   = "community-transfer-sourcecheck"
	ownPrefix = "community-transfer"

	DefaultWebURL   = "http://127.0.0.1:3000/api/meta"
	DefaultInterval = 30 * time.Second
	DefaultPrefix   = "bull"
)

// DefaultQueues are Misskey's Bull queues.
var DefaultQueues = []string{"deliver", "inbox", "db", "relationship", "objectStorage", "userWebhookDeliver", "systemWebhookDeliver", "endedPollNotification", "postScheduledNote", "system"}

// The five checks, in their fixed order. Only "queues" may be skipped.
const (
	CheckWebPort  = "web_port"
	CheckWebMeta  = "web_meta"
	CheckSessions = "sessions"
	CheckNotes    = "note_writes"
	CheckQueues   = "queues"
)

// Order is the fixed display order.
var Order = []string{CheckWebPort, CheckWebMeta, CheckSessions, CheckNotes, CheckQueues}

// Titles are the fixed English labels (the wizard localizes by name).
var Titles = map[string]string{
	CheckWebPort:  "1. Misskey web process stopped (local port 3000 not listening)",
	CheckWebMeta:  "2. Local /api/meta unreachable",
	CheckSessions: "3. No other connections to the Misskey DB (excluding our own tool)",
	CheckNotes:    "4. note table: 30s apart, max(id) unchanged and note insert/update/delete count deltas are 0",
	CheckQueues:   "5. Redis queues have no active jobs (SKIP if Redis not configured)",
}

// Session is one foreign client of the source database.
type Session struct {
	PID          int        `json:"pid"`
	User         string     `json:"usename"`
	Application  string     `json:"application_name"`
	ClientAddr   string     `json:"client_addr"`
	State        string     `json:"state"`
	BackendStart *time.Time `json:"backend_start,omitempty"`
	QueryStart   *time.Time `json:"query_start,omitempty"`
}

func (s Session) String() string {
	f := func(t *time.Time) string {
		if t == nil {
			return "-"
		}
		return t.UTC().Format(time.RFC3339)
	}
	addr := s.ClientAddr
	if addr == "" {
		addr = "local"
	}
	return fmt.Sprintf("pid=%d user=%s app=%q client=%s state=%s backend_start=%s query_start=%s", s.PID, s.User, s.Application, addr, s.State, f(s.BackendStart), f(s.QueryStart))
}

// Watermark is one sample of the write counters.
type Watermark struct {
	Inserted, Updated, Deleted int64
	MaxID                      string
	// Note* are pg_stat_user_tables counters of the post table.
	NoteIns, NoteUpd, NoteDel int64
}

// Source reads the source database. pgSource implements it; tests fake it.
type Source interface {
	Sessions(ctx context.Context) ([]Session, error)
	Watermark(ctx context.Context) (Watermark, error)
}

// Config selects what to check.
type Config struct {
	Source   Source
	Interval time.Duration // between watermark samples; 0 = DefaultInterval
	// WebURL is the LOCAL/internal source web endpoint ("" = DefaultWebURL).
	// Its host:port is also the port probed by item 1.
	WebURL string
	// Redis is the source Redis (nil = queues check skipped).
	Redis  redis.UniversalClient
	Prefix string
	Queues []string
	HTTP   *http.Client
	Sleep  func(ctx context.Context, d time.Duration) error
}

// Check is the outcome of one rule.
type Check struct {
	Name    string   `json:"name"`
	Title   string   `json:"title"`
	Status  string   `json:"status"`
	Reasons []string `json:"reasons,omitempty"`
}

// Result is the overall verdict.
type Result struct {
	Status    string    `json:"status"`
	Checks    []Check   `json:"checks"`
	CheckedAt time.Time `json:"checked_at"`
}

// Failed lists the names of failing checks.
func (r *Result) Failed() []string {
	var out []string
	for _, c := range r.Checks {
		if c.Status == Fail {
			out = append(out, c.Name)
		}
	}
	return out
}

// Run executes every check. It never returns an error: a check that cannot
// run fails with the reason.
func Run(ctx context.Context, c Config) *Result {
	if c.Interval <= 0 {
		c.Interval = DefaultInterval
	}
	if c.Sleep == nil {
		c.Sleep = sleepCtx
	}
	res := &Result{}
	port, meta := checkWeb(ctx, c)
	res.Checks = append(res.Checks, port, meta, checkSessions(ctx, c), checkWrites(ctx, c), checkQueues(ctx, c))
	for i := range res.Checks {
		res.Checks[i].Title = Titles[res.Checks[i].Name]
	}
	res.Status = Stopped
	if len(res.Failed()) > 0 {
		res.Status = Running
	}
	res.CheckedAt = time.Now().UTC()
	return res
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func checkSessions(ctx context.Context, c Config) Check {
	ch := Check{Name: CheckSessions, Status: Pass}
	if c.Source == nil {
		return Check{Name: CheckSessions, Status: Fail, Reasons: []string{"source database not configured"}}
	}
	ss, err := c.Source.Sessions(ctx)
	if err != nil {
		return Check{Name: CheckSessions, Status: Fail, Reasons: []string{"cannot read pg_stat_activity: " + Redact(err.Error())}}
	}
	for _, s := range ss {
		ch.Reasons = append(ch.Reasons, s.String())
	}
	if len(ss) > 0 {
		ch.Status = Fail
		ch.Reasons = append([]string{fmt.Sprintf("%d other client session(s) connected to the source database", len(ss))}, ch.Reasons...)
	} else {
		ch.Reasons = []string{"no other client sessions"}
	}
	return ch
}

func checkWrites(ctx context.Context, c Config) Check {
	ch := Check{Name: CheckNotes, Status: Fail}
	if c.Source == nil {
		ch.Reasons = []string{"source database not configured"}
		return ch
	}
	a, err := c.Source.Watermark(ctx)
	if err != nil {
		ch.Reasons = []string{"cannot sample write counters: " + Redact(err.Error())}
		return ch
	}
	if err := c.Sleep(ctx, c.Interval); err != nil {
		ch.Reasons = []string{"interrupted"}
		return ch
	}
	b, err := c.Source.Watermark(ctx)
	if err != nil {
		ch.Reasons = []string{"cannot sample write counters: " + Redact(err.Error())}
		return ch
	}
	diff := func(name string, x, y int64) {
		if x != y {
			ch.Reasons = append(ch.Reasons, fmt.Sprintf("%s changed %d -> %d", name, x, y))
		}
	}
	if a.MaxID != b.MaxID {
		ch.Reasons = append(ch.Reasons, fmt.Sprintf("note max(id) changed %s -> %s", a.MaxID, b.MaxID))
	}
	diff("note n_tup_ins", a.NoteIns, b.NoteIns)
	diff("note n_tup_upd", a.NoteUpd, b.NoteUpd)
	diff("note n_tup_del", a.NoteDel, b.NoteDel)
	// Extra evidence: database-wide write counters.
	diff("pg_stat_database tup_inserted", a.Inserted, b.Inserted)
	diff("pg_stat_database tup_updated", a.Updated, b.Updated)
	diff("pg_stat_database tup_deleted", a.Deleted, b.Deleted)
	if len(ch.Reasons) == 0 {
		ch.Status = Pass
		ch.Reasons = []string{fmt.Sprintf("no writes in %s (max id %s, note ins/upd/del deltas 0)", c.Interval, b.MaxID)}
	} else {
		ch.Reasons = append([]string{fmt.Sprintf("writes detected within %s", c.Interval)}, ch.Reasons...)
	}
	return ch
}

// checkWeb returns items 1 (port) and 2 (/api/meta). Both always run.
func checkWeb(ctx context.Context, c Config) (Check, Check) {
	port := Check{Name: CheckWebPort, Status: Pass}
	meta := Check{Name: CheckWebMeta, Status: Pass}
	target := c.WebURL
	if target == "" {
		target = DefaultWebURL
	}
	u, err := url.Parse(target)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		r := []string{"invalid source web URL (use the local address, e.g. " + DefaultWebURL + ")"}
		port.Status, port.Reasons = Fail, r
		meta.Status, meta.Reasons = Fail, r
		return port, meta
	}
	hostport := u.Host
	if u.Port() == "" {
		hostport = net.JoinHostPort(u.Hostname(), map[string]string{"http": "80", "https": "443"}[u.Scheme])
	}
	shown := u.Scheme + "://" + u.Host + u.Path
	var warn []string
	if !isLocalHost(u.Hostname()) {
		warn = []string{"warning: " + u.Hostname() + " does not look like a local/internal address; a CDN may answer for a stopped origin"}
	}
	dialer := net.Dialer{Timeout: 3 * time.Second}
	if conn, err := dialer.DialContext(ctx, "tcp", hostport); err == nil {
		conn.Close()
		port.Status = Fail
		port.Reasons = append([]string{hostport + " is listening"}, warn...)
	} else {
		port.Reasons = append([]string{hostport + " not listening"}, warn...)
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		meta.Reasons = append([]string{shown + " unreachable (" + connReason(err) + ")"}, warn...)
		return port, meta
	}
	resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		meta.Status = Fail
		meta.Reasons = append([]string{fmt.Sprintf("%s answered HTTP %d: the Misskey web process is still up", shown, resp.StatusCode)}, warn...)
	} else {
		meta.Reasons = append([]string{fmt.Sprintf("%s answered HTTP %d (not a Misskey API answer)", shown, resp.StatusCode)}, warn...)
	}
	return port, meta
}

func connReason(err error) string {
	var op *net.OpError
	switch {
	case errors.As(err, &op) && op.Op == "dial":
		return "port not listening"
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "Timeout"):
		return "timeout"
	}
	return "connection failed"
}

func isLocalHost(h string) bool {
	if h == "localhost" || !strings.Contains(h, ".") {
		return true // compose service names
	}
	ip := net.ParseIP(h)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

func checkQueues(ctx context.Context, c Config) Check {
	if c.Redis == nil {
		return Check{Name: CheckQueues, Status: Skipped, Reasons: []string{"source Redis not configured"}}
	}
	prefix := c.Prefix
	if prefix == "" {
		prefix = DefaultPrefix
	}
	queues := c.Queues
	if len(queues) == 0 {
		queues = DefaultQueues
	}
	ch := Check{Name: CheckQueues, Status: Pass}
	var waiting []string
	for _, q := range queues {
		active, err := queueLen(ctx, c.Redis, prefix+":"+q+":active")
		if err != nil {
			return Check{Name: CheckQueues, Status: Fail, Reasons: []string{"cannot read source Redis: " + Redact(err.Error())}}
		}
		wait, err := queueLen(ctx, c.Redis, prefix+":"+q+":wait")
		if err != nil {
			return Check{Name: CheckQueues, Status: Fail, Reasons: []string{"cannot read source Redis: " + Redact(err.Error())}}
		}
		if active > 0 {
			ch.Status = Fail
			ch.Reasons = append(ch.Reasons, fmt.Sprintf("%s: %d active job(s)", q, active))
		}
		if wait > 0 {
			waiting = append(waiting, fmt.Sprintf("%s: %d waiting", q, wait))
		}
	}
	if ch.Status == Pass {
		ch.Reasons = []string{"no active jobs in " + prefix + ":* queues"}
	}
	if len(waiting) > 0 {
		ch.Reasons = append(ch.Reasons, "note (not blocking, never delivered after cutover): "+strings.Join(waiting, ", "))
	}
	return ch
}

func queueLen(ctx context.Context, r redis.UniversalClient, key string) (int64, error) {
	typ, err := r.Type(ctx, key).Result()
	if err != nil {
		return 0, err
	}
	switch typ {
	case "list":
		return r.LLen(ctx, key).Result()
	case "zset":
		return r.ZCard(ctx, key).Result()
	case "set":
		return r.SCard(ctx, key).Result()
	}
	return 0, nil
}

// Redact drops anything that looks like a credential or connection string.
func Redact(msg string) string {
	fields := strings.Fields(msg)
	for i, f := range fields {
		l := strings.TrimLeft(strings.ToLower(f), "`'\"([{")
		if strings.Contains(l, "password") || strings.Contains(l, "://") || strings.Contains(l, "secret") || strings.Contains(f, "@") || strings.HasPrefix(l, "user=") || strings.HasPrefix(l, "host=") || strings.HasPrefix(l, "database=") || strings.HasPrefix(l, "dbname=") {
			fields[i] = "[redacted]"
		}
	}
	out := strings.Join(fields, " ")
	if len(out) > 300 {
		out = out[:300] + "…"
	}
	return out
}

// Format renders the result for a terminal.
func Format(r *Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "source check: %s (%s)\n", r.Status, r.CheckedAt.Format(time.RFC3339))
	for _, c := range r.Checks {
		fmt.Fprintf(&b, "  [%s] %s\n", strings.ToUpper(c.Status), c.Title)
		for _, x := range c.Reasons {
			fmt.Fprintf(&b, "      - %s\n", x)
		}
	}
	return b.String()
}

// Operator names who forced a run, best effort.
func Operator() string {
	for _, k := range []string{"TRANSFER_OPERATOR", "SUDO_USER", "USER", "LOGNAME"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return "unknown"
}

// Override is the record of a forced run.
type Override struct {
	Time     time.Time `json:"time"`
	Operator string    `json:"operator"`
	Via      string    `json:"via"` // cli or wizard
	Status   string    `json:"status"`
	Failed   []string  `json:"failed_checks"`
}

// NewOverride records that the result was overridden.
func NewOverride(r *Result, operator, via string) Override {
	return Override{Time: time.Now().UTC(), Operator: operator, Via: via, Status: r.Status, Failed: r.Failed()}
}

// LogLine is the WARN line every forced run prints.
func (o Override) LogLine() string {
	failed := strings.Join(o.Failed, ",")
	if failed == "" {
		failed = "none"
	}
	return fmt.Sprintf("WARN source-stopped check overridden with force via=%s time=%s operator=%s status=%s failed_checks=%s", o.Via, o.Time.Format(time.RFC3339), o.Operator, o.Status, failed)
}

// pgSource reads pg_stat_* and the post table.
type pgSource struct {
	db    *sql.DB
	table string // "note" (Misskey) or "statuses" (Mastodon)
}

// NewPGSource wraps a connection to the source database. Open it with
// application_name = AppName so it does not count as a foreign session.
func NewPGSource(db *sql.DB, table string) Source { return &pgSource{db: db, table: table} }

func (p *pgSource) Sessions(ctx context.Context) ([]Session, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT pid, coalesce(usename::text,''), coalesce(application_name,''), coalesce(host(client_addr),''), coalesce(state,''), backend_start, query_start
FROM pg_stat_activity
WHERE datname = current_database() AND pid <> pg_backend_pid()
  AND coalesce(backend_type,'client backend') = 'client backend'
  AND coalesce(application_name,'') NOT LIKE $1
ORDER BY backend_start`, ownPrefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var s Session
		var bs, qs sql.NullTime
		if err := rows.Scan(&s.PID, &s.User, &s.Application, &s.ClientAddr, &s.State, &bs, &qs); err != nil {
			return nil, err
		}
		if bs.Valid {
			s.BackendStart = &bs.Time
		}
		if qs.Valid {
			s.QueryStart = &qs.Time
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (p *pgSource) Watermark(ctx context.Context) (Watermark, error) {
	var w Watermark
	// pg_stat_clear_snapshot: never compare a cached stats snapshot.
	_, _ = p.db.ExecContext(ctx, `SELECT pg_stat_clear_snapshot()`)
	if err := p.db.QueryRowContext(ctx, `SELECT tup_inserted, tup_updated, tup_deleted FROM pg_stat_database WHERE datname = current_database()`).Scan(&w.Inserted, &w.Updated, &w.Deleted); err != nil {
		return w, err
	}
	if p.table == "" {
		return w, nil
	}
	var maxID sql.NullString
	// max(id) uses the primary key index; no count(*) on large tables.
	if err := p.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT max(id)::text FROM %q`, p.table)).Scan(&maxID); err != nil {
		return w, err
	}
	if err := p.db.QueryRowContext(ctx, `SELECT coalesce(sum(n_tup_ins),0)::bigint, coalesce(sum(n_tup_upd),0)::bigint, coalesce(sum(n_tup_del),0)::bigint
FROM pg_stat_user_tables WHERE relname = $1 AND schemaname = ANY(current_schemas(false))`, p.table).Scan(&w.NoteIns, &w.NoteUpd, &w.NoteDel); err != nil {
		return w, err
	}
	w.MaxID = maxID.String
	return w, nil
}
