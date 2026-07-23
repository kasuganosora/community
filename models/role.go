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

type Role struct { //nolint:recvcheck
	ID          int64  `gorm:"primary_key;autoIncrement"        json:"id"`
	Name        string `gorm:"unique;not null;type:varchar(20)" json:"name"`
	Color       string `gorm:"type:varchar(20)"                 json:"color"`
	Highlighted bool   `gorm:"default:false"                    json:"highlighted"`
	Permissions uint64 `gorm:"type:bigint"                      json:"-"`
}

func (Role) TableName() string {
	return "roles"
}

func (r *Role) HasPermission(permission uint64) bool {
	return r.Permissions&permission == permission
}

func (r *Role) SetPermission(permission uint64) *Role {
	r.Permissions |= permission

	return r
}

type RoleUser struct {
	RoleID    uint64     `gorm:"primary_key"    json:"role_id"`
	UserID    uint64     `gorm:"primary_key"    json:"user_id"`
	CreatedAt *time.Time `gorm:"autoCreateTime" json:"created_at"`
}

func (RoleUser) TableName() string {
	return "role_users"
}

func AssignRole(db *gorm.DB, userID uint64, roleID uint64) (err error) {
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

func GetUserRoles(db *gorm.DB, userID uint64) (roles []*Role, err error) {
	roles = make([]*Role, 0)
	userRoles := make([]*RoleUser, 0)

	err = db.Model(&RoleUser{}).Where("user_id = ?", userID).Find(&userRoles).Error

	if err != nil || len(userRoles) == 0 {
		return
	}

	roleIDs := make([]uint64, 0)
	for _, userRole := range userRoles {
		roleIDs = append(roleIDs, userRole.RoleID)
	}

	err = db.Model(&Role{}).Where("id IN ?", roleIDs).Find(&roles).Error

	return roles, err
}

func GetUserPermissions(db *gorm.DB, userID uint64) (permissions uint64, err error) {
	roles, err := GetUserRoles(db, userID)
	if err != nil {
		return
	}

	for _, role := range roles {
		permissions |= role.Permissions
	}

	return
}
