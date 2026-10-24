package admin

import (
	"encoding/json"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/tests/pgtest"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/mediaproxy"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============== escapeLike ==============

func TestEscapeLike(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"plain", "alice", "alice"},
		{"percent", "100%", "100\\%"},
		{"underscore", "a_b", "a\\_b"},
		{"backslash", `a\b`, `a\\b`},
		{"all special", `%_` + `\`, `\%\_\\`}, //nolint:dupword
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, escapeLike(tc.in))
		})
	}
}

// ============== serializeAdminAccount ==============

func TestSerializeAdminAccount_Local(t *testing.T) {
	now := parseTimeRFC3339(t, "2024-01-02T03:04:05Z")

	account := &models.Account{
		Id:          42,
		Username:    "alice",
		DisplayName: "Alice",
		Locked:      false,
		Bot:         true,
		Note:        "hi",
		CreatedAt:   &now,
		IsBan:       false,
	}
	account.Domain = nil

	result := serializeAdminAccount(account)

	assert.Equal(t, "42", result["id"])
	assert.Equal(t, "alice", result["username"])
	assert.Nil(t, result["domain"]) // local account => domain nil in JSON
	assert.Equal(t, "Alice", result["display_name"])
	assert.False(t, result["suspended"].(bool))
	assert.False(t, result["silenced"].(bool))
	assert.Equal(t, now.Format("2024-01-02T03:04:05Z"), result["created_at"])
}

func TestSerializeAdminAccount_RemoteWithUser(t *testing.T) {
	domain := "remote.social"
	user := &models.User{Id: 1, EmailHash: "abc", IsAdmin: true, Status: models.UserStatusNormal}
	account := &models.Account{
		Id:          7,
		Username:    "bob",
		DisplayName: "Bob",
		Locked:      true,
		IsBan:       true,
	}
	account.Domain = &domain
	account.User = user

	result := serializeAdminAccount(account)

	assert.Equal(t, "remote.social", result["domain"])
	assert.True(t, result["suspended"].(bool))
	// silenced = Locked && !IsBan => true && false => false
	assert.False(t, result["silenced"].(bool))
	assert.Equal(t, "admin", result["role"])
	assert.Equal(t, "abc", result["email_hash"])
	assert.Equal(t, models.UserStatusNormal, result["user_status"])
}

func TestSerializeAdminAccount_Silenced(t *testing.T) {
	account := &models.Account{
		Id:     1,
		Locked: true,
		IsBan:  false,
	}
	account.Domain = nil

	result := serializeAdminAccount(account)
	assert.False(t, result["silenced"].(bool), "locked profiles are not moderation-silenced")
	assert.False(t, result["suspended"].(bool))
}

func TestSerializeAdminAccount_NilTimes(t *testing.T) {
	account := &models.Account{Id: 1}
	account.Domain = nil

	result := serializeAdminAccount(account)
	assert.Nil(t, result["created_at"])
	assert.Nil(t, result["last_status_at"])
	assert.Nil(t, result["last_activity_at"])
	assert.Nil(t, result["last_sign_in_at"])
	assert.Nil(t, result["current_sign_in_at"])
	assert.Nil(t, result["last_sign_in_ip"])
	assert.Nil(t, result["current_sign_in_ip"])
}

func TestSerializeAdminAccount_SignIn(t *testing.T) {
	posted := parseTimeRFC3339(t, "2026-10-01T13:00:45Z")
	current := parseTimeRFC3339(t, "2026-10-02T01:02:03Z")
	previous := parseTimeRFC3339(t, "2026-09-01T00:00:00Z")
	account := &models.Account{
		Id:           1605,
		Username:     "ler",
		DisplayName:  "ler",
		LastStatusAt: &posted,
		User: &models.User{
			CurrentSignInAt: &current,
			LastSignInAt:    &previous,
			CurrentSignInIP: "203.0.113.10",
			LastSignInIP:    "198.51.100.4",
		},
	}

	result := serializeAdminAccount(account)
	assert.Equal(t, "2026-10-01T13:00:45Z", result["last_status_at"])
	assert.Equal(t, "2026-10-02T01:02:03Z", result["current_sign_in_at"])
	assert.Equal(t, "2026-09-01T00:00:00Z", result["last_sign_in_at"])
	assert.Equal(t, "203.0.113.10", result["current_sign_in_ip"])
	assert.Equal(t, "198.51.100.4", result["last_sign_in_ip"])
}

func TestSerializeAdminAccount_AvatarAndDecorations(t *testing.T) {
	local := &models.Account{
		Id:                 8,
		Username:           "alice",
		DisplayName:        "Alice",
		Avatar:             "/media/alice.png",
		AvatarStatic:       "/media/alice.png",
		AvatarDecorationID: 3,
		AvatarDecorations:  models.AvatarDecorationSelections{{ID: "3"}},
	}
	localResult := serializeAdminAccount(local)
	assert.Equal(t, "/media/alice.png", localResult["avatar"])
	nestedLocal, ok := localResult["account"].(*serializer.Account)
	require.True(t, ok)
	require.NotNil(t, nestedLocal)
	require.NotEmpty(t, nestedLocal.AvatarDecorations)
	assert.Equal(t, "/api/v1/avatar_decorations/3/image", nestedLocal.AvatarDecorations[0].URL)
	deco, ok := localResult["avatar_decorations"].([]serializer.AvatarDecoration)
	require.True(t, ok)
	require.NotEmpty(t, deco)
	assert.Equal(t, "/api/v1/avatar_decorations/3/image", deco[0].URL)

	domain := "remote.example"
	remote := &models.Account{
		Id:                9,
		Username:          "carol",
		DisplayName:       "Carol",
		Avatar:            "https://remote.example/files/carol.png",
		AvatarStatic:      "https://remote.example/files/carol.png",
		AvatarDecorations: models.AvatarDecorationSelections{{ID: "9xyz", URL: "https://remote.example/files/deco.png"}},
	}
	remote.Domain = &domain
	remoteResult := serializeAdminAccount(remote)
	nestedRemote, ok := remoteResult["account"].(*serializer.Account)
	require.True(t, ok)
	require.NotNil(t, nestedRemote)
	assert.Equal(t, nestedRemote.Avatar, remoteResult["avatar"])
	if mediaproxy.Enabled() {
		assert.Contains(t, remoteResult["avatar"].(string), mediaproxy.PathPrefix)
		require.NotEmpty(t, nestedRemote.AvatarDecorations)
		assert.Contains(t, nestedRemote.AvatarDecorations[0].URL, mediaproxy.PathPrefix)
	}
}

// ============== GetAccount ==============

func TestGetAccount_Success(t *testing.T) {
	env := newAdminAccountTestEnv(t)
	account := createLocalAccount(t, env, "getme")

	c, w := newGetContext(t, gin.Params{{Key: "id", Value: uitoa(account.Id)}}, "")
	GetAccount(c)

	assert.Equal(t, 200, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, uitoa(account.Id), resp["id"])
}

func TestGetAccount_InvalidID(t *testing.T) {
	newAdminAccountTestEnv(t)
	c, w := newGetContext(t, gin.Params{{Key: "id", Value: "abc"}}, "")
	GetAccount(c)
	assert.Equal(t, 400, w.Code)
}

func TestGetAccount_ZeroID(t *testing.T) {
	newAdminAccountTestEnv(t)
	c, w := newGetContext(t, gin.Params{{Key: "id", Value: "0"}}, "")
	GetAccount(c)
	assert.Equal(t, 400, w.Code)
}

func TestGetAccount_NotFound(t *testing.T) {
	newAdminAccountTestEnv(t)
	c, w := newGetContext(t, gin.Params{{Key: "id", Value: "999999"}}, "")
	GetAccount(c)
	assert.Equal(t, 404, w.Code)
}

// ============== ListAccounts ==============

func TestListAccounts_Empty(t *testing.T) {
	newAdminAccountTestEnv(t)
	c, w := newGetContext(t, nil, "")
	ListAccounts(c)

	assert.Equal(t, 200, w.Code)
	var resp []any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp)
}

func TestListAccounts_WithResults(t *testing.T) {
	env := newAdminAccountTestEnv(t)
	createLocalAccount(t, env, "alpha")
	createLocalAccount(t, env, "beta")

	c, w := newGetContext(t, nil, "")
	ListAccounts(c)

	assert.Equal(t, 200, w.Code)
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp, 2)
}

func TestListAccounts_LimitOffset(t *testing.T) {
	env := newAdminAccountTestEnv(t)
	for _, n := range []string{"u1", "u2", "u3"} {
		createLocalAccount(t, env, n)
	}

	c, w := newGetContext(t, nil, "limit=2&offset=0")
	ListAccounts(c)

	assert.Equal(t, 200, w.Code)
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp, 2)
}

func TestListAccounts_InvalidLimitIgnored(t *testing.T) {
	env := newAdminAccountTestEnv(t)
	createLocalAccount(t, env, "onlyone")

	// limit=0 and limit=999 are invalid => defaults to 40
	c, w := newGetContext(t, nil, "limit=0")
	ListAccounts(c)
	assert.Equal(t, 200, w.Code)

	c2, w2 := newGetContext(t, nil, "limit=999")
	ListAccounts(c2)
	assert.Equal(t, 200, w.Code)
	_ = w2
}

func TestListAccounts_FilterByUsername(t *testing.T) {
	env := newAdminAccountTestEnv(t)
	createLocalAccount(t, env, "alice")
	createLocalAccount(t, env, "bob")

	c, w := newGetContext(t, nil, "username=alice")
	ListAccounts(c)

	assert.Equal(t, 200, w.Code)
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp, 1)
	assert.Equal(t, "alice", resp[0]["username"])
}

func TestListAccounts_FilterByDisplayName(t *testing.T) {
	env := newAdminAccountTestEnv(t)
	createLocalAccount(t, env, "alice") // DisplayName defaults to "alice"

	c, w := newGetContext(t, nil, "display_name=alice")
	ListAccounts(c)

	assert.Equal(t, 200, w.Code)
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp)
}

func TestListAccounts_FilterByStatusActive(t *testing.T) {
	env := newAdminAccountTestEnv(t)
	createLocalAccount(t, env, "active")

	c, w := newGetContext(t, nil, "status=active")
	ListAccounts(c)

	assert.Equal(t, 200, w.Code)
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp)
}

func TestListAccounts_FilterByStatusSuspended(t *testing.T) {
	env := newAdminAccountTestEnv(t)
	createLocalAccount(t, env, "suspended")
	createLocalAccount(t, env, "active")

	// Note: the "suspended" status filter queries a `suspended` column that
	// does not exist on the accounts table (the model uses IsBan). The query
	// is therefore a no-op predicate in this DB, but the handler must still
	// return 200 without crashing.
	c, w := newGetContext(t, nil, "status=suspended")
	ListAccounts(c)

	assert.Equal(t, 200, w.Code)
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	// No account is actually banned (IsBan=false), so none should match.
	assert.Empty(t, resp, "only actual banned accounts match suspended")
}

func TestListAccounts_FilterByOriginLocal(t *testing.T) {
	env := newAdminAccountTestEnv(t)
	createLocalAccount(t, env, "local")

	c, w := newGetContext(t, nil, "origin=local")
	ListAccounts(c)

	assert.Equal(t, 200, w.Code)
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp)
}

func TestListAccounts_FilterByOriginRemote(t *testing.T) {
	env := newAdminAccountTestEnv(t)
	createLocalAccount(t, env, "local")

	c, w := newGetContext(t, nil, "origin=remote")
	ListAccounts(c)

	assert.Equal(t, 200, w.Code)
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp)
}

// ============== PerformAccountAction ==============

func TestPerformAccountAction_Suspend(t *testing.T) {
	env := newAdminAccountTestEnv(t)
	account := createLocalAccount(t, env, "victim")

	c, w := newJSONContext(t, http.MethodPost, gin.Params{{Key: "id", Value: uitoa(account.Id)}},
		AccountActionRequest{Type: "suspend"})

	PerformAccountAction(c)

	require.Equal(t, 200, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp["suspended"].(bool))
}

func TestPerformAccountAction_Unsuspend(t *testing.T) {
	env := newAdminAccountTestEnv(t)
	account := createLocalAccount(t, env, "unban")
	account.IsBan = true
	account.Locked = true
	require.NoError(t, env.gdb.Save(account).Error)

	c, w := newJSONContext(t, http.MethodPost, gin.Params{{Key: "id", Value: uitoa(account.Id)}},
		AccountActionRequest{Type: "unsuspend"})

	PerformAccountAction(c)

	require.Equal(t, 200, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp["suspended"].(bool))
}

func TestPerformAccountAction_Silence(t *testing.T) {
	env := newAdminAccountTestEnv(t)
	account := createLocalAccount(t, env, "tobesilenced")

	c, w := newJSONContext(t, http.MethodPost, gin.Params{{Key: "id", Value: uitoa(account.Id)}},
		AccountActionRequest{Type: "silence"})

	PerformAccountAction(c)

	assert.Equal(t, 422, w.Code)
}

func TestPerformAccountAction_Unsilence(t *testing.T) {
	env := newAdminAccountTestEnv(t)
	account := createLocalAccount(t, env, "tobesilenced")
	account.Locked = true
	require.NoError(t, env.gdb.Save(account).Error)

	c, w := newJSONContext(t, http.MethodPost, gin.Params{{Key: "id", Value: uitoa(account.Id)}},
		AccountActionRequest{Type: "unsilence"})

	PerformAccountAction(c)

	assert.Equal(t, 422, w.Code)
}

func TestPerformAccountAction_Enable(t *testing.T) {
	env := newAdminAccountTestEnv(t)
	account := createLocalAccount(t, env, "enableme")
	account.IsBan = true
	account.Locked = true
	require.NoError(t, env.gdb.Save(account).Error)

	c, w := newJSONContext(t, http.MethodPost, gin.Params{{Key: "id", Value: uitoa(account.Id)}},
		AccountActionRequest{Type: "enable"})

	PerformAccountAction(c)

	require.Equal(t, 200, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp["suspended"].(bool), "enabling login does not lift suspension")
}

func TestPerformAccountAction_SensitiveNoop(t *testing.T) {
	env := newAdminAccountTestEnv(t)
	account := createLocalAccount(t, env, "sensitive")

	c, w := newJSONContext(t, http.MethodPost, gin.Params{{Key: "id", Value: uitoa(account.Id)}},
		AccountActionRequest{Type: "sensitive"})

	PerformAccountAction(c)
	assert.Equal(t, 422, w.Code)
}

func TestPerformAccountAction_InvalidID(t *testing.T) {
	newAdminAccountTestEnv(t)
	c, w := newJSONContext(t, http.MethodPost, gin.Params{{Key: "id", Value: "bad"}},
		AccountActionRequest{Type: "enable"})

	PerformAccountAction(c)
	assert.Equal(t, 400, w.Code)
}

func TestPerformAccountAction_InvalidBody(t *testing.T) {
	env := newAdminAccountTestEnv(t)
	account := createLocalAccount(t, env, "x")

	// Send invalid JSON
	c := ginContextWithBody(t, http.MethodPost, gin.Params{{Key: "id", Value: uitoa(account.Id)}}, "{bad json")
	PerformAccountAction(c)
	assert.Equal(t, 400, c.Writer.Status())
}

func TestPerformAccountAction_NotFound(t *testing.T) {
	newAdminAccountTestEnv(t)
	c, w := newJSONContext(t, http.MethodPost, gin.Params{{Key: "id", Value: "999999"}},
		AccountActionRequest{Type: "enable"})

	PerformAccountAction(c)
	assert.Equal(t, 404, w.Code)
}

// ============== EnableAccount ==============

func TestEnableAccount_Success(t *testing.T) {
	env := newAdminAccountTestEnv(t)
	account := createLocalAccount(t, env, "enable2")
	account.IsBan = true
	account.Locked = true
	require.NoError(t, env.gdb.Save(account).Error)

	c, w := newGetContext(t, gin.Params{{Key: "id", Value: uitoa(account.Id)}}, "")
	EnableAccount(c)

	require.Equal(t, 200, w.Code)
}

func TestEnableAccount_InvalidID(t *testing.T) {
	newAdminAccountTestEnv(t)
	c, w := newGetContext(t, gin.Params{{Key: "id", Value: "abc"}}, "")
	EnableAccount(c)
	assert.Equal(t, 400, w.Code)
}

func TestEnableAccount_NotFound(t *testing.T) {
	newAdminAccountTestEnv(t)
	c, w := newGetContext(t, gin.Params{{Key: "id", Value: "888888"}}, "")
	EnableAccount(c)
	assert.Equal(t, 404, w.Code)
}

// ============== ApproveAccount ==============

func TestApproveAccount_Success(t *testing.T) {
	env := newAdminAccountTestEnv(t)
	account := createLocalAccount(t, env, "pending")
	// Mark the associated user as unapproved
	require.NoError(t, env.gdb.Model(&models.User{}).Where("id = ?", account.UserId).Update("status", models.UserStatusUnapproved).Error)

	c, w := newGetContext(t, gin.Params{{Key: "id", Value: uitoa(account.Id)}}, "")
	ApproveAccount(c)

	require.Equal(t, 200, w.Code)
}

func TestApproveAccount_NoUser(t *testing.T) {
	env := newAdminAccountTestEnv(t)
	// Create an account with no associated user via preloading path
	domain := "remote.social"
	account := &models.Account{
		Username:    "remoteuser",
		DisplayName: "Remote",
	}
	account.Domain = &domain
	require.NoError(t, env.gdb.Create(account).Error)

	c, w := newGetContext(t, gin.Params{{Key: "id", Value: uitoa(account.Id)}}, "")
	ApproveAccount(c)

	// account.User == nil => still returns 200 (no-op)
	assert.Equal(t, 422, w.Code)
}

func TestApproveAccount_InvalidID(t *testing.T) {
	newAdminAccountTestEnv(t)
	c, w := newGetContext(t, gin.Params{{Key: "id", Value: "x"}}, "")
	ApproveAccount(c)
	assert.Equal(t, 400, w.Code)
}

func TestApproveAccount_NotFound(t *testing.T) {
	newAdminAccountTestEnv(t)
	c, w := newGetContext(t, gin.Params{{Key: "id", Value: "777777"}}, "")
	ApproveAccount(c)
	assert.Equal(t, 404, w.Code)
}

// ============== RejectAccount ==============

func TestRejectAccount_Success(t *testing.T) {
	env := newAdminAccountTestEnv(t)
	account := createLocalAccount(t, env, "rejectme")
	require.NoError(t, env.gdb.Model(&models.User{}).Where("id = ?", account.UserId).Update("status", models.UserStatusUnapproved).Error)

	c, w := newGetContext(t, gin.Params{{Key: "id", Value: uitoa(account.Id)}}, "")
	RejectAccount(c)

	assert.Equal(t, 200, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.EqualValues(t, models.UserStatusRejected, resp["user_status"])
	var remaining models.Account
	require.NoError(t, env.gdb.First(&remaining, account.Id).Error)
}

func TestRejectAccount_InvalidID(t *testing.T) {
	newAdminAccountTestEnv(t)
	c, w := newGetContext(t, gin.Params{{Key: "id", Value: "bad"}}, "")
	RejectAccount(c)
	assert.Equal(t, 400, w.Code)
}

func TestRejectAccount_NotFound(t *testing.T) {
	newAdminAccountTestEnv(t)
	c, w := newGetContext(t, gin.Params{{Key: "id", Value: "666666"}}, "")
	RejectAccount(c)
	assert.Equal(t, 404, w.Code)
}

// ============== UnsilenceAccount / UnsuspendAccount ==============

func TestUnsilenceAccount_Success(t *testing.T) {
	env := newAdminAccountTestEnv(t)
	account := createLocalAccount(t, env, "sil")
	account.Locked = true
	require.NoError(t, env.gdb.Save(account).Error)

	c, w := newGetContext(t, gin.Params{{Key: "id", Value: uitoa(account.Id)}}, "")
	UnsilenceAccount(c)

	assert.Equal(t, 422, w.Code)
}

func TestUnsilenceAccount_InvalidID(t *testing.T) {
	newAdminAccountTestEnv(t)
	c, w := newGetContext(t, gin.Params{{Key: "id", Value: "zzz"}}, "")
	UnsilenceAccount(c)
	assert.Equal(t, 400, w.Code)
}

func TestUnsilenceAccount_NotFound(t *testing.T) {
	newAdminAccountTestEnv(t)
	c, w := newGetContext(t, gin.Params{{Key: "id", Value: "555555"}}, "")
	UnsilenceAccount(c)
	assert.Equal(t, 404, w.Code)
}

func TestUnsuspendAccount_Success(t *testing.T) {
	env := newAdminAccountTestEnv(t)
	account := createLocalAccount(t, env, "sus")
	account.IsBan = true
	account.Locked = true
	require.NoError(t, env.gdb.Save(account).Error)

	c, w := newGetContext(t, gin.Params{{Key: "id", Value: uitoa(account.Id)}}, "")
	UnsuspendAccount(c)

	require.Equal(t, 200, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp["suspended"].(bool))
}

func TestUnsuspendAccount_InvalidID(t *testing.T) {
	newAdminAccountTestEnv(t)
	c, w := newGetContext(t, gin.Params{{Key: "id", Value: "nope"}}, "")
	UnsuspendAccount(c)
	assert.Equal(t, 400, w.Code)
}

func TestUnsuspendAccount_NotFound(t *testing.T) {
	newAdminAccountTestEnv(t)
	c, w := newGetContext(t, gin.Params{{Key: "id", Value: "444444"}}, "")
	UnsuspendAccount(c)
	assert.Equal(t, 404, w.Code)
}

func TestAccountModerationPreservesPrivacyAndRejectsSelfAction(t *testing.T) {
	env := newAdminAccountTestEnv(t)
	account := createLocalAccount(t, env, "moderated")
	require.NoError(t, env.gdb.Model(account).UpdateColumn("locked", true).Error)
	for _, action := range []string{"disable", "enable", "suspend", "unsuspend"} {
		c, w := newJSONContext(t, http.MethodPost, gin.Params{{Key: "id", Value: uitoa(account.Id)}}, AccountActionRequest{Type: action})
		PerformAccountAction(c)
		require.Equal(t, 200, w.Code, w.Body.String())
		var a models.Account
		require.NoError(t, env.gdb.Preload("User").First(&a, account.Id).Error)
		require.True(t, a.Locked)
		if action == "disable" {
			require.Equal(t, models.UserStatusRejected, a.User.Status)
		}
		if action == "suspend" {
			require.True(t, a.IsBan)
			require.True(t, a.User.IsBan)
		}
		if action == "unsuspend" {
			require.False(t, a.IsBan)
			require.False(t, a.User.IsBan)
		}
	}
	for _, action := range []string{"disable", "suspend", "reject"} {
		c, w := newJSONContext(t, http.MethodPost, gin.Params{{Key: "id", Value: uitoa(account.Id)}}, AccountActionRequest{Type: action})
		c.Set("user", &models.User{Id: account.UserId, IsAdmin: true})
		PerformAccountAction(c)
		require.Equal(t, 422, w.Code)
	}
}

func TestAdminAccountPendingPaginationUsesActualUserState(t *testing.T) {
	env := newAdminAccountTestEnv(t)
	pending := createLocalAccount(t, env, "pendingactual")
	createLocalAccount(t, env, "normalactual")
	require.NoError(t, env.gdb.Model(&models.User{}).Where("id = ?", pending.UserId).UpdateColumn("status", models.UserStatusUnapproved).Error)
	c, w := newGetContext(t, nil, "status=pending&limit=1")
	ListAccounts(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, "1", w.Header().Get("X-Total-Count"))
	var rows []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rows))
	require.Len(t, rows, 1)
	require.Equal(t, uitoa(pending.Id), rows[0]["id"])
	require.EqualValues(t, models.UserStatusUnapproved, rows[0]["user_status"])
}

func TestGetAccount_IncludesSignInAndPostTimes(t *testing.T) {
	env := newAdminAccountTestEnv(t)
	account := createLocalAccount(t, env, "ler")
	posted := time.Date(2026, 10, 1, 13, 0, 45, 0, time.UTC)
	signedIn := time.Date(2026, 10, 2, 1, 2, 3, 0, time.UTC)
	require.NoError(t, env.gdb.Model(account).UpdateColumn("last_status_at", posted).Error)
	require.NoError(t, env.gdb.Model(&models.User{}).Where("id = ?", account.UserId).UpdateColumns(map[string]any{
		"current_sign_in_at": signedIn,
		"current_sign_in_ip": "203.0.113.10",
	}).Error)

	c, w := newGetContext(t, gin.Params{{Key: "id", Value: uitoa(account.Id)}}, "")
	GetAccount(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "2026-10-01T13:00:45Z", resp["last_status_at"])
	assert.Equal(t, "2026-10-02T01:02:03Z", resp["current_sign_in_at"])
	assert.Nil(t, resp["last_sign_in_at"])
	assert.Equal(t, "203.0.113.10", resp["current_sign_in_ip"])
	assert.Nil(t, resp["last_sign_in_ip"])
}

func TestListAccounts_IncludesActivityColumnsAndHidesDeletion(t *testing.T) {
	env := newAdminAccountTestEnv(t)
	visible := createLocalAccount(t, env, "visible")
	hidden := createLocalAccount(t, env, "hidden")
	posted := time.Date(2026, 10, 1, 13, 0, 45, 0, time.UTC)
	require.NoError(t, env.gdb.Model(visible).UpdateColumn("last_status_at", posted).Error)
	require.NoError(t, env.gdb.Model(hidden).UpdateColumn("deletion_requested_at", time.Now()).Error)

	c, w := newGetContext(t, nil, "")
	ListAccounts(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp, 1)
	assert.Equal(t, "visible", resp[0]["username"])
	assert.Equal(t, "2026-10-01T13:00:45Z", resp[0]["last_status_at"])
	assert.Nil(t, resp[0]["current_sign_in_at"])
	assert.Nil(t, resp[0]["current_sign_in_ip"])
}

func TestDeleteAccount_SchedulesErasureAndLeavesTheList(t *testing.T) {
	env := newAdminAccountTestEnv(t)
	require.NoError(t, env.gdb.AutoMigrate(&models.AccountDeletion{}))
	actor := createLocalAccount(t, env, "mod")
	target := createLocalAccount(t, env, "ler")

	c, w := newDeleteContext(t, gin.Params{{Key: "id", Value: uitoa(target.Id)}})
	DeleteAccount(c)
	assert.Equal(t, 401, w.Code)

	c, w = newDeleteContext(t, gin.Params{{Key: "id", Value: uitoa(target.Id)}})
	c.Set("user", &models.User{Id: target.UserId, IsAdmin: true})
	DeleteAccount(c)
	assert.Equal(t, 422, w.Code)

	domain := "remote.example"
	require.NoError(t, env.gdb.Model(target).UpdateColumn("domain", domain).Error)
	c, w = newDeleteContext(t, gin.Params{{Key: "id", Value: uitoa(target.Id)}})
	c.Set("user", &models.User{Id: actor.UserId, IsAdmin: true})
	DeleteAccount(c)
	assert.Equal(t, 422, w.Code)
	require.NoError(t, env.gdb.Model(target).Update("domain", gorm.Expr("NULL")).Error)

	require.NoError(t, env.gdb.Model(&models.User{}).Where("id = ?", target.UserId).UpdateColumn("is_admin", true).Error)
	c, w = newDeleteContext(t, gin.Params{{Key: "id", Value: uitoa(target.Id)}})
	c.Set("user", &models.User{Id: actor.UserId, IsAdmin: true})
	DeleteAccount(c)
	assert.Equal(t, 422, w.Code, w.Body.String())
	require.NoError(t, env.gdb.Model(&models.User{}).Where("id = ?", target.UserId).UpdateColumn("is_admin", false).Error)

	c, w = newDeleteContext(t, gin.Params{{Key: "id", Value: uitoa(target.Id)}})
	c.Set("user", &models.User{Id: actor.UserId, IsAdmin: true})
	DeleteAccount(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	var saved models.Account
	require.NoError(t, env.gdb.First(&saved, target.Id).Error)
	require.NotNil(t, saved.DeletionRequestedAt)
	require.True(t, saved.IsBan)
	var job models.AccountDeletion
	require.NoError(t, env.gdb.First(&job, "account_id = ?", target.Id).Error)
	assert.Equal(t, "pending", job.State)
	assert.Equal(t, target.UserId, job.UserID)

	c, w = newDeleteContext(t, gin.Params{{Key: "id", Value: uitoa(target.Id)}})
	c.Set("user", &models.User{Id: actor.UserId, IsAdmin: true})
	DeleteAccount(c)
	require.Equal(t, 200, w.Code, w.Body.String())

	c, w = newGetContext(t, nil, "")
	ListAccounts(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp, 1)
	assert.Equal(t, "mod", resp[0]["username"])
}

// Execute account filtering against real SQL: the old SQLExec fixture silently
// ignored OR/subquery predicates and could not validate moderation state.
func newAdminAccountTestEnv(t *testing.T) *adminTestEnv {
	t.Helper()
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	raw, err := conn.DB()
	require.NoError(t, err)
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { raw.Close(); database.ResetTestDB() })
	require.NoError(t, conn.AutoMigrate(allTestModels...))
	if !pgtest.Enabled() { // SQLite only auto-generates ids for INTEGER PRIMARY KEY
		var ddl string
		require.NoError(t, conn.Raw("SELECT sql FROM sqlite_master WHERE type='table' AND name='accounts'").Scan(&ddl).Error)
		require.NoError(t, conn.Exec("DROP TABLE accounts").Error)
		require.NoError(t, conn.Exec(strings.ReplaceAll(ddl, "bigint(20)", "integer")).Error)
	}
	database.SetTestDB(conn)
	return &adminTestEnv{gdb: conn}
}
