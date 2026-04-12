package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplicationScope_HasScope(t *testing.T) {
	scope := ApplicationScope{
		ReadScope:  AppScopeReadAccounts | AppScopeReadBlocks,
		WriteScope: AppScopeWriteStatuses,
	}

	t.Run("has read scope", func(t *testing.T) {
		assert.True(t, scope.HasScope(AppScopeTypeRead, AppScopeReadAccounts))
		assert.True(t, scope.HasScope(AppScopeTypeRead, AppScopeReadBlocks))
	})

	t.Run("missing read scope", func(t *testing.T) {
		assert.False(t, scope.HasScope(AppScopeTypeRead, AppScopeReadNotifications))
	})

	t.Run("has write scope", func(t *testing.T) {
		assert.True(t, scope.HasScope(AppScopeTypeWrite, AppScopeWriteStatuses))
	})

	t.Run("cumulative write scope includes lower scopes", func(t *testing.T) {
		// AppScopeWriteStatuses = 0x1FFF includes all lower write scope bits
		assert.True(t, scope.HasScope(AppScopeTypeWrite, AppScopeWriteAccounts))
	})

	t.Run("missing higher write scope", func(t *testing.T) {
		// WriteScope is 0x1FFF, admin write scopes are separate
		assert.False(t, scope.HasScope(AppScopeTypeAdmin, AppScopeAdminWriteAccounts))
	})

	t.Run("invalid scope type", func(t *testing.T) {
		assert.False(t, scope.HasScope(255, AppScopeReadAccounts))
	})
}

func TestApplicationScope_AddScope(t *testing.T) {
	t.Run("add read scope via pointer receiver", func(t *testing.T) {
		scope := &ApplicationScope{}
		scope.AddScope(AppScopeTypeRead, AppScopeReadAccounts)
		assert.True(t, scope.HasScope(AppScopeTypeRead, AppScopeReadAccounts))
	})

	t.Run("add write scope", func(t *testing.T) {
		scope := &ApplicationScope{}
		scope.AddScope(AppScopeTypeWrite, AppScopeWriteStatuses)
		assert.True(t, scope.HasScope(AppScopeTypeWrite, AppScopeWriteStatuses))
	})

	t.Run("add admin scope", func(t *testing.T) {
		scope := &ApplicationScope{}
		scope.AddScope(AppScopeTypeAdmin, AppScopeAdminRead)
		assert.True(t, scope.HasScope(AppScopeTypeAdmin, AppScopeAdminRead))
	})

	t.Run("add multiple scopes", func(t *testing.T) {
		scope := &ApplicationScope{}
		scope.AddScope(AppScopeTypeRead, AppScopeReadAccounts)
		scope.AddScope(AppScopeTypeRead, AppScopeReadBlocks)
		assert.True(t, scope.HasScope(AppScopeTypeRead, AppScopeReadAccounts))
		assert.True(t, scope.HasScope(AppScopeTypeRead, AppScopeReadBlocks))
	})
}

func TestApplicationScope_RemoveScope(t *testing.T) {
	t.Run("remove all read scope", func(t *testing.T) {
		scope := &ApplicationScope{ReadScope: AppScopeReadStatuses} // 0x7FF
		scope.RemoveScope(AppScopeTypeRead, AppScopeReadStatuses)
		assert.Equal(t, uint64(0), scope.ReadScope)
		assert.False(t, scope.HasScope(AppScopeTypeRead, AppScopeReadAccounts))
	})

	t.Run("remove higher bits keeps lower bits", func(t *testing.T) {
		// 0x7FF = AppScopeReadStatuses, remove 0x400 (bit 10) keeps 0x3FF
		scope := &ApplicationScope{ReadScope: AppScopeReadStatuses}
		scope.RemoveScope(AppScopeTypeRead, 0x400)
		assert.True(t, scope.HasScope(AppScopeTypeRead, AppScopeReadSearch))    // 0x3FF still set
		assert.False(t, scope.HasScope(AppScopeTypeRead, AppScopeReadStatuses)) // 0x7FF no longer
	})
}

func TestApplicationScope_MarshalUnmarshal(t *testing.T) {
	t.Run("roundtrip", func(t *testing.T) {
		original := ApplicationScope{
			ReadScope:  0x1234567890ABCDEF,
			WriteScope: 0xFEDCBA0987654321,
			AdminScope: 0x1111222233334444,
			OtherScope: 0x5555666677778888,
		}

		data, err := original.MarshalJSON()
		require.NoError(t, err)
		assert.NotEmpty(t, data)

		var decoded ApplicationScope
		err = decoded.UnmarshalJSON(data)
		require.NoError(t, err)

		assert.Equal(t, original.ReadScope, decoded.ReadScope)
		assert.Equal(t, original.WriteScope, decoded.WriteScope)
		assert.Equal(t, original.AdminScope, decoded.AdminScope)
		assert.Equal(t, original.OtherScope, decoded.OtherScope)
	})

	t.Run("zero values", func(t *testing.T) {
		original := ApplicationScope{}

		data, err := original.MarshalJSON()
		require.NoError(t, err)

		var decoded ApplicationScope
		err = decoded.UnmarshalJSON(data)
		require.NoError(t, err)

		assert.Equal(t, uint64(0), decoded.ReadScope)
		assert.Equal(t, uint64(0), decoded.WriteScope)
	})

	t.Run("unmarshal invalid hex", func(t *testing.T) {
		var scope ApplicationScope
		err := scope.UnmarshalJSON([]byte("not-valid-hex"))
		assert.Error(t, err)
	})

	t.Run("unmarshal too short data", func(t *testing.T) {
		var scope ApplicationScope
		err := scope.UnmarshalJSON([]byte("0102030405060708"))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid scope data length")
	})
}

func TestApplicationScope_FullReadScope(t *testing.T) {
	scope := ApplicationScope{ReadScope: AppScopeRead}
	// AppScopeRead is 0xFFFFFFFFFFFFFFFF, should match all read scopes
	assert.True(t, scope.HasScope(AppScopeTypeRead, AppScopeReadAccounts))
	assert.True(t, scope.HasScope(AppScopeTypeRead, AppScopeReadStatuses))
	assert.True(t, scope.HasScope(AppScopeTypeRead, AppScopeReadNotifications))
}
