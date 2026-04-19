package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestAnnouncement_TableName(t *testing.T) {
	a := Announcement{}
	assert.Equal(t, "announcements", a.TableName())
}

func TestAnnouncement_Publish(t *testing.T) {
	a := &Announcement{
		Published:   false,
		PublishedAt: nil,
	}

	a.Publish()

	assert.True(t, a.Published)
	assert.NotNil(t, a.PublishedAt)
	// PublishedAt should be a zero-valued time pointer
	assert.True(t, a.PublishedAt.IsZero())
}

func TestAnnouncement_UnPublish(t *testing.T) {
	now := time.Now()
	a := &Announcement{
		Published:   true,
		PublishedAt: &now,
	}

	a.UnPublish()

	assert.False(t, a.Published)
	assert.Nil(t, a.PublishedAt)
}

func TestAnnouncement_Publish_UnPublish_RoundTrip(t *testing.T) {
	a := &Announcement{}

	// Publish then unpublish
	a.Publish()
	assert.True(t, a.Published)
	assert.NotNil(t, a.PublishedAt)

	a.UnPublish()
	assert.False(t, a.Published)
	assert.Nil(t, a.PublishedAt)

	// Publish again
	a.Publish()
	assert.True(t, a.Published)
	assert.NotNil(t, a.PublishedAt)
}

func TestAnnouncement_Fields(t *testing.T) {
	now := time.Now()
	a := Announcement{
		Id:                1,
		Text:              "Hello world",
		Published:         true,
		AllDay:            true,
		ScheduledAt:       &now,
		StartsAt:          &now,
		EndsAt:            nil,
		PublishedAt:       &now,
		StatusIds:         []uint64{1, 2, 3},
		MentionAccountIds: []uint64{10, 20},
		Tags:              []string{"announcement", "important"},
		EmojisId:          []uint64{100},
	}

	assert.Equal(t, uint64(1), a.Id)
	assert.Equal(t, "Hello world", a.Text)
	assert.True(t, a.Published)
	assert.True(t, a.AllDay)
	assert.NotNil(t, a.ScheduledAt)
	assert.NotNil(t, a.StartsAt)
	assert.Nil(t, a.EndsAt)
	assert.NotNil(t, a.PublishedAt)
	assert.Equal(t, []uint64{1, 2, 3}, a.StatusIds)
	assert.Equal(t, []uint64{10, 20}, a.MentionAccountIds)
	assert.Equal(t, []string{"announcement", "important"}, a.Tags)
	assert.Equal(t, []uint64{100}, a.EmojisId)
}
