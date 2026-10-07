package admin

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ============== Utility functions ==============

func TestCalculatePercentage(t *testing.T) {
	cases := []struct {
		name        string
		part, total int64
		want        string
	}{
		{"zero total", 5, 0, "0"},
		{"zero part", 0, 100, "0"},
		{"exactly 100", 100, 100, "100"},
		{"over 100 clamped", 150, 100, "100"},
		{"above 10 percent", 25, 100, "25"},
		{"below 10 percent", 5, 100, "5"},
		{"small fraction", 1, 1000, "0.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, calculatePercentage(tc.part, tc.total))
		})
	}
}

func TestFormatNumber(t *testing.T) {
	cases := []struct {
		name string
		n    int64
		want string
	}{
		{"zero", 0, "0"},
		{"small", 42, "42"},
		{"negative", -42, "-42"},
		{"thousands", 1500, "1.5K"},
		{"million", 2500000, "2.5M"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, formatNumber(tc.n))
		})
	}
}

func TestFormatInt(t *testing.T) {
	assert.Equal(t, "0", formatInt(0))
	assert.Equal(t, "123", formatInt(123))
	assert.Equal(t, "-456", formatInt(-456))
	assert.Equal(t, "999999999", formatInt(999999999))
}

func TestFormatFloat(t *testing.T) {
	assert.Equal(t, "6", formatFloat(5.7, 0))
	assert.Equal(t, "3.9", formatFloat(3.9, 1))
	assert.Equal(t, "0.4", formatFloat(0.4, 2))
}

// ============== GetDimensions ==============

func TestGetDimensions_Empty(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newGetContext(t, nil, "")
	GetDimensions(c)

	assert.Equal(t, 200, w.Code)
	var resp []any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp)
}

func TestGetDimensions_Languages(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newGetContext(t, nil, "keys[]=languages")
	GetDimensions(c)

	assert.Equal(t, 200, w.Code)
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp, 1)
	assert.Equal(t, "languages", resp[0]["key"])
}

func TestGetDimensions_Sources(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newGetContext(t, nil, "keys[]=sources")
	GetDimensions(c)

	assert.Equal(t, 200, w.Code)
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp, 1)
	assert.Equal(t, "sources", resp[0]["key"])
}

func TestGetDimensions_Servers(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newGetContext(t, nil, "keys[]=servers")
	GetDimensions(c)

	assert.Equal(t, 200, w.Code)
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp, 1)
	assert.Equal(t, "servers", resp[0]["key"])
}

func TestGetDimensions_SpaceUsage(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newGetContext(t, nil, "keys[]=space_usage")
	GetDimensions(c)

	assert.Equal(t, 200, w.Code)
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp, 1)
	assert.Equal(t, "space_usage", resp[0]["key"])
}

func TestGetDimensions_SoftwareVersions(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newGetContext(t, nil, "keys[]=software_versions")
	GetDimensions(c)

	assert.Equal(t, 200, w.Code)
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp, 1)
	assert.Equal(t, "software_versions", resp[0]["key"])
}

func TestGetDimensions_UnknownKey(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newGetContext(t, nil, "keys[]=bogus")
	GetDimensions(c)

	assert.Equal(t, 200, w.Code)
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp, 1)
	assert.Equal(t, "bogus", resp[0]["key"])
}

func TestGetDimensions_MultipleKeys(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newGetContext(t, nil, "keys[]=languages&keys[]=space_usage")
	GetDimensions(c)

	assert.Equal(t, 200, w.Code)
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp, 2)
}

func TestGetDimensions_LegacyKeysParam(t *testing.T) {
	// keys (without []) should also work
	newAdminTestEnv(t)
	c, w := newGetContext(t, nil, "keys=languages")
	GetDimensions(c)

	assert.Equal(t, 200, w.Code)
}

// ============== GetMeasures ==============

func TestGetMeasures_Empty(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newGetContext(t, nil, "")
	GetMeasures(c)

	assert.Equal(t, 200, w.Code)
	var resp []any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp)
}

func TestGetMeasures_AllKnown(t *testing.T) {
	keys := []string{
		"active_users", "new_users", "interactions",
		"opened_reports", "resolved_reports",
		"status_count", "user_count",
	}
	newAdminTestEnv(t)

	for _, k := range keys {
		c, w := newGetContext(t, nil, "keys[]="+k)
		GetMeasures(c)
		assert.Equal(t, 200, w.Code, "measure %s failed", k)

		var resp []map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		require.Len(t, resp, 1)
		assert.Equal(t, k, resp[0]["key"])
	}
}

func TestGetMeasures_UnknownKey(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newGetContext(t, nil, "keys[]=nonexistent")
	GetMeasures(c)

	assert.Equal(t, 200, w.Code)
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp, 1)
	assert.Equal(t, "nonexistent", resp[0]["key"])
	assert.Equal(t, "0", resp[0]["total"])
}

func TestGetMeasures_Multiple(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newGetContext(t, nil, "keys[]=active_users&keys[]=status_count")
	GetMeasures(c)

	assert.Equal(t, 200, w.Code)
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp, 2)
}

// ============== GetRetention ==============

func TestGetRetention_DefaultFrequency(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newGetContext(t, nil, "")
	GetRetention(c)

	assert.Equal(t, 200, w.Code)
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp, 12)
	// default frequency is "day"
	for _, cohort := range resp {
		assert.Equal(t, "day", cohort["frequency"])
	}
}

func TestGetRetention_CustomFrequency(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newGetContext(t, nil, "frequency=week")
	GetRetention(c)

	assert.Equal(t, 200, w.Code)
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp, 12)
	for _, cohort := range resp {
		assert.Equal(t, "week", cohort["frequency"])
	}
}

func TestGetRetention_HasZeroPercentForEmpty(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newGetContext(t, nil, "")
	GetRetention(c)

	assert.Equal(t, 200, w.Code)
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	for _, cohort := range resp {
		// No accounts created => cohortSize 0 => "0" percent
		assert.Equal(t, "0", cohort["percent"])
	}
}

func TestStatsRealSchemaAggregation(t *testing.T) {
	// SQLExec's aggregate join projection drops application.name; use real SQL.
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	raw, err := conn.DB()
	require.NoError(t, err)
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	for _, ddl := range []string{
		"CREATE TABLE statuses (id INTEGER PRIMARY KEY, language TEXT, application_id INTEGER, domain TEXT, reblog_id INTEGER, scheduled_at DATETIME, deleted_at DATETIME)",
		"CREATE TABLE applications (id INTEGER PRIMARY KEY, name TEXT)",
		"CREATE TABLE reports (id INTEGER PRIMARY KEY, action_taken_at DATETIME, deleted_at DATETIME)",
	} {
		require.NoError(t, conn.Exec(ddl).Error)
	}
	env := &adminTestEnv{gdb: conn}
	require.NoError(t, conn.Exec("INSERT INTO applications (id,name) VALUES (1,'Stats client')").Error)
	require.NoError(t, conn.Exec("INSERT INTO statuses (id,language,application_id) VALUES (1,'zh',1)").Error)
	for _, key := range []string{"languages", "sources"} {
		result, err := getStatsDimension(env.gdb, key)
		require.NoError(t, err)
		rows := result["data"].([]gin.H)
		require.Len(t, rows, 1)
		assert.EqualValues(t, 1, rows[0]["value"])
		if key == "languages" {
			assert.Equal(t, "zh", rows[0]["key"])
		} else {
			assert.Equal(t, "Stats client", rows[0]["human_key"])
		}
	}
	now := time.Now()
	require.NoError(t, conn.Exec("INSERT INTO reports (id) VALUES (1)").Error)
	require.NoError(t, conn.Exec("INSERT INTO reports (id,action_taken_at) VALUES (2,?)", now).Error)
	opened, err := getStatsMeasure(env.gdb, "opened_reports", now)
	require.NoError(t, err)
	assert.Equal(t, "2", opened["total"])
	resolved, err := getStatsMeasure(env.gdb, "resolved_reports", now)
	require.NoError(t, err)
	assert.Equal(t, "1", resolved["total"])
	// Deleted/scheduled content must not inflate published-post statistics.
	require.NoError(t, conn.Exec("UPDATE statuses SET scheduled_at = ? WHERE id = 1", now).Error)
	result, err := getStatsMeasure(env.gdb, "status_count", now)
	require.NoError(t, err)
	assert.Equal(t, "0", result["total"])
}

func TestStatsExactTotalsAndThirtyDayWindow(t *testing.T) {
	env := newAdminTestEnv(t)
	now := time.Now().UTC()
	old := now.AddDate(0, 0, -31)
	recent := now.Add(-time.Hour)
	// Direct SQL fixtures avoid unrelated create hooks while exercising the real schema.
	for i := 0; i < 1001; i++ {
		require.NoError(t, env.gdb.Exec("INSERT INTO accounts (username, acct, display_name, created_at, last_activity_at) VALUES (?, '', '', ?, ?)", formatInt(int64(i)), recent, recent).Error)
	}
	require.NoError(t, env.gdb.Exec("INSERT INTO accounts (username, acct, display_name, created_at, last_activity_at) VALUES (?, '', '', ?, ?)", "old", old, old).Error)
	require.NoError(t, env.gdb.Exec("INSERT INTO accounts (username, acct, display_name, domain, created_at, last_activity_at) VALUES (?, '', '', ?, ?, ?)", "remote", "example.org", recent, recent).Error)
	for key, want := range map[string]string{"user_count": "1002", "new_users": "1001", "active_users": "1001"} {
		result, err := getStatsMeasure(env.gdb, key, now)
		require.NoError(t, err)
		assert.Equal(t, want, result["total"])
	}
	assert.Equal(t, "-9223372036854775808", formatInt(math.MinInt64))
}

func TestStatsDatabaseErrorsAndCancellation(t *testing.T) {
	for _, handler := range []struct {
		name  string
		run   gin.HandlerFunc
		query string
	}{
		{"dimensions", GetDimensions, "keys[]=languages"}, {"measures", GetMeasures, "keys[]=active_users"}, {"retention", GetRetention, ""},
	} {
		t.Run(handler.name, func(t *testing.T) {
			env := newAdminTestEnv(t)
			c, w := newGetContext(t, nil, handler.query)
			ctx, cancel := context.WithCancel(c.Request.Context())
			cancel()
			c.Request = c.Request.WithContext(ctx)
			handler.run(c)
			assert.Equal(t, 500, w.Code)
			require.NoError(t, env.gdb.Migrator().DropTable(&models.Account{}))
			if handler.name == "dimensions" {
				require.NoError(t, env.gdb.Migrator().DropTable(&models.Status{}))
			}
			c, w = newGetContext(t, nil, handler.query)
			handler.run(c)
			assert.Equal(t, 500, w.Code)
			assert.NotContains(t, w.Body.String(), "SELECT")
		})
	}
}

func TestRetentionActualFrequencyAndCohort(t *testing.T) {
	env := newAdminTestEnv(t)
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	created := today.Add(-12 * time.Hour)
	require.NoError(t, env.gdb.Exec("INSERT INTO accounts (username, acct, display_name, created_at, last_status_at) VALUES (?, '', '', ?, ?)", "retained", created, today).Error)
	require.NoError(t, env.gdb.Exec("INSERT INTO accounts (username, acct, display_name, created_at, last_status_at) VALUES (?, '', '', ?, ?)", "not-retained", created, created).Error)
	for _, frequency := range []string{"day", "week"} {
		c, w := newGetContext(t, nil, "frequency="+frequency)
		GetRetention(c)
		require.Equal(t, 200, w.Code)
		var rows []struct {
			Date       string
			CohortSize int64 `json:"cohort_size"`
			Percent    string
			Retained   int64
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rows))
		require.Len(t, rows, 12)
		first, err := time.Parse("2006-01-02", rows[0].Date)
		require.NoError(t, err)
		second, err := time.Parse("2006-01-02", rows[1].Date)
		require.NoError(t, err)
		if frequency == "day" {
			assert.Equal(t, 24*time.Hour, second.Sub(first))
			assert.EqualValues(t, 2, rows[11].CohortSize)
			assert.Equal(t, "50", rows[11].Percent)
			assert.EqualValues(t, 1, rows[11].Retained)
		} else {
			assert.Equal(t, 7*24*time.Hour, second.Sub(first))
			assert.Equal(t, time.Monday, first.Weekday())
		}
	}
	c, w := newGetContext(t, nil, "frequency=year")
	GetRetention(c)
	assert.Equal(t, 400, w.Code)
}
