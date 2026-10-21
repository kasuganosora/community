package setup

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kasuganosora/community/datatransfer/sourcecheck"
	"github.com/stretchr/testify/require"
)

// unreachableSource: nothing listens on these ports, so the DB checks fail
// (cannot connect) and the web checks pass.
func unreachableSource(t *testing.T) MisskeyParams {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return MisskeyParams{Host: "127.0.0.1", Port: port, Name: "misskey", User: "misskey", SSLMode: "disable",
		StopCheck: StopCheckParams{WebURL: "http://127.0.0.1:" + itoa(port) + "/api/meta"}}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestSourceStoppedEndpoint(t *testing.T) {
	withConfigDir(t)
	old := sourceCheckInterval
	sourceCheckInterval = time.Millisecond
	t.Cleanup(func() { sourceCheckInterval = old })
	s := NewServer("")
	h := s.Handler()
	body, _ := json.Marshal(unreachableSource(t))
	r := httptest.NewRequest(http.MethodPost, "/api/setup/test/source-stopped", strings.NewReader(string(body)))
	r.Header.Set("X-Setup-Token", s.Token())
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	require.Equal(t, 200, w.Code, w.Body.String())
	var out struct{ Result sourcecheck.Result }
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Equal(t, sourcecheck.Running, out.Result.Status, "an unreadable source DB is never reported as stopped")
	var names []string
	for _, c := range out.Result.Checks {
		names = append(names, c.Name)
	}
	require.Equal(t, sourcecheck.Order, names)
	require.Equal(t, sourcecheck.Skipped, out.Result.Checks[4].Status)
	require.NotContains(t, w.Body.String(), "password=")

	r = httptest.NewRequest(http.MethodPost, "/api/setup/test/source-stopped", strings.NewReader("{}"))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	require.Equal(t, 401, w.Code)
}

func TestGateSourceStopped(t *testing.T) {
	withConfigDir(t)
	old := sourceCheckInterval
	sourceCheckInterval = time.Millisecond
	t.Cleanup(func() { sourceCheckInterval = old })
	tr := &tracker{job: newJob("misskey"), lastWrite: time.Now()}
	r := &runner{t: tr, secrets: &jobSecrets{}}
	m := unreachableSource(t)

	err := r.gateSourceStopped(context.Background(), m)
	var se *Error
	require.True(t, errors.As(err, &se))
	require.Equal(t, "source_running", se.Code)
	require.Contains(t, se.Detail, sourcecheck.CheckSessions)
	require.NotNil(t, tr.job.SourceCheck)
	require.Empty(t, tr.job.SourceCheckOverrides)

	m.StopCheck.Force = true
	require.NoError(t, r.gateSourceStopped(context.Background(), m))
	require.Len(t, tr.job.SourceCheckOverrides, 1)
	require.Equal(t, "wizard", tr.job.SourceCheckOverrides[0].Via)
	found := false
	for _, l := range tr.job.Log {
		if l.Level == "warn" && strings.Contains(l.Message, "WARN source-stopped check overridden") && strings.Contains(l.Message, "failed_checks=sessions,note_writes") {
			found = true
		}
	}
	require.True(t, found, "a forced run logs a WARN line with the failed checks")
}
