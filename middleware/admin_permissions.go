package middleware

import (
	"github.com/kasuganosora/community/models"
	"strings"
)

// Segment-based allowlist, never a broad admin:read=>all administration grant.
// New routes remain root-only until their concrete capability is registered.
func adminRoutePermission(path string) uint64 {
	var rest string
	switch {
	case strings.HasPrefix(path, "/api/v1/admin/"):
		rest = strings.TrimPrefix(path, "/api/v1/admin/")
	case strings.HasPrefix(path, "/api/v2/admin/"):
		rest = strings.TrimPrefix(path, "/api/v2/admin/")
	default:
		return 0
	}
	if rest == "federation/jobs" || strings.HasPrefix(rest, "federation/jobs/") {
		return models.PermissionDevops
	}
	resource := strings.SplitN(rest, "/", 2)[0]
	switch resource {
	case "audit-logs":
		return models.PermissionViewAuditLog
	case "webhooks", "webhook-deliveries":
		return models.PermissionManageWebhooks
	case "files":
		return models.PermissionManageMedia
	case "avatar_decorations", "advertisements", "decorations", "ads":
		return models.PermissionManageBranding
	case "analytics", "dashboard", "dimensions", "measures", "retention":
		return models.PermissionViewDashboard
	case "accounts":
		return models.PermissionManageUsers
	case "reports", "statuses":
		return models.PermissionManageReports
	case "relays", "instances", "federation-jobs", "federation", "domain_blocks", "domain_allows":
		return models.PermissionManageFederation
	case "settings":
		section := strings.Split(rest, "/")
		if len(section) > 1 {
			if section[1] == "database" {
				return models.PermissionDevops
			}
			if section[1] == "branding" {
				return models.PermissionManageBranding
			}
		}
		return models.PermissionManageSettings
	case "captcha":
		return models.PermissionManageSettings
	case "ip_blocks", "email_domain_blocks", "canonical_email_blocks":
		return models.PermissionManageBlocks
	case "rule", "rules":
		return models.PermissionManageRules
	case "announcements":
		return models.PermissionManageAnnouncements
	case "custom_emojis", "emoji_roles":
		return models.PermissionManageCustomEmojis
	case "roles":
		return models.PermissionManageRoles
	case "invites":
		return models.PermissionManageInvites
	case "matrix", "thumbnails", "jobs", "queues", "system":
		return models.PermissionDevops
	default:
		return 0
	}
}
