package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMarker_TableName(t *testing.T) {
	m := Marker{}
	assert.Equal(t, "markers", m.TableName())
}

func TestMarker_Timelines(t *testing.T) {
	now := time.Now()
	m := &Marker{
		AccountId: 1,
		Timelines: map[string]*MarkerTimelineItem{
			"home": {
				LastReadId: 12345,
				UpdatedAt:  &now,
				Version:    1,
			},
			"notifications": {
				LastReadId: 67890,
				UpdatedAt:  &now,
				Version:    3,
			},
		},
	}

	assert.Equal(t, uint64(1), m.AccountId)
	assert.Len(t, m.Timelines, 2)

	home := m.Timelines["home"]
	assert.Equal(t, uint64(12345), home.LastReadId)
	assert.Equal(t, uint64(1), home.Version)

	notifs := m.Timelines["notifications"]
	assert.Equal(t, uint64(67890), notifs.LastReadId)
	assert.Equal(t, uint64(3), notifs.Version)
}

func TestMarker_NilTimelines(t *testing.T) {
	m := &Marker{AccountId: 1}
	assert.Nil(t, m.Timelines)
}
