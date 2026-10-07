package models

import "gorm.io/gorm"

// Only implemented capabilities can be delegated. Administrator is a reserved
// legacy role bit: this API cannot create or change that bit or User.IsAdmin.
const (
	PermissionManageMedia    uint64 = 0x100000
	PermissionManageBranding uint64 = 0x200000
)

var AdminCapabilityBits = map[string]uint64{
	"audit": PermissionViewAuditLog, "webhooks": PermissionManageWebhooks, "media": PermissionManageMedia, "branding": PermissionManageBranding,
	"dashboard": PermissionViewDashboard, "users": PermissionManageUsers,
	"reports": PermissionManageReports, "federation": PermissionManageFederation,
	"settings": PermissionManageSettings, "blocks": PermissionManageBlocks,
	"rules": PermissionManageRules, "announcements": PermissionManageAnnouncements,
	"emojis": PermissionManageCustomEmojis, "invites": PermissionManageInvites,
	"roles": PermissionManageRoles, "system": PermissionDevops,
}
var AdminCapabilityNames = []string{"audit", "webhooks", "media", "branding", "dashboard", "users", "reports", "federation", "settings", "blocks", "rules", "announcements", "emojis", "invites", "roles", "system"}

func SupportedAdminPermissions() uint64 {
	var bits uint64
	for _, bit := range AdminCapabilityBits {
		bits |= bit
	}
	return bits
}
func EffectiveAdminPermissions(conn *gorm.DB, user *User) (uint64, error) {
	if user == nil || user.IsBan || user.Status != UserStatusNormal {
		return 0, nil
	}
	if user.IsAdmin {
		return SupportedAdminPermissions(), nil
	}
	bits, err := GetUserPermissions(conn, user.Id)
	if err != nil {
		return 0, err
	}
	if bits&PermissionAdministrator != 0 {
		return SupportedAdminPermissions(), nil
	}
	return bits & SupportedAdminPermissions(), nil
}
func AdminCapabilities(bits uint64) []string {
	out := []string{}
	for _, name := range AdminCapabilityNames {
		if bits&AdminCapabilityBits[name] != 0 {
			out = append(out, name)
		}
	}
	return out
}
