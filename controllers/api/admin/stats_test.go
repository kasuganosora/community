package admin

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		{"below 10 percent", 5, 100, "5"}, // formatFloat(f, 1) => int64 => "5"
		{"small fraction", 1, 1000, "0"},  // 0.1 => int64(0.1) = 0
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
		{"thousands", 1500, "1K"},  // 1.5 => int64(1.5) = 1 => "1K"
		{"million", 2500000, "2M"}, // 2.5 => int64(2.5) = 2 => "2M"
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
	// precision 0 => formatInt
	assert.Equal(t, "5", formatFloat(5.7, 0))
	// precision > 0 => formatInt (simplified impl)
	assert.Equal(t, "3", formatFloat(3.9, 1))
	assert.Equal(t, "0", formatFloat(0.4, 2))
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
