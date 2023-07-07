package models

import (
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"time"
)

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

func (r *Role) SetPermission(permission int64) *Role {
	r.Permissions |= permission
	return r
}

type RoleUser struct {
	RoleID    int64      `json:"role_id" gorm:"primary_key"`
	UserID    int64      `json:"user_id" gorm:"primary_key"`
	CreatedAt *time.Time `json:"created_at" gorm:"autoCreateTime"`
}

func (RoleUser) TableName() string {
	return "role_users"
}

func AssignRole(db *gorm.DB, userID int64, roleID int64) (err error) {
	r := &RoleUser{
		RoleID: roleID,
		UserID: userID,
	}

	// check if role exists
	if err = db.First(&Role{}, "id = ?", roleID).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			err = errors.Wrap(err, "failed to check if role exists")
			return
		}
	}

	err = db.Create(r).Error
	return
}

func GetUserRoles(db *gorm.DB, userID int64) ([]*Role, error) {
	roles := make([]*Role, 0)
	err := db.Model(&RoleUser{}).Where("user_id = ?", userID).Find(&roles).Error
	return roles, err
}

func GetUserPermissions(db *gorm.DB, userID int64) (permissions int64, err error) {
	roles, err := GetUserRoles(db, userID)
	if err != nil {
		return
	}

	for _, role := range roles {
		permissions |= role.Permissions
	}

	return
}
