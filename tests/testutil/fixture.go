package testutil

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils"
	"gorm.io/gorm"
)

// FixtureBuilder helps create test data.
type FixtureBuilder struct {
	t  *testing.T
	db *gorm.DB
}

// NewFixtureBuilder creates a new fixture builder.
func NewFixtureBuilder(t *testing.T, db *gorm.DB) *FixtureBuilder {
	t.Helper()

	return &FixtureBuilder{t: t, db: db}
}

// CreateUser creates a test user.
func (f *FixtureBuilder) CreateUser(overrides ...func(*models.User)) *models.User {
	f.t.Helper()

	now := time.Now()
	user := &models.User{
		UUID:           uuid.New().String(),
		UserName:       "testuser", //nolint:goconst
		EmailHash:      "test_hash_" + RandomString(8),
		CreatedAt:      &now,
		UpdatedAt:      &now,
		Status:         models.UserStatusNormal,
		IsMailVerified: true,
	}

	for _, override := range overrides {
		override(user)
	}

	err := f.db.Create(user).Error
	if err != nil {
		f.t.Fatalf("FixtureBuilder.CreateUser failed: %v", err)
	}

	return user
}

// CreateAccount creates a test account.
func (f *FixtureBuilder) CreateAccount(user *models.User, overrides ...func(*models.Account)) *models.Account {
	f.t.Helper()

	now := time.Now()
	account := &models.Account{
		UserId:      user.Id,
		Username:    "testuser",
		DisplayName: "Test User",
		Locked:      false,
		Bot:         false,
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}

	for _, override := range overrides {
		override(account)
	}

	err := f.db.Create(account).Error
	if err != nil {
		f.t.Fatalf("FixtureBuilder.CreateAccount failed: %v", err)
	}

	return account
}

// CreateStatus creates a test status.
func (f *FixtureBuilder) CreateStatus(account *models.Account, overrides ...func(*models.Status)) *models.Status {
	f.t.Helper()

	now := time.Now()
	status := &models.Status{
		AccountId:  account.Id,
		Visibility: uint8(models.StatusVisibilityPublic),
		Sensitive:  false,
		CreatedAt:  &now,
		UpdatedAt:  &now,
	}

	for _, override := range overrides {
		override(status)
	}

	err := f.db.Create(status).Error
	if err != nil {
		f.t.Fatalf("FixtureBuilder.CreateStatus failed: %v", err)
	}

	return status
}

// CreateFollow creates a follow relationship.
func (f *FixtureBuilder) CreateFollow(follower, following *models.Account, overrides ...func(*models.Follow)) *models.Follow {
	f.t.Helper()

	now := time.Now()
	follow := &models.Follow{
		AccountId: follower.Id,
		FollowId:  following.Id,
		Status:    models.FollowStatusFollowed,
		CreatedAt: now.Unix(),
	}

	for _, override := range overrides {
		override(follow)
	}

	err := f.db.Create(follow).Error
	if err != nil {
		f.t.Fatalf("FixtureBuilder.CreateFollow failed: %v", err)
	}

	return follow
}

// CreateBlock creates a block relationship.
func (f *FixtureBuilder) CreateBlock(blocker, blocked *models.Account, overrides ...func(*models.Block)) *models.Block {
	f.t.Helper()

	now := time.Now()
	block := &models.Block{
		AccountId:       blocker.Id,
		TargetAccountId: blocked.Id,
		Type:            models.BlockTypeAccount,
		CreatedAt:       &now,
	}

	for _, override := range overrides {
		override(block)
	}

	err := f.db.Create(block).Error
	if err != nil {
		f.t.Fatalf("FixtureBuilder.CreateBlock failed: %v", err)
	}

	return block
}

// CreateAuthToken creates a test auth token.
func (f *FixtureBuilder) CreateAuthToken(userId uint64, overrides ...func(*models.AuthToken)) *models.AuthToken {
	f.t.Helper()

	now := time.Now()
	expiresAt := now.Add(24 * time.Hour)
	token := &models.AuthToken{
		UserId:    userId,
		Token:     "test-token-" + RandomString(16),
		CreatedAt: &now,
		ExpiredAt: &expiresAt,
	}

	for _, override := range overrides {
		override(token)
	}

	err := f.db.Create(token).Error
	if err != nil {
		f.t.Fatalf("FixtureBuilder.CreateAuthToken failed: %v", err)
	}

	return token
}

// CreateRole creates a test role.
func (f *FixtureBuilder) CreateRole(overrides ...func(*models.Role)) *models.Role {
	f.t.Helper()

	role := &models.Role{
		Name:        "Test Role",
		Color:       "#FF0000",
		Highlighted: false,
		Permissions: 0,
	}

	for _, override := range overrides {
		override(role)
	}

	err := f.db.Create(role).Error
	if err != nil {
		f.t.Fatalf("FixtureBuilder.CreateRole failed: %v", err)
	}

	return role
}

// CreateFilter creates a test filter.
func (f *FixtureBuilder) CreateFilter(accountId uint64, overrides ...func(*models.Filter)) *models.Filter {
	f.t.Helper()

	now := time.Now()
	filter := &models.Filter{
		AccountId:    accountId,
		Title:        "Test Filter",
		Context:      []string{"home"},
		ExpiresAt:    nil,
		FilterAction: uint8(models.FilterActionWarn),
		CreatedAt:    &now,
		UpdatedAt:    &now,
	}

	for _, override := range overrides {
		override(filter)
	}

	err := f.db.Create(filter).Error
	if err != nil {
		f.t.Fatalf("FixtureBuilder.CreateFilter failed: %v", err)
	}

	return filter
}

// CreatePoll creates a test poll.
func (f *FixtureBuilder) CreatePoll(overrides ...func(*models.Poll)) *models.Poll {
	f.t.Helper()

	now := time.Now()
	expiresAt := now.Add(24 * time.Hour)
	poll := &models.Poll{
		Multiple:  false,
		ExpiresAt: &expiresAt,
		CreatedAt: &now,
		UpdatedAt: &now,
	}

	for _, override := range overrides {
		override(poll)
	}

	err := f.db.Create(poll).Error
	if err != nil {
		f.t.Fatalf("FixtureBuilder.CreatePoll failed: %v", err)
	}

	return poll
}

// CreateAttachment creates a test attachment.
func (f *FixtureBuilder) CreateAttachment(overrides ...func(*models.Attachment)) *models.Attachment {
	f.t.Helper()

	now := time.Now()
	attachment := &models.Attachment{
		Type:        uint(models.AttachmentTypeImage),
		SavePath:    "/test/image.png",
		Description: "Test attachment",
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}

	for _, override := range overrides {
		override(attachment)
	}

	err := f.db.Create(attachment).Error
	if err != nil {
		f.t.Fatalf("FixtureBuilder.CreateAttachment failed: %v", err)
	}

	return attachment
}

// CreateNotification creates a test notification.
func (f *FixtureBuilder) CreateNotification(accountId uint64, overrides ...func(*models.Notification)) *models.Notification {
	f.t.Helper()

	now := time.Now()
	notification := &models.Notification{
		AccountId: accountId,
		Type:      uint8(models.NotificationTypeFavourite),
		CreatedAt: &now,
		UpdatedAt: &now,
	}

	for _, override := range overrides {
		override(notification)
	}

	err := f.db.Create(notification).Error
	if err != nil {
		f.t.Fatalf("FixtureBuilder.CreateNotification failed: %v", err)
	}

	return notification
}

// FullTestSetup creates a complete test setup with user, account, and status.
func (f *FixtureBuilder) FullTestSetup() (user *models.User, account *models.Account, status *models.Status) {
	f.t.Helper()
	user = f.CreateUser(func(u *models.User) {
		u.UserName = "fulltest"
		u.EmailHash = "fulltest_hash"
	})

	account = f.CreateAccount(user, func(a *models.Account) {
		a.Username = "fulltest"
		a.DisplayName = "Full Test User"
	})

	status = f.CreateStatus(account)

	return user, account, status
}

// RandomString generates a random string of the specified length.
// Uses utils.GetRandomString (crypto/rand) to avoid duplicating logic.
func RandomString(length int) string {
	return utils.GetRandomString(length)
}

// PtrString returns a pointer to the string.
func PtrString(s string) *string {
	return new(s)
}

// PtrInt returns a pointer to the int.
func PtrInt(i int) *int {
	return new(i)
}

// PtrInt64 returns a pointer to the int64.
func PtrInt64(i int64) *int64 {
	return new(i)
}

// PtrTime returns a pointer to the time.
func PtrTime(t time.Time) *time.Time {
	return new(t)
}
