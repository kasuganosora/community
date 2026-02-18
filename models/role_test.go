package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRole_HasPermission(t *testing.T) {
	role := &Role{
		ID:          1,
		Name:        "Admin",
		Permissions: PermissionAdministrator | PermissionManageReports | PermissionManageUsers,
	}

	tests := []struct {
		name       string
		permission uint64
		expected   bool
	}{
		{"Has admin", PermissionAdministrator, true},
		{"Has manage reports", PermissionManageReports, true},
		{"Has manage users", PermissionManageUsers, true},
		{"Does not have devops", PermissionDevops, false},
		{"Does not have view audit log", PermissionViewAuditLog, false},
		{"Has combined admin+reports", PermissionAdministrator | PermissionManageReports, true},
		{"Does not have admin+devops", PermissionAdministrator | PermissionDevops, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := role.HasPermission(tt.permission)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRole_SetPermission(t *testing.T) {
	role := &Role{
		ID:          1,
		Name:        "Test",
		Permissions: 0,
	}

	// Add permission
	role.SetPermission(PermissionAdministrator)
	assert.True(t, role.HasPermission(PermissionAdministrator))
	assert.Equal(t, uint64(PermissionAdministrator), role.Permissions)

	// Add another permission
	role.SetPermission(PermissionDevops)
	assert.True(t, role.HasPermission(PermissionAdministrator))
	assert.True(t, role.HasPermission(PermissionDevops))
	assert.Equal(t, uint64(PermissionAdministrator|PermissionDevops), role.Permissions)

	// Chain set
	role.SetPermission(PermissionViewAuditLog).SetPermission(PermissionViewDashboard)
	assert.True(t, role.HasPermission(PermissionViewAuditLog))
	assert.True(t, role.HasPermission(PermissionViewDashboard))
}

func TestRole_TableName(t *testing.T) {
	role := Role{}
	assert.Equal(t, "roles", role.TableName())
}

func TestRoleUser_TableName(t *testing.T) {
	roleUser := RoleUser{}
	assert.Equal(t, "role_users", roleUser.TableName())
}

func TestPermissionConstants(t *testing.T) {
	// Verify permission constants have distinct values
	permissions := []uint64{
		PermissionAdministrator,
		PermissionDevops,
		PermissionViewAuditLog,
		PermissionViewDashboard,
		PermissionManageReports,
		PermissionManageFederation,
		PermissionManageSettings,
		PermissionManageBlocks,
		PermissionManageTaxonomies,
		PermissionManageAppeals,
		PermissionManageUsers,
		PermissionManageInvites,
		PermissionManageRules,
		PermissionManageAnnouncements,
		PermissionManageCustomEmojis,
		PermissionManageWebhooks,
		PermissionInviteUsers,
		PermissionManageRoles,
		PermissionManageUserAccess,
		PermissionDeleteUserData,
	}

	// Check that each permission is a power of 2 (single bit)
	for _, p := range permissions {
		assert.True(t, p != 0 && (p&(p-1)) == 0, "Permission %d should be a power of 2", p)
	}

	// Check that all permissions are unique
	seen := make(map[uint64]bool)
	for _, p := range permissions {
		assert.False(t, seen[p], "Permission %d is duplicated", p)
		seen[p] = true
	}
}

// Note: Tests that require sqlexec database mock (AssignRole, GetUserRoles, GetUserPermissions)
// are temporarily disabled due to sqlexec compilation issues.
// These tests should be moved to a separate file with build tags or re-enabled when sqlexec is fixed.
