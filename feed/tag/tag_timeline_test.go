package tag

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTagTimeline_GetKeyFormat(t *testing.T) {
	timeline := &TagTimeline{Tag: "testtag"}

	assert.Equal(t, "feed:hashtag:%s", timeline.GetKeyFormat())
}

func TestTagTimeline_GetTimelineId(t *testing.T) {
	tests := []struct {
		name     string
		timeline *TagTimeline
		expected string
	}{
		{
			name:     "basic tag",
			timeline: &TagTimeline{Tag: "golang"},
			expected: "golang",
		},
		{
			name:     "local only",
			timeline: &TagTimeline{Tag: "golang", OnlyLocal: true},
			expected: "local:golang",
		},
		{
			name:     "remote only",
			timeline: &TagTimeline{Tag: "golang", OnlyRemote: true},
			expected: "remote:golang",
		},
		{
			name:     "media only",
			timeline: &TagTimeline{Tag: "golang", OnlyMedia: true},
			expected: "media:golang",
		},
		{
			name:     "local and media",
			timeline: &TagTimeline{Tag: "golang", OnlyLocal: true, OnlyMedia: true},
			expected: "local:media:golang",
		},
		{
			name:     "remote and media",
			timeline: &TagTimeline{Tag: "golang", OnlyRemote: true, OnlyMedia: true},
			expected: "remote:media:golang",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.timeline.GetTimelineId())
		})
	}
}

func TestTagTimeline_Struct(t *testing.T) {
	timeline := &TagTimeline{
		Tag:        "testing",
		OnlyLocal:  true,
		OnlyRemote: false,
		OnlyMedia:  true,
	}

	assert.Equal(t, "testing", timeline.Tag)
	assert.True(t, timeline.OnlyLocal)
	assert.False(t, timeline.OnlyRemote)
	assert.True(t, timeline.OnlyMedia)
}

func TestTagTimeline_VariousTags(t *testing.T) {
	tags := []string{"golang", "rust", "python", "javascript", "typescript"}

	for _, tag := range tags {
		t.Run(tag, func(t *testing.T) {
			timeline := &TagTimeline{Tag: tag}

			assert.Equal(t, tag, timeline.GetTimelineId())
			assert.Equal(t, "feed:hashtag:%s", timeline.GetKeyFormat())
		})
	}
}

func TestTagTimeline_EmptyTag(t *testing.T) {
	timeline := &TagTimeline{Tag: ""}

	assert.Equal(t, "", timeline.GetTimelineId())
	assert.Equal(t, "feed:hashtag:%s", timeline.GetKeyFormat())
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
	// Note: OnlyLocal and OnlyRemote together doesn't make sense logically
	// but we test the behavior
	timeline := &TagTimeline{
		Tag:        "test",
		OnlyLocal:  true,
		OnlyRemote: true,
		OnlyMedia:  true,
	}

	assert.Contains(t, timeline.GetTimelineId(), "test")
	assert.Contains(t, timeline.GetTimelineId(), "local")
	assert.Contains(t, timeline.GetTimelineId(), "remote")
	assert.Contains(t, timeline.GetTimelineId(), "media")
}

func TestTagTimeline_ZeroValues(t *testing.T) {
	timeline := &TagTimeline{}

	assert.Empty(t, timeline.Tag)
	assert.False(t, timeline.OnlyLocal)
	assert.False(t, timeline.OnlyRemote)
	assert.False(t, timeline.OnlyMedia)
}
