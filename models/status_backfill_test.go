package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestShouldBackfill(t *testing.T) {
	now := time.Now()
	require.False(t, ShouldBackfill(now.Add(-30*time.Minute), now, true), "fresh fetch is live")
	require.True(t, ShouldBackfill(now.Add(-3*time.Hour), now, true), "old fetch is backfill")
	require.False(t, ShouldBackfill(now.Add(-3*time.Hour), now, false), "slow delivery is live")
	require.True(t, ShouldBackfill(now.Add(-72*time.Hour), now, false), "catch-up delivery is backfill")
	require.False(t, ShouldBackfill(now.Add(time.Hour), now, true), "future timestamps are live")
}

func TestStatusBackfillMarker(t *testing.T) {
	var s *Status
	require.False(t, s.IsBackfilled())
	s = &Status{}
	require.False(t, s.IsBackfilled())
	s.Properties = map[string]any{"other": 1}
	s.MarkBackfilled()
	require.True(t, s.IsBackfilled())
	require.Equal(t, 1, s.Properties["other"])
}
