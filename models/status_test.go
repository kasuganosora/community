package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStatus_TableName(t *testing.T) {
	s := Status{}
	assert.Equal(t, "statuses", s.TableName())
}

func TestStatusVisibilityConstants(t *testing.T) {
	assert.Equal(t, 0, StatusVisibilityPublic)
	assert.Equal(t, 1, StatusVisibilityUnlisted)
	assert.Equal(t, 2, StatusVisibilityPrivate)
	assert.Equal(t, 3, StatusVisibilityDirect)
}

func TestStatusTypeConstants(t *testing.T) {
	assert.Equal(t, 0, StatusTypeStatus)
	assert.Equal(t, 1, StatusTypeRelog)
	assert.Equal(t, 2, StatusTypeReply)
	assert.Equal(t, 3, StatusTypeCard)
	assert.Equal(t, 4, StatusTypePoll)
}

func TestStatus_SetAllExtendFieldsLoaded(t *testing.T) {
	s := &Status{}
	assert.False(t, s.isLoadAllExtendFields)
	s.SetAllExtendFieldsLoaded()
	assert.True(t, s.isLoadAllExtendFields)
}

func TestStatus_GetVisibilityString(t *testing.T) {
	tests := []struct {
		vis      uint8
		expected string
	}{
		{StatusVisibilityPublic, "public"},
		{StatusVisibilityUnlisted, "unlisted"},
		{StatusVisibilityPrivate, "private"},
		{StatusVisibilityDirect, "direct"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			s := &Status{Visibility: tt.vis}
			assert.Equal(t, tt.expected, s.GetVisibilityString())
		})
	}
}

func TestStatus_IsReblog(t *testing.T) {
	t.Run("not reblog - nil", func(t *testing.T) {
		s := &Status{ReblogId: nil}
		assert.Nil(t, s.ReblogId)
	})

	t.Run("is reblog", func(t *testing.T) {
		reblogId := uint64(123)
		s := &Status{ReblogId: &reblogId}
		assert.NotNil(t, s.ReblogId)
		assert.Equal(t, uint64(123), *s.ReblogId)
	})
}

func TestStatus_IsReply(t *testing.T) {
	t.Run("not reply", func(t *testing.T) {
		s := &Status{InReplyToId: nil}
		assert.Nil(t, s.InReplyToId)
	})

	t.Run("is reply", func(t *testing.T) {
		replyTo := uint64(456)
		s := &Status{InReplyToId: &replyTo}
		assert.NotNil(t, s.InReplyToId)
		assert.Equal(t, uint64(456), *s.InReplyToId)
	})
}

func TestStatus_IsDirect(t *testing.T) {
	t.Run("direct visibility", func(t *testing.T) {
		s := &Status{Visibility: StatusVisibilityDirect}
		assert.Equal(t, uint8(StatusVisibilityDirect), s.Visibility)
	})
}
