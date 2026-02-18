package testutil

import (
	"time"

	"github.com/google/uuid"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// FixtureBuilder helps create test data
type FixtureBuilder struct {
	db *gorm.DB
}

// NewFixtureBuilder creates a new fixture builder
func NewFixtureBuilder(db *gorm.DB) *FixtureBuilder {
	return &FixtureBuilder{db: db}
}

// CreateUser creates a test user
func (f *FixtureBuilder) CreateUser(overrides ...func(*models.User)) *models.User {
	now := time.Now()
	user := &models.User{
		UUID:           uuid.New().String(),
		UserName:       "testuser",
		EmailHash:      "test_hash_" + RandomString(8),
		CreatedAt:      &now,
		UpdatedAt:      &now,
		Status:         models.UserStatusNormal,
		IsMailVerified: true,
	}

	for _, override := range overrides {
		override(user)
	}

	if err := f.db.Create(user).Error; err != nil {
		return nil
	}

	return user
}

// CreateAccount creates a test account
func (f *FixtureBuilder) CreateAccount(user *models.User, overrides ...func(*models.Account)) *models.Account {
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

	// Set domain to nil for local accounts
	if account.Domain == nil {
		account.Domain = nil
	}

	if err := f.db.Create(account).Error; err != nil {
		return nil
	}

	return account
}

// CreateStatus creates a test status
func (f *FixtureBuilder) CreateStatus(account *models.Account, overrides ...func(*models.Status)) *models.Status {
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

	if err := f.db.Create(status).Error; err != nil {
		return nil
	}

	return status
}

// CreateFollow creates a follow relationship
func (f *FixtureBuilder) CreateFollow(follower, following *models.Account, overrides ...func(*models.Follow)) *models.Follow {
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

	if err := f.db.Create(follow).Error; err != nil {
		return nil
	}

	return follow
}

// CreateBlock creates a block relationship
func (f *FixtureBuilder) CreateBlock(blocker, blocked *models.Account, overrides ...func(*models.Block)) *models.Block {
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

	if err := f.db.Create(block).Error; err != nil {
		return nil
	}

	return block
}

// CreateAuthToken creates a test auth token
func (f *FixtureBuilder) CreateAuthToken(userId uint64, overrides ...func(*models.AuthToken)) *models.AuthToken {
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

	if err := f.db.Create(token).Error; err != nil {
		return nil
	}

	return token
}

// CreateRole creates a test role
func (f *FixtureBuilder) CreateRole(overrides ...func(*models.Role)) *models.Role {
	role := &models.Role{
		Name:        "Test Role",
		Color:       "#FF0000",
		Highlighted: false,
		Permissions: 0,
	}

	for _, override := range overrides {
		override(role)
	}

	if err := f.db.Create(role).Error; err != nil {
		return nil
	}

	return role
}

// CreateFilter creates a test filter
func (f *FixtureBuilder) CreateFilter(accountId uint64, overrides ...func(*models.Filter)) *models.Filter {
	now := time.Now()
	filter := &models.Filter{
		AccountId:    accountId,
		Title:        "Test Filter",
		Context:      []string{"home"},
		ExpiresAt:    nil,
		FilterAction: uint8(models.FilterActionWarn),
		CratedAt:     &now,
		UpdatedAt:    &now,
	}

	for _, override := range overrides {
		override(filter)
	}

	if err := f.db.Create(filter).Error; err != nil {
		return nil
	}

	return filter
}

// CreatePoll creates a test poll
func (f *FixtureBuilder) CreatePoll(overrides ...func(*models.Poll)) *models.Poll {
	now := time.Now()
	expiresAt := now.Add(24 * time.Hour)
	poll := &models.Poll{
		Multiple:  false,
		ExpiresAt: &expiresAt,
		CratedAt:  &now,
		UpdatedAt: &now,
	}

	for _, override := range overrides {
		override(poll)
	}

	if err := f.db.Create(poll).Error; err != nil {
		return nil
	}

	return poll
}

// CreateAttachment creates a test attachment
func (f *FixtureBuilder) CreateAttachment(overrides ...func(*models.Attachment)) *models.Attachment {
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

	if err := f.db.Create(attachment).Error; err != nil {
		return nil
	}

	return attachment
}

// CreateNotification creates a test notification
func (f *FixtureBuilder) CreateNotification(accountId uint64, overrides ...func(*models.Notification)) *models.Notification {
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

	if err := f.db.Create(notification).Error; err != nil {
		return nil
	}

	return notification
}

// FullTestSetup creates a complete test setup with user, account, and status
func (f *FixtureBuilder) FullTestSetup() (user *models.User, account *models.Account, status *models.Status) {
	user = f.CreateUser(func(u *models.User) {
		u.UserName = "fulltest"
		u.EmailHash = "fulltest_hash"
	})
	if user == nil {
		return nil, nil, nil
	}

	account = f.CreateAccount(user, func(a *models.Account) {
		a.Username = "fulltest"
		a.DisplayName = "Full Test User"
	})
	if account == nil {
		return nil, nil, nil
	}

	status = f.CreateStatus(account)

	return user, account, status
}

// RandomString generates a random string of the specified length
// Uses crypto/rand for security
func RandomString(length int) string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, length)
	for i := 0; i < length; i++ {
		// Simple pseudo-random for fixtures, not for security
		result[i] = chars[i%len(chars)]
	}
	return string(result)
}

// PtrString returns a pointer to the string
func PtrString(s string) *string {
	return &s
}

// PtrInt returns a pointer to the int
func PtrInt(i int) *int {
	return &i
}

// PtrInt64 returns a pointer to the int64
func PtrInt64(i int64) *int64 {
	return &i
}

// PtrTime returns a pointer to the time
func PtrTime(t time.Time) *time.Time {
	return &t
}
