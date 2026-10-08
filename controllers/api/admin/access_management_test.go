package admin

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/invitecode"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
)

func accessFixture(t *testing.T) (*adminTestEnv, *models.User, *models.Account, *models.Account) {
	env := newAdminAccountTestEnv(t)
	require.NoError(t, env.gdb.AutoMigrate(&models.Role{}, &models.RoleUser{}, &models.InviteCode{}))
	// InviteCode's historical explicit bigint primary key needs INTEGER for
	// real SQLite auto IDs; production uses MySQL's auto_increment unchanged.
	if !pgtest.Enabled() {
		require.NoError(t, env.gdb.Exec("DROP TABLE invite_codes").Error)
		require.NoError(t, env.gdb.Exec(`CREATE TABLE invite_codes (id INTEGER PRIMARY KEY AUTOINCREMENT,code TEXT NOT NULL UNIQUE,user_id INTEGER NOT NULL,account_id TEXT NOT NULL,max_use_count INTEGER,use_count INTEGER,expire_at DATETIME,after_register_action TEXT,after_register_action_param TEXT,disabled BOOLEAN DEFAULT false,created_at DATETIME,updated_at DATETIME,deleted_at DATETIME)`).Error)
	}
	root := createLocalAccount(t, env, "access_root")
	target := createLocalAccount(t, env, "access_target")
	require.NoError(t, env.gdb.Model(&models.User{}).Where("id = ?", root.UserId).UpdateColumn("is_admin", true).Error)
	user := &models.User{}
	require.NoError(t, env.gdb.First(user, root.UserId).Error)
	return env, user, root, target
}
func TestRoleDelegationIsImmediateBoundedAndNotSelfEscalating(t *testing.T) {
	env, root, _, target := accessFixture(t)
	c, w := newJSONContext(t, http.MethodPost, nil, adminRoleBody{Name: "delegated", Permissions: []string{"roles", "dashboard"}})
	c.Set("user", root)
	CreateRole(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	var role map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &role))
	id := role["id"].(string)
	params := gin.Params{{Key: "id", Value: id}, {Key: "account_id", Value: uitoa(target.Id)}}
	c, w = newJSONContext(t, http.MethodPost, params, nil)
	c.Set("user", root)
	AssignAdminRole(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	delegated := &models.User{}
	require.NoError(t, env.gdb.First(delegated, target.UserId).Error)
	permissions, err := models.EffectiveAdminPermissions(env.gdb, delegated)
	require.NoError(t, err)
	require.EqualValues(t, models.PermissionManageRoles|models.PermissionViewDashboard, permissions)
	c, w = newJSONContext(t, http.MethodPut, gin.Params{{Key: "id", Value: id}}, adminRoleBody{Name: "delegated", Permissions: []string{"roles", "webhooks"}})
	c.Set("user", delegated)
	UpdateRole(c)
	require.Equal(t, 422, w.Code)
	c, w = newJSONContext(t, http.MethodPost, params, nil)
	c.Set("user", delegated)
	AssignAdminRole(c)
	require.Equal(t, 422, w.Code)
	c, w = newJSONContext(t, http.MethodPost, nil, adminRoleBody{Name: "owner2", Permissions: []string{"administrator"}})
	c.Set("user", root)
	CreateRole(c)
	require.Equal(t, 422, w.Code)
	c, w = newJSONContext(t, http.MethodDelete, params, nil)
	c.Set("user", root)
	UnassignAdminRole(c)
	require.Equal(t, 200, w.Code)
	permissions, err = models.EffectiveAdminPermissions(env.gdb, delegated)
	require.NoError(t, err)
	require.Zero(t, permissions, "revocation is not cached across requests")
	c, w = newJSONContext(t, http.MethodPost, nil, adminRoleBody{Name: "forbidden", Permissions: []string{"dashboard"}})
	c.Set("user", delegated)
	CreateRole(c)
	require.Equal(t, 403, w.Code)
}
func TestLastRootAndReservedRolesAreProtected(t *testing.T) {
	env, root, rootAccount, target := accessFixture(t)
	owner := models.Role{Name: "reserved", Permissions: models.PermissionAdministrator}
	require.NoError(t, env.gdb.Create(&owner).Error)
	c, w := newJSONContext(t, http.MethodDelete, gin.Params{{Key: "id", Value: uitoa(uint64(owner.ID))}}, nil)
	c.Set("user", root)
	DeleteRole(c)
	require.Equal(t, 422, w.Code)
	c, w = newJSONContext(t, http.MethodPost, gin.Params{{Key: "id", Value: uitoa(rootAccount.Id)}}, AccountActionRequest{Type: "disable"})
	c.Set("user", root)
	PerformAccountAction(c)
	require.Equal(t, 422, w.Code)
	require.Contains(t, w.Body.String(), "last active root")
	delegated := &models.User{}
	require.NoError(t, env.gdb.First(delegated, target.UserId).Error)
	c, w = newJSONContext(t, http.MethodPost, gin.Params{{Key: "id", Value: uitoa(rootAccount.Id)}}, AccountActionRequest{Type: "suspend"})
	c.Set("user", delegated)
	PerformAccountAction(c)
	require.Equal(t, 403, w.Code)
}
func TestAdminInvitesUseExistingAtomicRegistrationLimits(t *testing.T) {
	env, root, _, target := accessFixture(t)
	c, w := newJSONContext(t, http.MethodPost, nil, gin.H{"count": 2, "max_uses": 1, "expires_in": 3600})
	c.Set("user", root)
	CreateAdminInvites(c)
	require.Equal(t, 201, w.Code, w.Body.String())
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	var invites []models.InviteCode
	require.NoError(t, env.gdb.Order("id ASC").Find(&invites).Error)
	require.Len(t, invites, 2)
	require.NotEqual(t, invites[0].Code, invites[1].Code)
	require.NotNil(t, invites[0].ExpireAt)
	_, err := invitecode.ConsumeInviteCode(env.gdb, invites[0].Code)
	require.NoError(t, err)
	_, err = invitecode.ConsumeInviteCode(env.gdb, invites[0].Code)
	require.Error(t, err)
	c, w = newJSONContext(t, http.MethodPut, gin.Params{{Key: "id", Value: uitoa(uint64(invites[1].Id))}}, gin.H{"disabled": true})
	c.Set("user", root)
	UpdateAdminInvite(c)
	require.Equal(t, 200, w.Code)
	_, err = invitecode.ConsumeInviteCode(env.gdb, invites[1].Code)
	require.Error(t, err)
	require.NoError(t, env.gdb.Model(&invites[1]).Updates(map[string]any{"disabled": false, "expire_at": time.Now().Add(-time.Minute)}).Error)
	_, err = invitecode.ConsumeInviteCode(env.gdb, invites[1].Code)
	require.Error(t, err)
	ordinary := &models.User{}
	require.NoError(t, env.gdb.First(ordinary, target.UserId).Error)
	c, w = newGetContext(t, nil, "")
	c.Set("user", ordinary)
	ListAdminInvites(c)
	require.Equal(t, 403, w.Code)
	require.NotContains(t, w.Body.String(), invites[0].Code)
	c, w = newJSONContext(t, http.MethodPost, nil, gin.H{"count": 101, "max_uses": 1, "expires_in": 3600})
	c.Set("user", root)
	CreateAdminInvites(c)
	require.Equal(t, 422, w.Code)
	var count int64
	require.NoError(t, env.gdb.Model(&models.InviteCode{}).Count(&count).Error)
	require.EqualValues(t, 2, count)
}
