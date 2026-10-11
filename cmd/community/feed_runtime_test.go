package main

import (
	"testing"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/feed"
	"github.com/stretchr/testify/require"
)

func TestFeedRuntimeRoleAndModeContract(t *testing.T) {
	t.Cleanup(func() { _ = feed.ConfigureRuntime("durable") })
	for _, role := range []string{"all", "api", "feed-worker"} {
		for _, mode := range []string{"durable", "shadow", "legacy"} {
			t.Run(role+"/"+mode, func(t *testing.T) {
				t.Setenv("COMMUNITY_ROLE", role)
				t.Setenv("FEED_MODE", mode)
				t.Setenv("FEED_SCAN_CONCURRENCY", "2")
				t.Setenv("FEED_DELIVERY_CONCURRENCY", "4")
				t.Setenv("FEED_BATCH_SIZE", "256")
				gotRole, gotMode, options, err := feedRuntimeOptions()
				if role == "feed-worker" && mode == "legacy" {
					require.Error(t, err)
					return
				}
				require.NoError(t, err)
				require.Equal(t, role, gotRole)
				require.Equal(t, mode, gotMode)
				require.Equal(t, mode == "shadow", options.Shadow)
				require.Equal(t, mode == "durable", feed.DurableFanoutEnabled())
				require.Equal(t, mode != "durable", feed.ForceDatabaseReads())
			})
		}
	}
}
func TestFeedRuntimeRejectsInvalidConfiguration(t *testing.T) {
	for _, pair := range [][2]string{{"COMMUNITY_ROLE", "unknown"}, {"FEED_MODE", "unknown"}, {"FEED_BATCH_SIZE", "127"}, {"FEED_BATCH_SIZE", "1025"}, {"FEED_SCAN_CONCURRENCY", "0"}, {"FEED_DELIVERY_CONCURRENCY", "33"}} {
		t.Run(pair[0]+pair[1], func(t *testing.T) {
			t.Setenv("COMMUNITY_ROLE", "api")
			t.Setenv("FEED_MODE", "durable")
			t.Setenv("FEED_BATCH_SIZE", "256")
			t.Setenv("FEED_SCAN_CONCURRENCY", "2")
			t.Setenv("FEED_DELIVERY_CONCURRENCY", "4")
			t.Setenv(pair[0], pair[1])
			_, _, _, err := feedRuntimeOptions()
			require.Error(t, err)
		})
	}
}

func TestFeedRuntimeModeDefaultAndPrecedence(t *testing.T) {
	previous := config.C.Other
	t.Cleanup(func() { config.C.Other = previous; _ = feed.ConfigureRuntime("durable") })
	for _, tc := range []struct{ name, environment, configured, want string }{
		{"unset", "", "", "shadow"},
		{"blank", "  ", " ", "shadow"},
		{"configuration", "", "durable", "durable"},
		{"blank_environment", " ", " durable ", "durable"},
		{"environment_wins", " shadow ", "durable", "shadow"},
		{"explicit_durable", "durable", "shadow", "durable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config.C.Other = map[string]string{"FEED_MODE": tc.configured}
			t.Setenv("FEED_MODE", tc.environment)
			t.Setenv("COMMUNITY_ROLE", "all")
			t.Setenv("FEED_BATCH_SIZE", "256")
			t.Setenv("FEED_SCAN_CONCURRENCY", "2")
			t.Setenv("FEED_DELIVERY_CONCURRENCY", "4")
			_, mode, options, err := feedRuntimeOptions()
			require.NoError(t, err)
			require.Equal(t, tc.want, mode)
			require.Equal(t, mode == "shadow", options.Shadow)
			require.Equal(t, mode == "durable", feed.DurableFanoutEnabled())
			require.Equal(t, mode != "durable", feed.ForceDatabaseReads())
		})
	}
}
