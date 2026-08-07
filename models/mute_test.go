package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMuteAccount_TableName(t *testing.T) {
	m := MuteAccount{}
	assert.Equal(t, "mute_accounts", m.TableName())
}

func TestMuteStatus_TableName(t *testing.T) {
	m := MuteStatus{}
	assert.Equal(t, "mute_statuses", m.TableName())
}

func TestMuteAccounts_ExcludeExpired(t *testing.T) {
	past := time.Now().Add(-1 * time.Hour)
	future := time.Now().Add(1 * time.Hour)

	mutes := MuteAccounts{
		{Id: 1, AccountId: 100, TargetId: 200, ExpiredAt: nil},     // no expiry = active
		{Id: 2, AccountId: 101, TargetId: 201, ExpiredAt: &future}, // future = active
		{Id: 3, AccountId: 102, TargetId: 202, ExpiredAt: &past},   // past = expired
	}

	active := mutes.ExcludeExpired()
	assert.Len(t, active, 2)
	assert.Equal(t, uint64(1), active[0].Id)
	assert.Equal(t, uint64(2), active[1].Id)
}

func TestMuteAccounts_ExcludeExpired_AllExpired(t *testing.T) {
	past := time.Now().Add(-1 * time.Hour)
	mutes := MuteAccounts{
		{Id: 1, ExpiredAt: &past},
		{Id: 2, ExpiredAt: &past},
	}
	active := mutes.ExcludeExpired()
	assert.Empty(t, active)
}

func TestMuteAccounts_ExcludeExpired_Empty(t *testing.T) {
	mutes := MuteAccounts{}
	active := mutes.ExcludeExpired()
	assert.Empty(t, active)
}

func TestMuteAccounts_IsMuted(t *testing.T) {
	mutes := MuteAccounts{
		{Id: 1, AccountId: 100, TargetId: 200},
		{Id: 2, AccountId: 101, TargetId: 201},
	}

	t.Run("muted account", func(t *testing.T) {
		// IsMuted 判断的是 accountId 是否是被静音的目标（TargetId），
		// 而不是执行静音的用户（AccountId）。
		assert.True(t, mutes.IsMuted(200))
		assert.True(t, mutes.IsMuted(201))
	})

	t.Run("muter account is not the muted target", func(t *testing.T) {
		// AccountId 是执行静音的用户，不应被判定为被静音的目标。
		assert.False(t, mutes.IsMuted(100))
		assert.False(t, mutes.IsMuted(101))
	})

	t.Run("not muted account", func(t *testing.T) {
		assert.False(t, mutes.IsMuted(999))
	})

	t.Run("empty list", func(t *testing.T) {
		empty := MuteAccounts{}
		assert.False(t, empty.IsMuted(100))
	})
}
