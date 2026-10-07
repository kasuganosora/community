package admin

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAnalyticsRealDataBoundaries(t *testing.T) {
	env := newAdminAccountTestEnv(t)
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	// A completed signup cohort followed by a completely observed return week.
	signup := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	for _, item := range []struct {
		id      int
		domain  any
		created time.Time
	}{{1, nil, signup}, {2, nil, signup}, {3, "remote.example", signup}, {4, "pending.example", signup}} {
		require.NoError(t, env.gdb.Exec("INSERT INTO accounts (id, username, acct, display_name, domain, created_at) VALUES (?, ?, '', '', ?, ?)", item.id, "account", item.domain, item.created).Error)
	}
	insert := func(id, author int, domain any, created time.Time, deleted, scheduled any) {
		t.Helper()
		require.NoError(t, env.gdb.Exec("INSERT INTO statuses (id, account_id, domain, created_at, updated_at, deleted_at, scheduled_at) VALUES (?, ?, ?, ?, ?, ?, ?)", id, author, domain, created, created, deleted, scheduled).Error)
	}
	insert(1, 1, nil, now.Add(-time.Hour), nil, nil)
	insert(2, 1, nil, now.Add(-2*time.Hour), nil, nil) // distinct posting accounts
	insert(3, 3, "remote.example", now.Add(-time.Hour), nil, nil)
	insert(4, 2, nil, now.Add(-time.Hour), now, nil)
	insert(5, 2, nil, now.Add(-time.Hour), nil, now)
	insert(6, 2, nil, now.Add(time.Hour), nil, nil) // future is outside activity window
	insert(7, 1, nil, time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC), nil, nil)
	start := time.Date(2026, 4, 11, 0, 0, 0, 0, time.UTC) // now's UTC day minus 179 days
	require.Equal(t, now.Truncate(24*time.Hour).AddDate(0, 0, -179), start)
	insert(8, 2, nil, start, nil, nil)
	insert(9, 2, nil, start.Add(-time.Second), nil, nil)
	require.NoError(t, env.gdb.Exec("INSERT INTO follows (account_id,follow_id,status) VALUES (1,3,0),(3,1,0),(1,4,1)").Error)
	require.NoError(t, env.gdb.Exec("INSERT INTO emojis (shortcode,domain,url,static_url) VALUES ('local','','a','a'),('remote','remote.example','b','b')").Error)
	result, err := loadAnalytics(env.gdb, now, 7)
	require.NoError(t, err)
	require.Len(t, result.Daily, 180)
	require.Equal(t, analyticsDay{Date: "2026-10-07", LocalPosts: 2, RemotePosts: 1, PostingAccounts: 1}, result.Daily[179])
	require.EqualValues(t, 1, result.Daily[0].LocalPosts)
	require.EqualValues(t, 2, result.Totals["local_accounts"])
	require.EqualValues(t, 2, result.Totals["remote_accounts"])
	require.EqualValues(t, 2, result.Totals["instances"])
	require.EqualValues(t, 1, result.Totals["local_emojis"])
	require.EqualValues(t, 1, result.Totals["remote_emojis"])
	require.Equal(t, []analyticsDomain{{Domain: "remote.example", Count: 1}}, result.Federation["outgoing"])
	require.Equal(t, []analyticsDomain{{Domain: "remote.example", Count: 1}}, result.Federation["incoming"])
	require.Len(t, result.Cohorts, 12)
	require.Equal(t, analyticsCohort{Date: "2026-09-21", Size: 2, Retained: 1}, result.Cohorts[11])
	require.Equal(t, false, result.Online["available"])
}

func TestAnalyticsFailureIsNotZero(t *testing.T) {
	env := newAdminAccountTestEnv(t)
	c, w := newGetContext(t, nil, "days=365")
	GetAnalytics(c)
	require.Equal(t, 400, w.Code)
	c, w = newGetContext(t, nil, "days=7")
	ctx, cancel := context.WithCancel(c.Request.Context())
	cancel()
	c.Request = c.Request.WithContext(ctx)
	GetAnalytics(c)
	require.Equal(t, 500, w.Code)
	require.NotContains(t, w.Body.String(), "totals")
	require.NoError(t, env.gdb.Exec("DROP TABLE statuses").Error)
	c, w = newGetContext(t, nil, "")
	GetAnalytics(c)
	require.Equal(t, 500, w.Code)
	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Contains(t, response, "error")
	require.NotContains(t, w.Body.String(), "SELECT")
}

func TestAnalyticsSQLDateAcrossDrivers(t *testing.T) {
	for _, input := range []any{"2026-10-03", []byte("2026-10-03"), time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 3, 0, 0, 0, 0, time.FixedZone("SQL session", 9*3600))} {
		var date analyticsDate
		require.NoError(t, date.Scan(input))
		require.Equal(t, analyticsDate("2026-10-03"), date)
		encoded, err := json.Marshal(analyticsDay{Date: date, LocalPosts: 125})
		require.NoError(t, err)
		require.Contains(t, string(encoded), `"date":"2026-10-03"`)
		buckets := map[string]int64{string(date): 125}
		require.EqualValues(t, 125, buckets["2026-10-03"])
	}
	for _, invalid := range []any{nil, 42, "not-a-date", "2026-02-30"} {
		var date analyticsDate
		require.Error(t, date.Scan(invalid))
	}
}
