package models

const (
	PermissionAdministrator       = 0x1
	PermissionDevops              = 0x2
	PermissionViewAuditLog        = 0x4
	PermissionViewDashboard       = 0x8
	PermissionManageReports       = 0x10
	PermissionManageFederation    = 0x20
	PermissionManageSettings      = 0x40
	PermissionManageBlocks        = 0x80
	PermissionManageTaxonomies    = 0x100
	PermissionManageAppeals       = 0x200
	PermissionManageUsers         = 0x400
	PermissionManageInvites       = 0x800
	PermissionManageRules         = 0x1000
	PermissionManageAnnouncements = 0x2000
	PermissionManageCustomEmojis  = 0x4000
	PermissionManageWebhooks      = 0x8000
	PermissionInviteUsers         = 0x10000
	PermissionManageRoles         = 0x20000
	PermissionManageUserAccess    = 0x40000
	PermissionDeleteUserData      = 0x80000
)

type Role struct {
	ID          int64  `json:"id" gorm:"primary_key;autoIncrement"`
	Name        string `json:"name" gorm:"unique;not null;type:varchar(20)"`
	Color       string `json:"color" gorm:"type:varchar(20)"`
	Highlighted bool   `json:"highlighted" gorm:"default:false"`
	Permissions int64  `json:"-" gorm:"type:bigint"`
}

func (Role) TableName() string {
	return "roles"
}

func (r *Role) HasPermission(permission int64) bool {
	return r.Permissions&permission == permission
}
