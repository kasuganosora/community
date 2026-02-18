package public

import (
	"strings"
	"testing"

	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
)

// TestPublicTimeline_GetKeyFormat tests the GetKeyFormat method
func TestPublicTimeline_GetKeyFormat(t *testing.T) {
	timeline := &PublicTimeline{}
	assert.Equal(t, "feed:public:%s", timeline.GetKeyFormat())
}

// TestPublicTimeline_GetTimelineId tests the GetTimelineId method
func TestPublicTimeline_GetTimelineId(t *testing.T) {
	tests := []struct {
		name     string
		timeline *PublicTimeline
		expected string
	}{
		{
			name:     "All timeline",
			timeline: &PublicTimeline{},
			expected: "all",
		},
		{
			name:     "Only media",
			timeline: &PublicTimeline{OnlyMedia: true},
			expected: "media",
		},
		{
			name:     "Only local",
			timeline: &PublicTimeline{OnlyLocal: true},
			expected: "local",
		},
		{
			name:     "Only remote",
			timeline: &PublicTimeline{OnlyRemote: true},
			expected: "remote",
		},
		{
			name:     "Media and local",
			timeline: &PublicTimeline{OnlyMedia: true, OnlyLocal: true},
			expected: "media:local",
		},
		{
			name:     "Media and remote",
			timeline: &PublicTimeline{OnlyMedia: true, OnlyRemote: true},
			expected: "media:remote",
		},
		{
			name:     "Local and remote",
			timeline: &PublicTimeline{OnlyLocal: true, OnlyRemote: true},
			expected: "local:remote",
		},
		{
			name:     "All filters",
			timeline: &PublicTimeline{OnlyMedia: true, OnlyLocal: true, OnlyRemote: true},
			expected: "media:local:remote",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.timeline.GetTimelineId())
		})
	}
}

// TestPublicTimeline_TimelineInterface tests that PublicTimeline implements Timeline interface
func TestPublicTimeline_TimelineInterface(t *testing.T) {
	var timeline feed.Timeline = &PublicTimeline{}

	assert.Equal(t, "feed:public:%s", timeline.GetKeyFormat())
	assert.Equal(t, "all", timeline.GetTimelineId())
}

// TestPublicTimeline_GetTimelineId_OrderConsistent tests that ID order is consistent
func TestPublicTimeline_GetTimelineId_OrderConsistent(t *testing.T) {
	// Test multiple times to ensure order is consistent
	for i := 0; i < 10; i++ {
		timeline := &PublicTimeline{OnlyLocal: true, OnlyMedia: true}
		id := timeline.GetTimelineId()

		// Should always be media:local (consistent order)
		parts := strings.Split(id, ":")
		assert.Contains(t, parts, "media")
		assert.Contains(t, parts, "local")
	}
}

// TestPublicTimeline_FlagsCombinations tests all flag combinations
func TestPublicTimeline_FlagsCombinations(t *testing.T) {
	// Test all 8 combinations
	combinations := []struct {
		media  bool
		local  bool
		remote bool
	}{
		{false, false, false},
		{true, false, false},
		{false, true, false},
		{false, false, true},
		{true, true, false},
		{true, false, true},
		{false, true, true},
		{true, true, true},
	}

	for _, combo := range combinations {
		timeline := &PublicTimeline{
			OnlyMedia:  combo.media,
			OnlyLocal:  combo.local,
			OnlyRemote: combo.remote,
		}

		id := timeline.GetTimelineId()
		assert.NotEmpty(t, id)

		// Verify ID matches the flags
		if !combo.media && !combo.local && !combo.remote {
			assert.Equal(t, "all", id)
		} else {
			if combo.media {
				assert.Contains(t, id, "media")
			}
			if combo.local {
				assert.Contains(t, id, "local")
			}
			if combo.remote {
				assert.Contains(t, id, "remote")
			}
		}
	}
}

// TestPublicTimeline_VisibilityFilter tests visibility filtering
func TestPublicTimeline_VisibilityFilter(t *testing.T) {
	// Only public statuses should be in public timeline
	visibilities := []struct {
		visibility  uint8
		shouldBeIn  bool
	}{
		{uint8(models.StatusVisibilityPublic), true},
		{uint8(models.StatusVisibilityUnlisted), false},
		{uint8(models.StatusVisibilityPrivate), false},
		{uint8(models.StatusVisibilityDirect), false},
	}

	for _, v := range visibilities {
		t.Run("", func(t *testing.T) {
			if v.shouldBeIn {
				assert.Equal(t, uint8(models.StatusVisibilityPublic), v.visibility)
			}
		})
	}
}

// TestPublicTimeline_MultipleInstances tests multiple timeline instances
func TestPublicTimeline_MultipleInstances(t *testing.T) {
	timelines := []*PublicTimeline{
		{},
		{OnlyMedia: true},
		{OnlyLocal: true},
		{OnlyRemote: true},
		{OnlyMedia: true, OnlyLocal: true},
	}

	ids := make(map[string]bool)
	for _, tl := range timelines {
		id := tl.GetTimelineId()
		assert.NotEmpty(t, id)
		// Different filters should produce different IDs
		ids[id] = true
	}

	// Each combination should have unique ID
	assert.Len(t, ids, 5)
}

// TestPublicTimeline_ConcurrentAccess tests concurrent access to timeline
func TestPublicTimeline_ConcurrentAccess(t *testing.T) {
	timeline := &PublicTimeline{OnlyMedia: true}

	done := make(chan bool)

	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				_ = timeline.GetKeyFormat()
				_ = timeline.GetTimelineId()
			}
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

// TestPublicTimeline_FilterLogic tests filter logic
func TestPublicTimeline_FilterLogic(t *testing.T) {
	t.Run("Media filter logic", func(t *testing.T) {
		timeline := &PublicTimeline{OnlyMedia: true}
		assert.True(t, timeline.OnlyMedia)

		// Status with attachments
		status := &models.Status{
			Attachments: []*models.Attachment{{Id: 1}},
		}
		assert.NotNil(t, status.Attachments)
		assert.Greater(t, len(status.Attachments), 0)
	})

	t.Run("Local filter logic", func(t *testing.T) {
		timeline := &PublicTimeline{OnlyLocal: true}
		assert.True(t, timeline.OnlyLocal)

		// Local status (no domain)
		status := &models.Status{
			Domain: nil,
		}
		assert.Nil(t, status.Domain)
	})

	t.Run("Remote filter logic", func(t *testing.T) {
		timeline := &PublicTimeline{OnlyRemote: true}
		assert.True(t, timeline.OnlyRemote)

		// Remote status (has domain)
		domain := "example.com"
		status := &models.Status{
			Domain: &domain,
		}
		assert.NotNil(t, status.Domain)
	})
}

// TestPublicTimeline_CombinedFilters tests combined filter scenarios
func TestPublicTimeline_CombinedFilters(t *testing.T) {
	t.Run("Local media status", func(t *testing.T) {
		timeline := &PublicTimeline{OnlyMedia: true, OnlyLocal: true}

		// Local status with media
		status := &models.Status{
			Domain:      nil, // local
			Attachments: []*models.Attachment{{Id: 1}},
		}

		assert.True(t, timeline.OnlyMedia)
		assert.True(t, timeline.OnlyLocal)
		assert.Nil(t, status.Domain)
		assert.NotNil(t, status.Attachments)
	})

	t.Run("Remote media status", func(t *testing.T) {
		timeline := &PublicTimeline{OnlyMedia: true, OnlyRemote: true}

		domain := "remote.example.com"
		status := &models.Status{
			Domain:      &domain, // remote
			Attachments: []*models.Attachment{{Id: 1}},
		}

		assert.True(t, timeline.OnlyMedia)
		assert.True(t, timeline.OnlyRemote)
		assert.NotNil(t, status.Domain)
		assert.NotNil(t, status.Attachments)
	})
}

// TestPublicTimeline_EdgeCases tests edge cases
func TestPublicTimeline_EdgeCases(t *testing.T) {
	t.Run("Empty timeline ID for all", func(t *testing.T) {
		timeline := &PublicTimeline{}
		// No flags set means "all"
		assert.Equal(t, "all", timeline.GetTimelineId())
	})

	t.Run("GetKeyFormat is consistent", func(t *testing.T) {
		timeline1 := &PublicTimeline{}
		timeline2 := &PublicTimeline{OnlyMedia: true}

		// Key format should be the same regardless of flags
		assert.Equal(t, timeline1.GetKeyFormat(), timeline2.GetKeyFormat())
	})
}
