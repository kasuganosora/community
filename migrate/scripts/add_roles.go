package scripts

import (
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

type AddRoles struct {
}

func (AddRoles) Up(db *gorm.DB) (err error) {
	roles := make([]*models.Role, 0)

	ownerRole := &models.Role{
		Name: "owner",
	}

	ownerRole.SetPermission(models.PermissionAdministrator)
	roles = append(roles, ownerRole)

	adminRole := &models.Role{
		Name: "admin",
	}

	adminRole.SetPermission(models.PermissionViewDashboard).
		SetPermission(models.PermissionViewAuditLog).
		SetPermission(models.PermissionManageUsers).
		SetPermission(models.PermissionManageUserAccess).
		SetPermission(models.PermissionDeleteUserData).
		SetPermission(models.PermissionManageReports).
		SetPermission(models.PermissionManageTaxonomies).
		SetPermission(models.PermissionManageFederation).
		SetPermission(models.PermissionManageSettings).
		SetPermission(models.PermissionManageBlocks).
		SetPermission(models.PermissionManageAppeals).
		SetPermission(models.PermissionManageRules).
		SetPermission(models.PermissionManageInvites).
		SetPermission(models.PermissionManageAnnouncements).
		SetPermission(models.PermissionManageCustomEmojis).
		SetPermission(models.PermissionManageWebhooks).
		SetPermission(models.PermissionManageRoles)

	roles = append(roles, adminRole)

	moderatorRole := &models.Role{
		Name: "moderator",
	}

	moderatorRole.SetPermission(models.PermissionViewDashboard).
		SetPermission(models.PermissionViewAuditLog).
		SetPermission(models.PermissionManageUsers).
		SetPermission(models.PermissionManageReports).
		SetPermission(models.PermissionManageTaxonomies)

	roles = append(roles, moderatorRole)

	return db.Create(roles).Error

}

func (AddRoles) Down(db *gorm.DB) (err error) {
	return db.Delete(&models.Role{}, "name in ?", []string{"owner", "admin", "moderator"}).Error
}
