package list

import (
	"testing"

	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
)

func TestListTimeline_GetKeyFormat(t *testing.T) {
	timeline := &ListTimeline{}
	assert.Equal(t, "feed:list:%s", timeline.GetKeyFormat())
}

func TestListTimeline_GetTimelineId(t *testing.T) {
	tests := []struct {
		name     string
		timeline *ListTimeline
		expected string
	}{
		{
			name:     "Basic list",
			timeline: &ListTimeline{List: &models.List{Title: "mylist"}},
			expected: "mylist",
		},
		{
			name: "List with local filter",
			timeline: &ListTimeline{
				List:      &models.List{Title: "tech"},
				OnlyLocal: true,
			},
			expected: "local:tech",
		},
		{
			name: "List with remote filter",
			timeline: &ListTimeline{
				List:       &models.List{Title: "news"},
				OnlyRemote: true,
			},
			expected: "remote:news",
		},
		{
			name: "List with media filter",
			timeline: &ListTimeline{
				List:      &models.List{Title: "photos"},
				OnlyMedia: true,
			},
			expected: "media:photos",
		},
		{
			name: "List with all filters",
			timeline: &ListTimeline{
				List:       &models.List{Title: "all"},
				OnlyLocal:  true,
				OnlyRemote: true,
				OnlyMedia:  true,
			},
			expected: "local:remote:media:all",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.timeline.GetTimelineId())
		})
	}
}

func TestListTimeline_TimelineInterface(t *testing.T) {
	list := &models.List{Id: 1, Title: "test"}
	var timeline feed.Timeline = &ListTimeline{List: list}

	assert.Equal(t, "feed:list:%s", timeline.GetKeyFormat())
	assert.Equal(t, "test", timeline.GetTimelineId())
}

func TestListTimeline_DifferentLists(t *testing.T) {
	lists := []*models.List{
		{Id: 1, Title: "list1"},
		{Id: 2, Title: "list2"},
		{Id: 3, Title: "list3"},
	}

	for _, list := range lists {
		timeline := &ListTimeline{List: list}
		assert.NotEmpty(t, timeline.GetTimelineId())
	}
}

func TestListTimeline_FilterFlags(t *testing.T) {
	timeline := &ListTimeline{
		OnlyLocal:  true,
		OnlyRemote: false,
		OnlyMedia:  true,
	}

	assert.True(t, timeline.OnlyLocal)
	assert.False(t, timeline.OnlyRemote)
	assert.True(t, timeline.OnlyMedia)
}

func TestListTimeline_Combinations(t *testing.T) {
	// 测试所有 8 种组合
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
		timeline := &ListTimeline{
			List:       &models.List{Title: "test"},
			OnlyLocal:  combo.local,
			OnlyRemote: combo.remote,
			OnlyMedia:  combo.media,
		}
		id := timeline.GetTimelineId()
		assert.NotEmpty(t, id)
	}
}

func TestListTimeline_ConcurrentAccess(t *testing.T) {
	timeline := &ListTimeline{
		List:      &models.List{Title: "concurrent"},
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

func TestListTimeline_NilList(t *testing.T) {
	timeline := &ListTimeline{List: nil}
	// GetKeyFormat 应该仍然工作
	assert.Equal(t, "feed:list:%s", timeline.GetKeyFormat())
	// GetTimelineId 会在 nil list 上 panic，这是预期行为
}
