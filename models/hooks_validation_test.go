package models

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kasuganosora/community/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ============================================================================
// account.go - hooks, helpers and conversions
// ============================================================================

func TestAccount_TableName(t *testing.T) {
	a := Account{}
	assert.Equal(t, "accounts", a.TableName())
}

func TestAccount_BeforeSave_LocalAccount(t *testing.T) {
	a := &Account{Username: "alice"}
	err := a.BeforeSave(&gorm.DB{})
	require.NoError(t, err)
	assert.Equal(t, "alice@__LOCAL__", a.Acct)
}

func TestAccount_BeforeSave_RemoteAccount(t *testing.T) {
	domain := "example.com"
	a := &Account{Username: "bob", Domain: &domain}
	err := a.BeforeSave(&gorm.DB{})
	require.NoError(t, err)
	assert.Equal(t, "bob@example.com", a.Acct)
}

func TestAccount_BeforeCreate_BannedAccountGetsLocked(t *testing.T) {
	a := &Account{IsBan: true, Locked: false}
	err := a.BeforeCreate(&gorm.DB{})
	require.NoError(t, err)
	assert.True(t, a.Locked, "banned account should be locked after BeforeCreate")
}

func TestAccount_BeforeCreate_NormalAccountStaysUnlocked(t *testing.T) {
	a := &Account{IsBan: false, Locked: false}
	err := a.BeforeCreate(&gorm.DB{})
	require.NoError(t, err)
	assert.False(t, a.Locked)
}

func TestAccountExtra_TableName(t *testing.T) {
	ae := AccountExtra{}
	assert.Equal(t, "accounts_extra", ae.TableName())
}

func TestAccountEmoji_TableName(t *testing.T) {
	ae := AccountEmoji{}
	assert.Equal(t, "account_emojis", ae.TableName())
}

func TestAccount_IsLocal_NilDomain(t *testing.T) {
	a := &Account{Domain: nil}
	assert.True(t, a.IsLocal())
}

func TestAccount_IsLocal_WithDomain(t *testing.T) {
	domain := "remote.example"
	a := &Account{Domain: &domain}
	assert.False(t, a.IsLocal())
}

func TestAccounts_GetLocalAccounts_Nil(t *testing.T) {
	ret, err := GetLocalAccounts(&gorm.DB{}, nil)
	require.NoError(t, err)
	assert.Empty(t, ret)
}

func TestAccounts_GetLocalAccounts_AccountsSlice(t *testing.T) {
	localDomain := "__LOCAL__"
	remoteDomain := "remote.example"
	accounts := Accounts{
		{Id: 1, Username: "local1"},
		{Id: 2, Username: "remote1", Domain: &remoteDomain},
		{Id: 3, Username: "local2"},
	}
	ret, err := GetLocalAccounts(&gorm.DB{}, accounts)
	require.NoError(t, err)
	// Only nil-domain (local) accounts should be returned
	assert.Len(t, ret, 2)
	assert.False(t, ret.Contains(2))
	_ = localDomain
}

func TestAccounts_GetLocalAccounts_InvalidType(t *testing.T) {
	_, err := GetLocalAccounts(&gorm.DB{}, "invalid-type")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid accountsOrAccountId type")
}

func TestAccounts_GetLocalAccounts_EmptyAccountsSlice(t *testing.T) {
	ret, err := GetLocalAccounts(&gorm.DB{}, Accounts{})
	require.NoError(t, err)
	assert.Empty(t, ret)
}

func TestGetAccountsByText_NoMentions(t *testing.T) {
	ret, err := GetAccountsByText(&gorm.DB{}, "hello world no mentions", 10)
	require.NoError(t, err)
	assert.Empty(t, ret)
}

func TestGetEmailHash_Deterministic(t *testing.T) {
	h1 := GetEmailHash("user@example.com")
	h2 := GetEmailHash("user@example.com")
	assert.NotEmpty(t, h1)
	assert.Equal(t, h1, h2, "same email should produce same hash")
}

func TestGetEmailHash_DifferentEmails(t *testing.T) {
	h1 := GetEmailHash("a@example.com")
	h2 := GetEmailHash("b@example.com")
	assert.NotEqual(t, h1, h2, "different emails should produce different hashes")
}

func TestMatchAccountTags_NoMatch(t *testing.T) {
	assert.Empty(t, MatchAccountTags("just plain text"))
}

func TestMatchAccountTags_Deduplicates(t *testing.T) {
	result := MatchAccountTags("@bob and @bob again @bob")
	assert.Equal(t, []string{"@bob"}, result)
}

// ============================================================================
// notification.go - GetTypeString / NotificationTypeFromString
// ============================================================================

func TestNotification_TableName(t *testing.T) {
	n := Notification{}
	assert.Equal(t, "notifications", n.TableName())
}

func TestNotification_GetTypeString_AllTypes(t *testing.T) {
	tests := []struct {
		typ      uint8
		expected string
	}{
		{NotificationTypeMention, "mention"},
		{NotificationTypeReblog, "reblog"},
		{NotificationTypeFavourite, "favourite"},
		{NotificationTypeFollow, "follow"},
		{NotificationTypePoll, "poll"},
		{NotificationTypeFollowRequest, "follow_request"},
		{NotificationTypeStatusUpdate, "update"},
		{NotificationTypeAdminReport, "admin.report"},
		{NotificationTypeAddList, "add_list"},
		{NotificationTypeRemoveList, "remove_list"},
		{255, ""}, //nolint:gosec // unknown type
	}

	for _, tt := range tests {
		n := &Notification{Type: tt.typ}
		assert.Equal(t, tt.expected, n.GetTypeString())
	}
}

func TestNotificationTypeFromString_AllValid(t *testing.T) {
	tests := []struct {
		str      string
		expected int
	}{
		{"mention", NotificationTypeMention},
		{"reblog", NotificationTypeReblog},
		{"favourite", NotificationTypeFavourite},
		{"follow", NotificationTypeFollow},
		{"poll", NotificationTypePoll},
		{"follow_request", NotificationTypeFollowRequest},
		{"update", NotificationTypeStatusUpdate},
		{"admin.report", NotificationTypeAdminReport},
		{"add_list", NotificationTypeAddList},
		{"remove_list", NotificationTypeRemoveList},
		{"unknown_type", -1},
		{"", -1},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.expected, NotificationTypeFromString(tt.str))
	}
}

func TestNotification_Hooks_TriggerEvents(t *testing.T) {
	n := &Notification{Type: NotificationTypeFollow}

	// All hooks just trigger events (no-op when no handlers registered) and return nil.
	assert.NotPanics(t, func() {
		_ = n.BeforeCreate(&gorm.DB{})
		_ = n.AfterCreate(&gorm.DB{})
		_ = n.BeforeUpdate(&gorm.DB{})
		_ = n.AfterUpdate(&gorm.DB{})
		_ = n.BeforeDelete(&gorm.DB{})
		_ = n.AfterDelete(&gorm.DB{})
		_ = n.BeforeSave(&gorm.DB{})
		_ = n.AfterSave(&gorm.DB{})
	})
}

func TestNotification_Hooks_ReturnNoError(t *testing.T) {
	n := &Notification{Type: NotificationTypeMention}
	require.NoError(t, n.BeforeCreate(&gorm.DB{}))
	require.NoError(t, n.AfterCreate(&gorm.DB{}))
	require.NoError(t, n.BeforeUpdate(&gorm.DB{}))
	require.NoError(t, n.AfterUpdate(&gorm.DB{}))
	require.NoError(t, n.BeforeDelete(&gorm.DB{}))
	require.NoError(t, n.AfterDelete(&gorm.DB{}))
	require.NoError(t, n.BeforeSave(&gorm.DB{}))
	require.NoError(t, n.AfterSave(&gorm.DB{}))
}

func TestNotifications_Type(t *testing.T) {
	var ns Notifications
	assert.Nil(t, ns)
	ns = Notifications{&Notification{Id: 1}}
	assert.Len(t, ns, 1)
}

// ============================================================================
// report.go - status/category methods and hooks
// ============================================================================

func TestReport_TableName(t *testing.T) {
	r := Report{}
	assert.Equal(t, "reports", r.TableName())
}

func TestReport_GetStatusString(t *testing.T) {
	r := &Report{}
	assert.Equal(t, "Unresolved", r.GetStatusString())

	now := time.Now()
	r.ActionTakenAt = &now
	assert.Equal(t, "Resolved", r.GetStatusString())
}

func TestReport_ToCategoryString_AllCategories(t *testing.T) {
	tests := []struct {
		category uint16
		expected string
	}{
		{ReportCategoryOther, "Other"},
		{ReportCategorySpam, "Spam"},
		{ReportCategoryLegal, "Legal"},
		{ReportCategoryViolation, "Violation"},
		{999, "Unknown"},
	}

	for _, tt := range tests {
		r := &Report{Category: tt.category}
		assert.Equal(t, tt.expected, r.ToCategoryString())
	}
}

func TestReport_SetCategory_AllCategories(t *testing.T) {
	tests := []struct {
		str      string
		expected uint16
	}{
		{"Other", ReportCategoryOther},
		{"Spam", ReportCategorySpam},
		{"Legal", ReportCategoryLegal},
		{"Violation", ReportCategoryViolation},
		{"Unknown", ReportCategoryOther}, // default
	}

	for _, tt := range tests {
		r := &Report{}
		r.SetCategory(tt.str)
		assert.Equal(t, tt.expected, r.Category)
	}
}

func TestReport_Hooks_ReturnNoError(t *testing.T) {
	r := &Report{}
	require.NoError(t, r.BeforeCreate(&gorm.DB{}))
	require.NoError(t, r.AfterCreate(&gorm.DB{}))
	require.NoError(t, r.BeforeUpdate(&gorm.DB{}))
	require.NoError(t, r.AfterUpdate(&gorm.DB{}))
	require.NoError(t, r.BeforeDelete(&gorm.DB{}))
	require.NoError(t, r.AfterDelete(&gorm.DB{}))
	require.NoError(t, r.BeforeSave(&gorm.DB{}))
}

func TestReports_Type(t *testing.T) {
	var rs Reports
	assert.Nil(t, rs)
	rs = Reports{&Report{Id: 1}}
	assert.Len(t, rs, 1)
}

// ============================================================================
// user.go - hooks and table names
// ============================================================================

func TestUser_TableName(t *testing.T) {
	u := User{}
	assert.Equal(t, "users", u.TableName())
}

func TestUser_BeforeCreate_GeneratesUUID(t *testing.T) {
	u := &User{}
	require.NoError(t, u.BeforeCreate(&gorm.DB{}))
	assert.NotEmpty(t, u.UUID)
	assert.Len(t, u.UUID, 36, "UUID should be 36 chars")
}

func TestUser_BeforeCreate_PreservesExistingUUID(t *testing.T) {
	u := &User{UUID: "preset-uuid"}
	require.NoError(t, u.BeforeCreate(&gorm.DB{}))
	assert.Equal(t, "preset-uuid", u.UUID)
}

func TestUser_BeforeCreate_DifferentUUIDs(t *testing.T) {
	u1 := &User{}
	u2 := &User{}
	require.NoError(t, u1.BeforeCreate(&gorm.DB{}))
	require.NoError(t, u2.BeforeCreate(&gorm.DB{}))
	assert.NotEqual(t, u1.UUID, u2.UUID)
}

func TestUserSecretData_TableName(t *testing.T) {
	usd := UserSecretData{}
	assert.Equal(t, "user_secret_data", usd.TableName())
}

func TestUserSecretData_AfterFind(t *testing.T) {
	usd := &UserSecretData{
		UserId:   100,
		Salt:     "mysalt",
		Password: "hash",
		Email:    "a@b.com",
	}
	require.NoError(t, usd.AfterFind(&gorm.DB{}))
	// AfterFind should populate originAttribute map
	assert.NotNil(t, usd.originAttribute)
	assert.Equal(t, "mysalt", usd.originAttribute["Salt"])
	assert.Equal(t, "hash", usd.originAttribute["Password"])
}

func TestUserSecretData_BeforeSave_NoKey(t *testing.T) {
	// When EncryptedKey is empty, BeforeSave returns early without error.
	originalKey := config.C.EncryptedKey
	config.C.EncryptedKey = ""
	t.Cleanup(func() { config.C.EncryptedKey = originalKey })

	usd := &UserSecretData{
		UserId:   1,
		Salt:     "salt",
		Password: "password",
		Email:    "test@example.com",
	}
	err := usd.BeforeSave(&gorm.DB{})
	require.NoError(t, err)
	// Email should remain unchanged since no encryption key
	assert.Equal(t, "test@example.com", usd.Email)
}

func TestUserSecretData_BeforeSave_WithKey_NewData(t *testing.T) {
	// With a valid EncryptedKey, BeforeSave encrypts fields tagged with encrypt:"true".
	originalKey := config.C.EncryptedKey
	config.C.EncryptedKey = "test-encryption-key-16"
	t.Cleanup(func() { config.C.EncryptedKey = originalKey })

	usd := &UserSecretData{
		UserId:   1,
		Salt:     "saltvalue",
		Email:    "secret@example.com",
		Password: "plainpassword",
	}
	// originAttribute is empty (fresh object, never AfterFind'd), so the
	// "value unchanged" check fails and encryption proceeds.
	err := usd.BeforeSave(&gorm.DB{})
	require.NoError(t, err)
	// Email (encrypt:"true") should now be encrypted (different from plaintext)
	assert.NotEqual(t, "secret@example.com", usd.Email)
	// Password (not tagged encrypt:"true") should be unchanged
	assert.Equal(t, "plainpassword", usd.Password)
}

func TestUserSecretData_BeforeSave_WithKey_UnchangedData(t *testing.T) {
	// When originAttribute matches current values, no re-encryption occurs.
	originalKey := config.C.EncryptedKey
	config.C.EncryptedKey = "test-encryption-key-16"
	t.Cleanup(func() { config.C.EncryptedKey = originalKey })

	usd := &UserSecretData{
		UserId:   1,
		Salt:     "salt",
		Email:    "already-encrypted-value",
		Password: "password",
	}
	// Populate originAttribute so the "unchanged" branch is taken.
	usd.originAttribute = map[string]any{
		"UserId":   uint64(1),
		"Salt":     "salt",
		"Password": "password",
		"Email":    "already-encrypted-value",
	}
	err := usd.BeforeSave(&gorm.DB{})
	require.NoError(t, err)
	// Email unchanged because it matches originAttribute
	assert.Equal(t, "already-encrypted-value", usd.Email)
}

func TestUserSecretData_AfterSave_RefreshesOriginAttribute(t *testing.T) {
	usd := &UserSecretData{
		UserId:   1,
		Email:    "changed@example.com",
		Password: "newpassword",
	}
	require.NoError(t, usd.AfterSave(&gorm.DB{}))
	assert.NotNil(t, usd.originAttribute)
	assert.Equal(t, "changed@example.com", usd.originAttribute["Email"])
	assert.Equal(t, "newpassword", usd.originAttribute["Password"])
}

// ============================================================================
// list.go - policy methods
// ============================================================================

func TestList_SetRepliesPolicy_AllValues(t *testing.T) {
	tests := []struct {
		str      string
		expected uint8
	}{
		{"list", ListRepliesPolicyList},
		{"followed", ListRepliesPolicyFollowed},
		{"none", ListRepliesPolicyNone},
		{"invalid", ListRepliesPolicyList}, // default
		{"", ListRepliesPolicyList},
	}

	for _, tt := range tests {
		l := &List{}
		l.SetRepliesPolicy(tt.str)
		assert.Equal(t, tt.expected, l.RepliesPolicy)
	}
}

func TestList_GetRepliesPolicy_AllValues(t *testing.T) {
	tests := []struct {
		policy   uint8
		expected string
	}{
		{ListRepliesPolicyList, "list"},
		{ListRepliesPolicyFollowed, "followed"},
		{ListRepliesPolicyNone, "none"},
		{99, "list"}, // default
	}

	for _, tt := range tests {
		l := &List{RepliesPolicy: tt.policy}
		assert.Equal(t, tt.expected, l.GetRepliesPolicy())
	}
}

func TestListCanView_Public(t *testing.T) {
	list := &List{Visibility: ListVisibilityPublic}
	account := &Account{Id: 1}
	pass, err := ListCanView(list, account)
	require.NoError(t, err)
	assert.True(t, pass)
}

func TestListCanView_Owner(t *testing.T) {
	list := &List{Visibility: ListVisibilityPrivate, AccountId: 5}
	owner := &Account{Id: 5}
	pass, err := ListCanView(list, owner)
	require.NoError(t, err)
	assert.True(t, pass)
}

func TestListCanView_FollowerInList(t *testing.T) {
	list := &List{
		Visibility: ListVisibilityPrivate,
		AccountId:  1,
		Followers:  []*Account{{Id: 2}},
	}
	follower := &Account{Id: 2}
	pass, err := ListCanView(list, follower)
	require.NoError(t, err)
	assert.True(t, pass)
}

func TestListCanView_NotFollower(t *testing.T) {
	list := &List{
		Visibility: ListVisibilityPrivate,
		AccountId:  1,
		Followers:  []*Account{{Id: 2}},
	}
	stranger := &Account{Id: 99}
	pass, err := ListCanView(list, stranger)
	require.NoError(t, err)
	assert.False(t, pass)
}

func TestListAccount_TableName(t *testing.T) {
	la := ListAccount{}
	assert.Equal(t, "list_accounts", la.TableName())
}

func TestListFollowAccount_TableName(t *testing.T) {
	lfa := ListFollowAccount{}
	assert.Equal(t, "list_follow_accounts", lfa.TableName())
}

// ============================================================================
// invite_code.go - IsAvailable
// ============================================================================

func TestInviteCode_TableName(t *testing.T) {
	ic := InviteCode{}
	assert.Equal(t, "invite_codes", ic.TableName())
}

func TestInviteCode_IsAvailable_DefaultValid(t *testing.T) {
	ic := &InviteCode{}
	assert.True(t, ic.IsAvailable())
}

func TestInviteCode_IsAvailable_Disabled(t *testing.T) {
	ic := &InviteCode{Disabled: true}
	assert.False(t, ic.IsAvailable())
}

func TestInviteCode_IsAvailable_MaxUseReached(t *testing.T) {
	ic := &InviteCode{MaxUseCount: 5, UseCount: 5}
	assert.False(t, ic.IsAvailable())
}

func TestInviteCode_IsAvailable_MaxUseExceeded(t *testing.T) {
	ic := &InviteCode{MaxUseCount: 3, UseCount: 10}
	assert.False(t, ic.IsAvailable())
}

func TestInviteCode_IsAvailable_UnderMaxUse(t *testing.T) {
	ic := &InviteCode{MaxUseCount: 5, UseCount: 2}
	assert.True(t, ic.IsAvailable())
}

func TestInviteCode_IsAvailable_Expired(t *testing.T) {
	past := time.Now().Add(-1 * time.Hour)
	ic := &InviteCode{ExpireAt: &past}
	assert.False(t, ic.IsAvailable())
}

func TestInviteCode_IsAvailable_NotExpired(t *testing.T) {
	future := time.Now().Add(1 * time.Hour)
	ic := &InviteCode{ExpireAt: &future}
	assert.True(t, ic.IsAvailable())
}

func TestInviteCode_IsAvailable_UnlimitedUse(t *testing.T) {
	// MaxUseCount == 0 means unlimited
	ic := &InviteCode{MaxUseCount: 0, UseCount: 1000}
	assert.True(t, ic.IsAvailable())
}

// ============================================================================
// mute.go - ExcludeExpired / IsMuted
// ============================================================================

func TestMuteAccount_Hooks_TableName(t *testing.T) {
	m := MuteAccount{}
	assert.Equal(t, "mute_accounts", m.TableName())
}

func TestMuteStatus_Hooks_TableName(t *testing.T) {
	m := MuteStatus{}
	assert.Equal(t, "mute_statuses", m.TableName())
}

func TestMuteAccounts_Hooks_ExcludeExpired(t *testing.T) {
	past := time.Now().Add(-1 * time.Hour)
	future := time.Now().Add(1 * time.Hour)
	mm := MuteAccounts{
		{Id: 1, TargetId: 10, ExpiredAt: nil},     // not expired (nil = permanent)
		{Id: 2, TargetId: 20, ExpiredAt: &future}, // not expired
		{Id: 3, TargetId: 30, ExpiredAt: &past},   // expired
	}
	result := mm.ExcludeExpired()
	assert.Len(t, result, 2)
	assert.False(t, result.IsMuted(30))
	assert.True(t, result.IsMuted(10))
	assert.True(t, result.IsMuted(20))
}

func TestMuteAccounts_Hooks_ExcludeExpired_AllExpired(t *testing.T) {
	past := time.Now().Add(-1 * time.Hour)
	mm := MuteAccounts{
		{Id: 1, TargetId: 10, ExpiredAt: &past},
		{Id: 2, TargetId: 20, ExpiredAt: &past},
	}
	result := mm.ExcludeExpired()
	assert.Empty(t, result)
}

func TestMuteAccounts_IsMuted_NotFound(t *testing.T) {
	mm := MuteAccounts{
		{Id: 1, TargetId: 10},
	}
	assert.False(t, mm.IsMuted(999))
	assert.True(t, mm.IsMuted(10))
}

func TestMuteAccounts_IsMuted_Empty(t *testing.T) {
	var mm MuteAccounts
	assert.False(t, mm.IsMuted(1))
}

// ============================================================================
// block.go - additional coverage
// ============================================================================

func TestBlocks_IsStatusBlocked_ReblogBlocked(t *testing.T) {
	domain := "blocked.example"
	reblogAccount := &Account{Id: 50, Domain: &domain}
	status := &Status{
		AccountId: 10,
		Account:   &Account{Id: 10},
		Reblog: &Status{
			AccountId: 50,
			Account:   reblogAccount,
		},
	}
	blocks := Blocks{
		{Type: BlockTypeAccount, AccountId: 50, TargetAccountId: 999},
	}
	// The reblog's author (id=50) is involved in a block as AccountId
	assert.True(t, blocks.IsStatusBlocked(status))
}

func TestBlocks_IsStatusBlocked_NotBlocked(t *testing.T) {
	status := &Status{
		AccountId: 10,
		Account:   &Account{Id: 10},
	}
	blocks := Blocks{
		{Type: BlockTypeAccount, AccountId: 999, TargetAccountId: 888},
	}
	assert.False(t, blocks.IsStatusBlocked(status))
}

// ============================================================================
// follow.go - Followed/FollowRequested scopes require a fully initialized
// gorm.DB; since models tests cannot import testutil (circular import),
// the scope functions are exercised indirectly via existing follow tests.
// ============================================================================

// ============================================================================
// conversation.go - BeforeSave hooks
// ============================================================================

func TestConversation_BeforeSave_NewConversation(t *testing.T) {
	// New conversation (Id == 0) with no accounts: should not attempt delete.
	c := &Conversation{Id: 0, AccountsIds: []uint64{}}
	err := c.BeforeSave(&gorm.DB{})
	require.NoError(t, err)
}

func TestConversation_BeforeSave_ExistingWithAccounts(t *testing.T) {
	c := &Conversation{Id: 1, AccountsIds: []uint64{10, 20}}
	err := c.BeforeSave(&gorm.DB{})
	require.NoError(t, err)
}

// ============================================================================
// relay.go - state methods
// ============================================================================

func TestRelay_TableName(t *testing.T) {
	r := Relay{}
	assert.Equal(t, "relays", r.TableName())
}

func TestRelay_IsEnabled(t *testing.T) {
	r := &Relay{State: RelayStateAccepted}
	assert.True(t, r.IsEnabled())

	r.State = RelayStateIdle
	assert.False(t, r.IsEnabled())
}

func TestRelay_IsPending(t *testing.T) {
	r := &Relay{State: RelayStatePending}
	assert.True(t, r.IsPending())

	r.State = RelayStateAccepted
	assert.False(t, r.IsPending())
}

func TestRelay_SetState(t *testing.T) {
	r := &Relay{State: RelayStateIdle}
	r.SetState(RelayStateAccepted)
	assert.Equal(t, RelayStateAccepted, r.State)

	r.SetState(RelayStateRejected)
	assert.Equal(t, RelayStateRejected, r.State)
}

// ============================================================================
// auth_token.go - NewAuthToken
// ============================================================================

func TestAuthToken_Hooks_TableName(t *testing.T) {
	a := AuthToken{}
	assert.Equal(t, "auth_tokens", a.TableName())
}

func TestNewAuthToken_WithExpiry(t *testing.T) {
	scopes := &ApplicationScope{}
	tokenRow, tokenStr := NewAuthToken(123, AuthTokenTypeWeb, 1, 0x10, scopes, 3600, "0123456789abcdef")
	require.NotNil(t, tokenRow)
	assert.NotEmpty(t, tokenStr)
	assert.Equal(t, uint64(123), tokenRow.UserId)
	assert.Equal(t, uint8(AuthTokenTypeWeb), tokenRow.Type)
	assert.NotNil(t, tokenRow.ExpiredAt)
}

func TestNewAuthToken_NoExpiry(t *testing.T) {
	tokenRow, tokenStr := NewAuthToken(456, AuthTokenTypeOauth, 0, 0, nil, 0, "0123456789abcdef")
	require.NotNil(t, tokenRow)
	assert.NotEmpty(t, tokenStr)
	assert.Equal(t, uint64(456), tokenRow.UserId)
	assert.Nil(t, tokenRow.ExpiredAt, "expiredIn=0 means never expire")
}

func TestAuthTokenData_GetTokenStr_RoundTrip(t *testing.T) {
	key := "0123456789abcdef0123456789abcdef"
	original := AuthTokenData{
		UserId:        100,
		AuthType:      AuthTokenTypeWeb,
		ApplicationId: 5,
		Permissions:   0x1F,
		CreatedAt:     1700000000,
		ExpiredAt:     1700003600,
	}
	tokenStr := original.GetTokenStr(key)
	assert.NotEmpty(t, tokenStr)
	assert.Greater(t, len(tokenStr), 16)

	// Decrypt and verify round-trip
	decoded, err := GetAuthTokenData(tokenStr, key)
	require.NoError(t, err)
	assert.Equal(t, original.UserId, decoded.UserId)
	assert.Equal(t, original.AuthType, decoded.AuthType)
	assert.Equal(t, original.ApplicationId, decoded.ApplicationId)
	assert.Equal(t, original.Permissions, decoded.Permissions)
	assert.Equal(t, original.CreatedAt, decoded.CreatedAt)
	assert.Equal(t, original.ExpiredAt, decoded.ExpiredAt)
}

func TestAuthTokenData_GetTokenStr_AnyKeyWorks(t *testing.T) {
	// AesEncrypt hashes the key via SHA256, so even a short key produces a valid
	// 32-byte AES key. The token should therefore be non-empty and round-trip.
	data := AuthTokenData{UserId: 1, AuthType: AuthTokenTypeWeb}
	tokenStr := data.GetTokenStr("shortkey")
	assert.NotEmpty(t, tokenStr)

	decoded, err := GetAuthTokenData(tokenStr, "shortkey")
	require.NoError(t, err)
	assert.Equal(t, uint64(1), decoded.UserId)
}

func TestGetAuthTokenData_TooShort(t *testing.T) {
	_, err := GetAuthTokenData("short", "0123456789abcdef")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid token length")
}

func TestGetAuthTokenData_InvalidCheckCode(t *testing.T) {
	// 16+ chars but with a wrong check code prefix
	_, err := GetAuthTokenData("zzzzzzzz"+"randomstr"+"encryptedpayloaddatahere", "0123456789abcdef")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid token check code")
}

// ============================================================================
// application.go - scope methods
// ============================================================================

func TestApplication_TableName(t *testing.T) {
	a := Application{}
	assert.Equal(t, "applications", a.TableName())
}

func TestApplicationUserAuth_TableName(t *testing.T) {
	a := ApplicationUserAuth{}
	assert.Equal(t, "application_user_auths", a.TableName())
}

func TestApplicationScope_RemoveScope_AllTypes(t *testing.T) {
	s := &ApplicationScope{
		ReadScope:  0xFF,
		WriteScope: 0xFF,
		AdminScope: 0xFF,
		OtherScope: 0xFF,
	}
	s.RemoveScope(AppScopeTypeRead, 0x3)
	s.RemoveScope(AppScopeTypeWrite, 0x7)
	s.RemoveScope(AppScopeTypeAdmin, 0xF)
	s.RemoveScope(AppScopeTypeOther, 0x1)

	assert.Equal(t, uint64(0xFC), s.ReadScope)
	assert.Equal(t, uint64(0xF8), s.WriteScope)
	assert.Equal(t, uint64(0xF0), s.AdminScope)
	assert.Equal(t, uint64(0xFE), s.OtherScope)
}

func TestApplicationScope_RemoveScope_InvalidType(t *testing.T) {
	s := &ApplicationScope{ReadScope: 0xFF}
	// Invalid scope type (99) should be a no-op
	s.RemoveScope(99, 0x3)
	assert.Equal(t, uint64(0xFF), s.ReadScope)
}

func TestApplicationScope_AddScope_InvalidType(t *testing.T) {
	s := &ApplicationScope{}
	// Invalid scope type (99) should be a no-op
	s.AddScope(99, 0x3)
	assert.Equal(t, uint64(0), s.ReadScope)
	assert.Equal(t, uint64(0), s.WriteScope)
}

func TestApplicationScope_Scan_Bytes(t *testing.T) {
	s := &ApplicationScope{}
	// Use the MarshalJSON output as the scan input
	orig := &ApplicationScope{ReadScope: 1, WriteScope: 2, AdminScope: 3, OtherScope: 4}
	data, err := orig.MarshalJSON()
	require.NoError(t, err)

	err = s.Scan(context.Background(), nil, reflect.Value{}, data)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), s.ReadScope)
	assert.Equal(t, uint64(2), s.WriteScope)
	assert.Equal(t, uint64(3), s.AdminScope)
	assert.Equal(t, uint64(4), s.OtherScope)
}

func TestApplicationScope_Scan_String(t *testing.T) {
	s := &ApplicationScope{}
	orig := &ApplicationScope{ReadScope: 7}
	data, err := orig.MarshalJSON()
	require.NoError(t, err)

	err = s.Scan(context.Background(), nil, reflect.Value{}, string(data))
	require.NoError(t, err)
	assert.Equal(t, uint64(7), s.ReadScope)
}

func TestApplicationScope_Scan_InvalidType(t *testing.T) {
	s := &ApplicationScope{}
	err := s.Scan(context.Background(), nil, reflect.Value{}, 12345)
	assert.Error(t, err)
}

func TestApplicationScope_Scan_InvalidHex(t *testing.T) {
	s := &ApplicationScope{}
	err := s.Scan(context.Background(), nil, reflect.Value{}, "not-hex!!")
	assert.Error(t, err)
}

func TestApplicationScope_Value(t *testing.T) {
	s := &ApplicationScope{ReadScope: 1}
	ret, err := s.Value(context.Background(), nil, reflect.Value{}, nil)
	require.NoError(t, err)
	assert.NotNil(t, ret)
}

func TestApplicationScope_UnmarshalJSON_TooShort(t *testing.T) {
	s := &ApplicationScope{}
	err := s.UnmarshalJSON([]byte("ab")) // 2 hex chars = 1 byte, need 32 bytes
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid scope data length")
}

func TestApplication_BeforeCreate_GeneratesUUID(t *testing.T) {
	a := &Application{}
	require.NoError(t, a.BeforeCreate(&gorm.DB{}))
	require.NotNil(t, a.Uuid)
}

func TestApplication_BeforeCreate_PreservesUUID(t *testing.T) {
	preset := uuid.New()
	a := &Application{Uuid: &preset}
	require.NoError(t, a.BeforeCreate(&gorm.DB{}))
	assert.Equal(t, preset, *a.Uuid)
}

// ============================================================================
// poll.go - Delete and collections
// ============================================================================

func TestPoll_Hooks_TableName(t *testing.T) {
	p := Poll{}
	assert.Equal(t, "polls", p.TableName())
}

func TestPollOption_Hooks_TableName(t *testing.T) {
	po := PollOption{}
	assert.Equal(t, "poll_options", po.TableName())
}

func TestPollOptionVote_Hooks_TableName(t *testing.T) {
	pov := PollOptionVote{}
	assert.Equal(t, "poll_option_votes", pov.TableName())
}

func TestPollOptions_GetOptionIds_Empty(t *testing.T) {
	ids := PollOptions{}.GetOptionIds()
	assert.Empty(t, ids)
}

func TestPollOptions_GetNames_Empty(t *testing.T) {
	names := PollOptions{}.GetNames()
	assert.Nil(t, names)
}

// ============================================================================
// filter.go - GetFilters and collections
// ============================================================================

func TestFilter_GetStatusFilters_SkipsOwnStatus(t *testing.T) {
	status := &Status{AccountId: 10}
	account := &Account{Id: 10}
	filter := &Filter{
		FilterAction: FilterActionHide,
		Keywords:     []*FilterKeyword{{Keyword: "spam"}},
	}
	ff := Filters{filter}
	result := ff.GetStatusFilters(status, account)
	assert.Empty(t, result, "own status should not be filtered")
}

func TestFilter_GetHeightLevelFilter_Empty(t *testing.T) {
	ff := Filters{}
	result := ff.GetHeightLevelFilter()
	assert.Nil(t, result)
}

// ============================================================================
// tag.go - GetTagsFromText (no db needed for empty result)
// ============================================================================

func TestGetTagsFromText_NoTags(t *testing.T) {
	ret, err := GetTagsFromText(&gorm.DB{}, "no tags here")
	require.NoError(t, err)
	assert.Empty(t, ret)
}

// ============================================================================
// emoji.go - GetEmojiFromText (no db needed for empty result)
// ============================================================================

func TestGetEmojiFromText_NoEmojis(t *testing.T) {
	ret, err := GetEmojiFromText(&gorm.DB{}, "no emojis here")
	require.NoError(t, err)
	assert.Empty(t, ret)
}

func TestEmojis_Hooks_GetIds_Empty(t *testing.T) {
	ids := Emojis{}.GetIds()
	assert.Empty(t, ids)
}

// ============================================================================
// status.go - SetConversationId, SetVisibilityByString, IsScheduled
// ============================================================================

func TestStatus_SetVisibilityByString_AllValues(t *testing.T) {
	tests := []struct {
		str      string
		expected uint8
	}{
		{"public", StatusVisibilityPublic},
		{"unlisted", StatusVisibilityUnlisted},
		{"private", StatusVisibilityPrivate},
		{"direct", StatusVisibilityDirect},
		{"PUBLIC", StatusVisibilityPublic}, // case-insensitive
		{"Unlisted", StatusVisibilityUnlisted},
	}

	for _, tt := range tests {
		s := &Status{}
		err := s.SetVisibilityByString(tt.str)
		require.NoError(t, err)
		assert.Equal(t, tt.expected, s.Visibility)
	}
}

func TestStatus_SetVisibilityByString_Invalid(t *testing.T) {
	s := &Status{}
	err := s.SetVisibilityByString("invalid")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid visibility")
}

func TestStatus_GetVisibilityString_Unknown(t *testing.T) {
	s := &Status{Visibility: 99}
	assert.Equal(t, "unknown", s.GetVisibilityString())
}

func TestStatus_IsScheduled_NotScheduled(t *testing.T) {
	s := &Status{}
	assert.False(t, s.IsScheduled())
}

func TestStatus_IsScheduled_Scheduled(t *testing.T) {
	future := time.Now().Add(1 * time.Hour)
	s := &Status{ScheduledAt: &future}
	assert.True(t, s.IsScheduled())
}

func TestStatus_SetConversationId_NewFromZero(t *testing.T) {
	s := &Status{}
	s.SetConversationId(42)
	require.NotNil(t, s.ConversationId)
	assert.Equal(t, uint64(42), *s.ConversationId)
	assert.True(t, s.conversationIdChanged)
}

func TestStatus_SetConversationId_SameValueNoChange(t *testing.T) {
	existing := uint64(42)
	s := &Status{ConversationId: &existing}
	s.SetConversationId(42)
	assert.False(t, s.conversationIdChanged, "setting same value should not mark changed")
}

func TestStatus_SetConversationId_SetToZeroWhenNil(t *testing.T) {
	s := &Status{}
	s.SetConversationId(0)
	assert.Nil(t, s.ConversationId)
	assert.False(t, s.conversationIdChanged)
}

func TestStatus_SetConversationId_ClearExisting(t *testing.T) {
	existing := uint64(42)
	s := &Status{ConversationId: &existing}
	s.SetConversationId(0)
	assert.Nil(t, s.ConversationId)
	assert.True(t, s.conversationIdChanged)
}

func TestStatus_CanView_NilAccount(t *testing.T) {
	s := &Status{Visibility: StatusVisibilityPublic}
	assert.True(t, s.CanView(nil, nil))

	s.Visibility = StatusVisibilityPrivate
	assert.False(t, s.CanView(nil, nil))
}

func TestStatus_CanView_NilAccountUnlisted(t *testing.T) {
	s := &Status{Visibility: StatusVisibilityUnlisted}
	assert.True(t, s.CanView(nil, nil))
}

func TestStatus_CanView_Owner(t *testing.T) {
	s := &Status{AccountId: 10, Visibility: StatusVisibilityPrivate}
	account := &Account{Id: 10}
	assert.True(t, s.CanView(account, map[uint64]*Follow{}))
}

func TestStatus_CanView_ScheduledNotOwner(t *testing.T) {
	future := time.Now().Add(1 * time.Hour)
	s := &Status{
		AccountId:   10,
		Visibility:  StatusVisibilityPublic,
		ScheduledAt: &future,
	}
	account := &Account{Id: 99}
	assert.False(t, s.CanView(account, nil))
}

func TestStatus_CanView_PublicVisibleToAll(t *testing.T) {
	s := &Status{AccountId: 10, Visibility: StatusVisibilityPublic}
	account := &Account{Id: 99}
	assert.True(t, s.CanView(account, map[uint64]*Follow{}))
}

func TestStatus_CanView_PrivateByFollower(t *testing.T) {
	s := &Status{AccountId: 10, Visibility: StatusVisibilityPrivate}
	follower := &Account{Id: 99}
	follows := map[uint64]*Follow{10: {}}
	assert.True(t, s.CanView(follower, follows))
}

func TestStatus_CanView_PrivateNotFollowerNotMentioned(t *testing.T) {
	s := &Status{AccountId: 10, Visibility: StatusVisibilityPrivate}
	stranger := &Account{Id: 99}
	assert.False(t, s.CanView(stranger, map[uint64]*Follow{}))
}

func TestStatus_CanView_PrivateMentioned(t *testing.T) {
	s := &Status{
		AccountId:  10,
		Visibility: StatusVisibilityPrivate,
		ToAccounts: Accounts{{Id: 99}},
	}
	mentioned := &Account{Id: 99}
	assert.True(t, s.CanView(mentioned, map[uint64]*Follow{}))
}

func TestStatus_CanView_DirectMentioned(t *testing.T) {
	s := &Status{
		AccountId:  10,
		Visibility: StatusVisibilityDirect,
		ToAccounts: Accounts{{Id: 99}},
	}
	mentioned := &Account{Id: 99}
	assert.True(t, s.CanView(mentioned, map[uint64]*Follow{}))
}

func TestStatus_CanView_DirectNotMentioned(t *testing.T) {
	s := &Status{
		AccountId:  10,
		Visibility: StatusVisibilityDirect,
		ToAccounts: Accounts{{Id: 88}},
	}
	stranger := &Account{Id: 99}
	assert.False(t, s.CanView(stranger, map[uint64]*Follow{}))
}

func TestStatuses_GetIds(t *testing.T) {
	ss := Statuses{
		{Id: 1},
		{Id: 2},
		{Id: 3},
	}
	assert.Equal(t, []uint64{1, 2, 3}, ss.GetIds())
}

func TestStatuses_GetIds_Empty(t *testing.T) {
	ss := Statuses{}
	assert.Nil(t, ss.GetIds())
}

func TestStatuses_GetIdsWithoutReblogWarp(t *testing.T) {
	reblogId := uint64(5)
	ss := Statuses{
		{Id: 1, ReblogId: nil},
		{Id: 2, ReblogId: &reblogId},
	}
	ids := ss.GetIdsWithoutReblogWarp()
	assert.Equal(t, []uint64{1, 5}, ids)
}

func TestStatuses_GetAccountIds_Deduplicates(t *testing.T) {
	ss := Statuses{
		{AccountId: 1},
		{AccountId: 2},
		{AccountId: 1}, // duplicate
	}
	ids := ss.GetAccountIds()
	assert.Equal(t, []uint64{1, 2}, ids)
}

func TestStatuses_Filter(t *testing.T) {
	ss := Statuses{
		{Id: 1, Visibility: StatusVisibilityPublic},
		{Id: 2, Visibility: StatusVisibilityPrivate},
		{Id: 3, Visibility: StatusVisibilityPublic},
	}
	result := ss.Filter(func(s *Status) bool {
		return s.Visibility == StatusVisibilityPublic
	})
	assert.Len(t, result, 2)
	assert.Equal(t, uint64(1), result[0].Id)
	assert.Equal(t, uint64(3), result[1].Id)
}

func TestStatuses_Filter_None(t *testing.T) {
	ss := Statuses{
		{Id: 1, Visibility: StatusVisibilityPublic},
	}
	result := ss.Filter(func(s *Status) bool {
		return false
	})
	assert.Empty(t, result)
}

func TestStatusContent_TableName(t *testing.T) {
	sc := StatusContent{}
	assert.Equal(t, "status_contents", sc.TableName())
}

// ============================================================================
// system_setting.go, marker.go, rule.go - table names
// ============================================================================

func TestSystemSetting_TableName(t *testing.T) {
	ss := SystemSetting{}
	assert.Equal(t, "system_settings", ss.TableName())
}

func TestMarker_Hooks_TableName(t *testing.T) {
	m := Marker{}
	assert.Equal(t, "markers", m.TableName())
}

func TestRule_TableName(t *testing.T) {
	r := Rule{}
	assert.Equal(t, "rules", r.TableName())
}
