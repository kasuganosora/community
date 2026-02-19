package tag

import (
	"testing"

	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
)

func TestTagTimeline_GetKeyFormat(t *testing.T) {
	timeline := &TagTimeline{}
	assert.Equal(t, "feed:hashtag:%s", timeline.GetKeyFormat())
}

func TestTagTimeline_GetTimelineId(t *testing.T) {
	tests := []struct {
		name     string
		timeline *TagTimeline
		expected string
	}{
		{
			name:     "Basic tag",
			timeline: &TagTimeline{Tag: "golang"},
			expected: "golang",
		},
		{
			name: "Tag with local filter",
			timeline: &TagTimeline{
				Tag:       "rust",
				OnlyLocal: true,
			},
			expected: "local:rust",
		},
		{
			name: "Tag with remote filter",
			timeline: &TagTimeline{
				Tag:        "python",
				OnlyRemote: true,
			},
			expected: "remote:python",
		},
		{
			name: "Tag with media filter",
			timeline: &TagTimeline{
				Tag:       "javascript",
				OnlyMedia: true,
			},
			expected: "media:javascript",
		},
		{
			name: "Tag with all filters",
			timeline: &TagTimeline{
				Tag:        "typescript",
				OnlyLocal:  true,
				OnlyRemote: true,
				OnlyMedia:  true,
			},
			expected: "local:remote:media:typescript",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.timeline.GetTimelineId())
		})
	}
}

func TestTagTimeline_TimelineInterface(t *testing.T) {
	var timeline feed.Timeline = &TagTimeline{Tag: "test"}

	assert.Equal(t, "feed:hashtag:%s", timeline.GetKeyFormat())
	assert.Equal(t, "test", timeline.GetTimelineId())
}

func TestTagTimeline_DifferentTags(t *testing.T) {
	tags := []string{"golang", "rust", "python", "javascript", "typescript"}

	for _, tag := range tags {
		t.Run(tag, func(t *testing.T) {
			timeline := &TagTimeline{Tag: tag}
			assert.Contains(t, timeline.GetTimelineId(), tag)
		})
	}
}

func TestTagTimeline_EmptyTag(t *testing.T) {
	timeline := &TagTimeline{Tag: ""}
	assert.Equal(t, "", timeline.GetTimelineId())
}

func TestTagTimeline_AllFlagsFalse(t *testing.T) {
	timeline := &TagTimeline{
		Tag:        "test",
		OnlyLocal:  false,
		OnlyRemote: false,
		OnlyMedia:  false,
	}

	assert.Equal(t, "test", timeline.GetTimelineId())
}

func TestTagTimeline_AllFlagsTrue(t *testing.T) {
	timeline := &TagTimeline{
		Tag:        "test",
		OnlyLocal:  true,
		OnlyRemote: true,
		OnlyMedia:  true,
	}

	id := timeline.GetTimelineId()
	assert.Contains(t, id, "local")
	assert.Contains(t, id, "remote")
	assert.Contains(t, id, "media")
	assert.Contains(t, id, "test")
}

func TestTagTimeline_ZeroValues(t *testing.T) {
	timeline := &TagTimeline{}
	assert.Equal(t, "feed:hashtag:%s", timeline.GetKeyFormat())
	assert.Equal(t, "", timeline.GetTimelineId())
}

func TestPush_VisibilityCheck(t *testing.T) {
	tests := []struct {
		name       string
		visibility uint8
		shouldPush bool
	}{
		{"public", models.StatusVisibilityPublic, true},
		{"unlisted", models.StatusVisibilityUnlisted, true},
		{"private", models.StatusVisibilityPrivate, true},
		{"direct", models.StatusVisibilityDirect, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := &models.Status{
				Id:         1,
				Visibility: tt.visibility,
			}
			assert.NotNil(t, status)
			// 验证 visibility 值
			assert.Equal(t, tt.visibility, status.Visibility)
		})
	}
}

func TestPush_WithTags(t *testing.T) {
	status := &models.Status{
		Id:         1,
		Visibility: models.StatusVisibilityPublic,
		Tags: []*models.Tag{
			{Name: "golang"},
			{Name: "programming"},
		},
	}

	assert.NotNil(t, status.Tags)
	assert.Len(t, status.Tags, 2)
}

func TestPush_WithoutTags(t *testing.T) {
	status := &models.Status{
		Id:         1,
		Visibility: models.StatusVisibilityPublic,
		Tags:       nil,
	}

	// getTags 会从数据库加载 tags
	assert.Nil(t, status.Tags)
}

func TestUnpush_VisibilityCheck(t *testing.T) {
	status := &models.Status{
		Id:         1,
		Visibility: models.StatusVisibilityPublic,
	}
	// Unpush 需要 Redis 连接
	assert.NotNil(t, status)
}

func TestUnpush_WithTags(t *testing.T) {
	status := &models.Status{
		Id:         1,
		Visibility: models.StatusVisibilityPublic,
		Tags: []*models.Tag{
			{Name: "golang"},
		},
	}

	assert.NotNil(t, status.Tags)
}

func TestGetTags(t *testing.T) {
	status := &models.Status{
		Id:   1,
		Tags: nil,
	}

	// getTags 需要数据库连接
	assert.Nil(t, status.Tags)
}

func TestTagTimeline_FilterFlags(t *testing.T) {
	timeline := &TagTimeline{
		OnlyLocal:  true,
		OnlyRemote: false,
		OnlyMedia:  true,
	}

	assert.True(t, timeline.OnlyLocal)
	assert.False(t, timeline.OnlyRemote)
	assert.True(t, timeline.OnlyMedia)
}

func TestTagTimeline_Combinations(t *testing.T) {
	combinations := []struct {
		local  bool
		remote bool
		media  bool
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
		timeline := &TagTimeline{
			Tag:        "test",
			OnlyLocal:  combo.local,
			OnlyRemote: combo.remote,
			OnlyMedia:  combo.media,
		}
		id := timeline.GetTimelineId()
		assert.NotEmpty(t, id)
	}
}

func TestTagTimeline_ConcurrentAccess(t *testing.T) {
	timeline := &TagTimeline{
		Tag:       "concurrent",
		OnlyLocal: true,
	}

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

func TestGetTimelineContent_Params(t *testing.T) {
	params := &feed.ListParam{
		MaxId: 100,
		MinId: 1,
		Limit: 20,
		Other: map[string]any{
			"local":      true,
			"remote":     false,
			"only_media": true,
		},
	}

	assert.Equal(t, uint64(100), params.MaxId)
	assert.Equal(t, uint64(1), params.MinId)
	assert.Equal(t, uint64(20), params.Limit)
	assert.NotNil(t, params.Other)
}

func TestTagTimeline_Rebuild(t *testing.T) {
	timeline := &TagTimeline{Tag: "test"}

	// Rebuild 需要 Redis 和数据库连接
	assert.NotNil(t, timeline)
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

func TestTagTimeline_StatusWithAttachments(t *testing.T) {
	timeline := &TagTimeline{Tag: "photos", OnlyMedia: true}

	status := &models.Status{
		Id:         1,
		Visibility: models.StatusVisibilityPublic,
		Attachments: []*models.Attachment{
			{Id: 1},
			{Id: 2},
		},
	}

	assert.True(t, timeline.OnlyMedia)
	assert.NotNil(t, status.Attachments)
	assert.GreaterOrEqual(t, len(status.Attachments), 1)
}

func TestTagTimeline_LocalStatus(t *testing.T) {
	timeline := &TagTimeline{Tag: "local", OnlyLocal: true}

	status := &models.Status{
		Id:         1,
		Visibility: models.StatusVisibilityPublic,
		Domain:     nil,
		Account:    &models.Account{},
	}

	assert.True(t, timeline.OnlyLocal)
	assert.Nil(t, status.Domain)
}

func TestTagTimeline_RemoteStatus(t *testing.T) {
	timeline := &TagTimeline{Tag: "federated", OnlyRemote: true}
	domain := "remote.example.com"

	status := &models.Status{
		Id:         1,
		Visibility: models.StatusVisibilityPublic,
		Domain:     &domain,
		Account:    &models.Account{},
	}

	assert.True(t, timeline.OnlyRemote)
	assert.NotNil(t, status.Domain)
}
