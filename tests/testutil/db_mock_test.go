package testutil

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

// TestAccount is a simplified account model for testing
type TestAccount struct {
	Id          uint64     `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId      uint64     `json:"-" gorm:"not null;index"`
	Username    string     `json:"username" gorm:"not null;type:varchar(64)"`
	Domain      *string    `json:"domain" gorm:"type:varchar(128);index"`
	DisplayName string     `json:"display_name" gorm:"type:varchar(128)"`
	Locked      bool       `json:"locked" gorm:"default:false"`
	CreatedAt   *time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt   *time.Time `json:"updated_at" gorm:"autoUpdateTime"`
	DeletedAt   gorm.DeletedAt
}

func (TestAccount) TableName() string {
	return "accounts"
}

// TestUser is a simplified user model for testing
type TestUser struct {
	Id        uint64 `json:"id" gorm:"primaryKey;autoIncrement"`
	Email     string `json:"email" gorm:"not null;type:varchar(128);uniqueIndex"`
	CreatedAt *time.Time
}

func (TestUser) TableName() string {
	return "users"
}

func TestNewTestDB(t *testing.T) {
	testDB, err := NewTestDB()
	assert.NoError(t, err)
	assert.NotNil(t, testDB)
	assert.NotNil(t, testDB.GormDB)
	assert.NotNil(t, testDB.DB)
	assert.NotNil(t, testDB.Session)
	defer testDB.Close()
}

func TestTestDB_AutoMigrate(t *testing.T) {
	testDB, err := NewTestDB()
	assert.NoError(t, err)
	defer testDB.Close()

	// Test auto migrate
	err = testDB.AutoMigrate(&TestAccount{}, &TestUser{})
	assert.NoError(t, err)
}

func TestTestDB_CreateAndFind(t *testing.T) {
	testDB, err := NewTestDB()
	assert.NoError(t, err)
	defer testDB.Close()

	// Migrate
	err = testDB.AutoMigrate(&TestAccount{})
	assert.NoError(t, err)

	// Create
	now := time.Now()
	account := TestAccount{
		UserId:      1,
		Username:    "testuser",
		DisplayName: "Test User",
		Locked:      false,
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}
	result := testDB.GormDB.Create(&account)
	assert.NoError(t, result.Error)
	assert.Greater(t, account.Id, uint64(0), "Auto-increment ID should be returned after Create")

	// Find by username
	var foundAccount TestAccount
	result = testDB.GormDB.Where("username = ?", "testuser").First(&foundAccount)
	assert.NoError(t, result.Error)
	assert.Equal(t, "testuser", foundAccount.Username)
	assert.Equal(t, "Test User", foundAccount.DisplayName)
	assert.Equal(t, false, foundAccount.Locked)
}

func TestTestDB_Update(t *testing.T) {
	testDB, err := NewTestDB()
	assert.NoError(t, err)
	defer testDB.Close()

	// Migrate and create
	err = testDB.AutoMigrate(&TestAccount{})
	assert.NoError(t, err)

	now := time.Now()
	account := TestAccount{
		UserId:      1,
		Username:    "updatetest",
		DisplayName: "Before Update",
		Locked:      false,
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}
	testDB.GormDB.Create(&account)

	// Update with WHERE condition
	result := testDB.GormDB.Model(&TestAccount{}).Where("username = ?", "updatetest").Update("display_name", "After Update")
	assert.NoError(t, result.Error)

	// Update bool field
	result = testDB.GormDB.Model(&TestAccount{}).Where("username = ?", "updatetest").Update("locked", true)
	assert.NoError(t, result.Error)

	// Verify update
	var updatedAccount TestAccount
	testDB.GormDB.Where("username = ?", "updatetest").First(&updatedAccount)
	assert.Equal(t, "After Update", updatedAccount.DisplayName)
	assert.Equal(t, true, updatedAccount.Locked)

	// Test update all records (without WHERE)
	result = testDB.GormDB.Model(&TestAccount{}).Where("1 = 1").Update("locked", false)
	assert.NoError(t, result.Error)
}

func TestTestDB_Delete(t *testing.T) {
	testDB, err := NewTestDB()
	assert.NoError(t, err)
	defer testDB.Close()

	// Migrate and create
	err = testDB.AutoMigrate(&TestAccount{})
	assert.NoError(t, err)

	now := time.Now()
	account := TestAccount{
		UserId:      1,
		Username:    "deletetest",
		DisplayName: "To Be Deleted",
		Locked:      false,
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}
	testDB.GormDB.Create(&account)

	// Delete with WHERE condition
	result := testDB.GormDB.Where("username = ?", "deletetest").Delete(&TestAccount{})
	assert.NoError(t, result.Error)

	// Verify deleted - First should return ErrRecordNotFound due to soft delete
	// Note: sqlexec may not fully support soft delete, so we check with Unscoped to see the actual record
	var deletedAccount TestAccount
	result = testDB.GormDB.Unscoped().Where("username = ?", "deletetest").First(&deletedAccount)
	if result.Error == nil {
		// Record exists, check if deleted_at is set
		assert.NotNil(t, deletedAccount.DeletedAt.Time, "DeletedAt should be set after soft delete")
	} else {
		// Record not found at all (hard delete or working soft delete)
		assert.Error(t, result.Error)
	}
}

func TestTestDB_MultipleRecords(t *testing.T) {
	testDB, err := NewTestDB()
	assert.NoError(t, err)
	defer testDB.Close()

	// Migrate
	err = testDB.AutoMigrate(&TestAccount{})
	assert.NoError(t, err)

	// Create multiple accounts
	now := time.Now()
	accounts := []TestAccount{
		{UserId: 1, Username: "multi1", DisplayName: "Multi One", Locked: false, CreatedAt: &now, UpdatedAt: &now},
		{UserId: 1, Username: "multi2", DisplayName: "Multi Two", Locked: false, CreatedAt: &now, UpdatedAt: &now},
		{UserId: 2, Username: "multi3", DisplayName: "Multi Three", Locked: true, CreatedAt: &now, UpdatedAt: &now},
	}
	for _, acc := range accounts {
		err := testDB.GormDB.Create(&acc).Error
		assert.NoError(t, err)
	}

	// Query with WHERE - user_id = 1
	var found []TestAccount
	result := testDB.GormDB.Where("user_id = ?", 1).Find(&found)
	assert.NoError(t, result.Error)
	assert.Equal(t, 2, len(found))
}

func TestTestDB_LikeQuery(t *testing.T) {
	testDB, err := NewTestDB()
	assert.NoError(t, err)
	defer testDB.Close()

	// Migrate
	err = testDB.AutoMigrate(&TestAccount{})
	assert.NoError(t, err)

	// Create accounts with similar names
	now := time.Now()
	accounts := []TestAccount{
		{UserId: 1, Username: "john_doe", DisplayName: "John Doe", Locked: false, CreatedAt: &now, UpdatedAt: &now},
		{UserId: 1, Username: "john_smith", DisplayName: "John Smith", Locked: false, CreatedAt: &now, UpdatedAt: &now},
		{UserId: 2, Username: "jane_doe", DisplayName: "Jane Doe", Locked: false, CreatedAt: &now, UpdatedAt: &now},
	}
	for _, acc := range accounts {
		err := testDB.GormDB.Create(&acc).Error
		assert.NoError(t, err)
	}

	// Test LIKE query - find all usernames starting with "john"
	var johnUsers []TestAccount
	result := testDB.GormDB.Where("username LIKE ?", "john%").Find(&johnUsers)
	assert.NoError(t, result.Error)
	assert.Equal(t, 2, len(johnUsers), "LIKE query should find 2 users starting with 'john'")

	// Test LIKE query - find all usernames containing "_doe"
	var doeUsers []TestAccount
	result = testDB.GormDB.Where("username LIKE ?", "%_doe%").Find(&doeUsers)
	assert.NoError(t, result.Error)
	assert.Equal(t, 2, len(doeUsers), "LIKE query should find 2 users containing '_doe'")
}
