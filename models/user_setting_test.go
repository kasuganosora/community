package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUserSetting_TableName(t *testing.T) {
	us := UserSetting{}
	assert.Equal(t, "user_setting", us.TableName())
}

func TestUserSetting_GetAggregatesReblogs(t *testing.T) {
	t.Run("true when explicitly set", func(t *testing.T) {
		us := &UserSetting{Data: map[string]any{"aggregates_reblogs": true}} //nolint:goconst
		assert.True(t, us.GetAggregatesReblogs())
	})

	t.Run("false when explicitly set", func(t *testing.T) {
		us := &UserSetting{Data: map[string]any{"aggregates_reblogs": false}}
		assert.False(t, us.GetAggregatesReblogs())
	})

	t.Run("defaults to true when missing", func(t *testing.T) {
		us := &UserSetting{Data: map[string]any{}}
		assert.True(t, us.GetAggregatesReblogs())
	})

	t.Run("defaults to true when nil data", func(t *testing.T) {
		us := &UserSetting{}
		assert.True(t, us.GetAggregatesReblogs())
	})

	t.Run("defaults to true when wrong type", func(t *testing.T) {
		us := &UserSetting{Data: map[string]any{"aggregates_reblogs": "yes"}}
		assert.True(t, us.GetAggregatesReblogs())
	})
}

func TestUserSetting_IsFollowProtect(t *testing.T) {
	t.Run("true when explicitly set", func(t *testing.T) {
		us := &UserSetting{Data: map[string]any{"follow_protect": true}} //nolint:goconst
		assert.True(t, us.IsFollowProtect())
	})

	t.Run("false when explicitly set", func(t *testing.T) {
		us := &UserSetting{Data: map[string]any{"follow_protect": false}}
		assert.False(t, us.IsFollowProtect())
	})

	t.Run("defaults to false when missing", func(t *testing.T) {
		us := &UserSetting{Data: map[string]any{}}
		assert.False(t, us.IsFollowProtect())
	})

	t.Run("defaults to false when nil data", func(t *testing.T) {
		us := &UserSetting{}
		assert.False(t, us.IsFollowProtect())
	})

	t.Run("defaults to false when wrong type", func(t *testing.T) {
		us := &UserSetting{Data: map[string]any{"follow_protect": 1}}
		assert.False(t, us.IsFollowProtect())
	})
}
