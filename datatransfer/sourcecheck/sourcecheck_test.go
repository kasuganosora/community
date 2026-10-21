package sourcecheck

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type fakeSource struct {
	sessions []Session
	marks    []Watermark
	n        int
}

func (f *fakeSource) Sessions(context.Context) ([]Session, error) { return f.sessions, nil }
func (f *fakeSource) Watermark(context.Context) (Watermark, error) {
	w := f.marks[min(f.n, len(f.marks)-1)]
	f.n++
	return w, nil
}

func noSleep(context.Context, time.Duration) error { return nil }

func closedPort(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	l.Close()
	return "http://" + addr + "/api/meta"
}

func quiet() *fakeSource {
	return &fakeSource{marks: []Watermark{{Inserted: 5, Updated: 3, MaxID: "abc", NoteIns: 10, NoteUpd: 2}}}
}

func get(r *Result, name string) Check {
	for _, c := range r.Checks {
		if c.Name == name {
			return c
		}
	}
	return Check{}
}

func TestQuietIsStopped(t *testing.T) {
	r := Run(context.Background(), Config{Source: quiet(), WebURL: closedPort(t), Sleep: noSleep})
	require.Equal(t, Stopped, r.Status, Format(r))
	require.Equal(t, Skipped, get(r, "queues").Status)
	require.Equal(t, Pass, get(r, CheckWebMeta).Status)
	require.Equal(t, Pass, get(r, CheckWebPort).Status)
	var names []string
	for _, c := range r.Checks {
		names = append(names, c.Name)
		require.NotEmpty(t, c.Title)
	}
	require.Equal(t, Order, names)
	require.Contains(t, get(r, CheckWebMeta).Reasons[0], "unreachable")
}

func TestSessionsAreRunning(t *testing.T) {
	src := quiet()
	now := time.Now()
	src.sessions = []Session{{PID: 42, User: "misskey", Application: "", ClientAddr: "172.18.0.5", State: "idle", BackendStart: &now}}
	r := Run(context.Background(), Config{Source: src, WebURL: closedPort(t), Sleep: noSleep})
	require.Equal(t, Running, r.Status)
	require.Equal(t, []string{"sessions"}, r.Failed())
	require.Contains(t, strings.Join(get(r, "sessions").Reasons, "\n"), "pid=42 user=misskey")
}

func TestChangingCountersAreRunning(t *testing.T) {
	for name, b := range map[string]Watermark{
		"inserted": {Inserted: 6, Updated: 3, MaxID: "abc", NoteIns: 10, NoteUpd: 2},
		"maxid":    {Inserted: 5, Updated: 3, MaxID: "abd", NoteIns: 10, NoteUpd: 2},
		"note_ins": {Inserted: 5, Updated: 3, MaxID: "abc", NoteIns: 11, NoteUpd: 2},
		"note_upd": {Inserted: 5, Updated: 3, MaxID: "abc", NoteIns: 10, NoteUpd: 3},
		"note_del": {Inserted: 5, Updated: 3, MaxID: "abc", NoteIns: 10, NoteUpd: 2, NoteDel: 1},
	} {
		src := &fakeSource{marks: []Watermark{{Inserted: 5, Updated: 3, MaxID: "abc", NoteIns: 10, NoteUpd: 2}, b}}
		var slept time.Duration
		r := Run(context.Background(), Config{Source: src, WebURL: closedPort(t), Interval: 7 * time.Second, Sleep: func(_ context.Context, d time.Duration) error { slept = d; return nil }})
		require.Equal(t, Running, r.Status, name)
		require.Equal(t, []string{CheckNotes}, r.Failed(), name)
		require.Equal(t, 7*time.Second, slept)
	}
}

func TestWebUpIsRunning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }))
	defer srv.Close()
	r := Run(context.Background(), Config{Source: quiet(), WebURL: srv.URL + "/api/meta", Sleep: noSleep})
	require.Equal(t, Running, r.Status)
	require.Equal(t, []string{CheckWebPort, CheckWebMeta}, r.Failed())

	r = Run(context.Background(), Config{Source: quiet(), WebURL: "https://maid.example.com/api/meta", HTTP: srv.Client(), Sleep: noSleep})
	require.Contains(t, strings.Join(get(r, CheckWebMeta).Reasons, " "), "does not look like a local")
}

func TestQueues(t *testing.T) {
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	cfg := Config{Source: quiet(), WebURL: closedPort(t), Redis: rc, Prefix: "maid", Sleep: noSleep}
	mr.Lpush("maid:deliver:wait", "1")
	r := Run(context.Background(), cfg)
	require.Equal(t, Stopped, r.Status, "waiting jobs are a note only")
	require.Contains(t, strings.Join(get(r, "queues").Reasons, " "), "deliver: 1 waiting")
	mr.Lpush("maid:inbox:active", "7")
	r = Run(context.Background(), cfg)
	require.Equal(t, Running, r.Status)
	require.Equal(t, []string{"queues"}, r.Failed())
}

func TestOverrideAndRedaction(t *testing.T) {
	src := quiet()
	src.sessions = []Session{{PID: 1}}
	r := Run(context.Background(), Config{Source: src, WebURL: closedPort(t), Sleep: noSleep})
	o := NewOverride(r, "ops", "cli")
	line := o.LogLine()
	require.True(t, strings.HasPrefix(line, "WARN "))
	require.Contains(t, line, "operator=ops")
	require.Contains(t, line, "failed_checks=sessions")
	require.NotContains(t, Redact("dial postgres://u:pw@h/db failed password=x"), "pw@")
	require.NotContains(t, Redact("password=x"), "x")
	require.NotContains(t, Redact("failed to connect to `user=misskey_ro database=misskey`"), "misskey_ro")
}
