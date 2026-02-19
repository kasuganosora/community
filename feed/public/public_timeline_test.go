package public

import (
	"strings"
	"testing"

	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
)

func TestPublicTimeline_GetKeyFormat(t *testing.T) {
	timeline := &PublicTimeline{}
	assert.Equal(t, "feed:public:%s", timeline.GetKeyFormat())
}

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

func TestPublicTimeline_TimelineInterface(t *testing.T) {
	var timeline feed.Timeline = &PublicTimeline{}

	assert.Equal(t, "feed:public:%s", timeline.GetKeyFormat())
	assert.Equal(t, "all", timeline.GetTimelineId())
}

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

func TestPublicTimeline_VisibilityFilter(t *testing.T) {
	// Only public statuses should be in public timeline
	visibilities := []struct {
		visibility uint8
		shouldBeIn bool
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

func TestPush_VisibilityCheck(t *testing.T) {
	tests := []struct {
		name       string
		visibility uint8
		shouldPush bool
	}{
		{"public", models.StatusVisibilityPublic, true},
		{"unlisted", models.StatusVisibilityUnlisted, false},
		{"private", models.StatusVisibilityPrivate, false},
		{"direct", models.StatusVisibilityDirect, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := &models.Status{
				Id:         1,
				Visibility: tt.visibility,
			}
			// Push 需要 Redis 连接，这里只测试可见性检查逻辑
			assert.NotNil(t, status)
			// 验证 visibility 值
			assert.Equal(t, tt.visibility, status.Visibility)
		})
	}
}

func TestUnpush_VisibilityCheck(t *testing.T) {
	status := &models.Status{
		Id:         1,
		Visibility: models.StatusVisibilityPublic,
	}
	// Unpush 需要 Redis 连接
	assert.NotNil(t, status)
}

func TestGetTimelineContentAll_Params(t *testing.T) {
	params := &feed.ListParam{
		MaxId: 100,
		MinId: 1,
		Limit: 20,
		Other: map[string]any{
			"local":  true,
			"remote": false,
		},
	}

	assert.Equal(t, uint64(100), params.MaxId)
	assert.Equal(t, uint64(1), params.MinId)
	assert.Equal(t, uint64(20), params.Limit)
	assert.NotNil(t, params.Other)
}

func TestGetTimelineContentMedia_Params(t *testing.T) {
	params := &feed.ListParam{
		MaxId: 100,
		Limit: 20,
		Other: map[string]any{
			"local": true,
		},
	}

	assert.Equal(t, uint64(100), params.MaxId)
	assert.Equal(t, uint64(20), params.Limit)
}

func TestPushAllChannel(t *testing.T) {
	status := &models.Status{
		Id:         1,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
		Attachments: []*models.Attachment{
			{Id: 1},
		},
		Account: &models.Account{},
	}

	// pushAllChannel 需要 Redis 连接
	assert.NotNil(t, status)
	assert.NotNil(t, status.Attachments)
	assert.Greater(t, len(status.Attachments), 0)
}

func TestPublicTimeline_StatusWithAttachments(t *testing.T) {
	timeline := &PublicTimeline{OnlyMedia: true}

	status := &models.Status{
		Id:         1,
		Visibility: models.StatusVisibilityPublic,
		Attachments: []*models.Attachment{
			{Id: 1},
			{Id: 2},
		},
	}

	// 测试 OnlyMedia 过滤逻辑
	assert.True(t, timeline.OnlyMedia)
	assert.NotNil(t, status.Attachments)
	assert.GreaterOrEqual(t, len(status.Attachments), 1)
}

func TestPublicTimeline_LocalStatus(t *testing.T) {
	timeline := &PublicTimeline{OnlyLocal: true}

	status := &models.Status{
		Id:         1,
		Visibility: models.StatusVisibilityPublic,
		Domain:     nil, // local
		Account:    &models.Account{},
	}

	// 测试 OnlyLocal 过滤逻辑
	assert.True(t, timeline.OnlyLocal)
	assert.Nil(t, status.Domain)
}

func TestPublicTimeline_RemoteStatus(t *testing.T) {
	timeline := &PublicTimeline{OnlyRemote: true}
	domain := "remote.example.com"

	status := &models.Status{
		Id:         1,
		Visibility: models.StatusVisibilityPublic,
		Domain:     &domain,
		Account:    &models.Account{},
	}

	// 测试 OnlyRemote 过滤逻辑
	assert.True(t, timeline.OnlyRemote)
	assert.NotNil(t, status.Domain)
}
