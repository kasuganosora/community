package testutil

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

// TestAccount is a simplified account model for testing
// Note: sqlexec has issues with bool types, using int instead
type TestAccount struct {
	Id          uint64     `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId      uint64     `json:"-" gorm:"not null;index"`
	Username    string     `json:"username" gorm:"not null;type:varchar(64)"`
	Domain      *string    `json:"domain" gorm:"type:varchar(128);index"`
	DisplayName string     `json:"display_name" gorm:"type:varchar(128)"`
	Locked      int        `json:"locked" gorm:"default:0"`
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
		Locked:      0,
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}
	result := testDB.GormDB.Create(&account)
	assert.NoError(t, result.Error)

	// Find by username
	var foundAccount TestAccount
	result = testDB.GormDB.Where("username = ?", "testuser").First(&foundAccount)
	assert.NoError(t, result.Error)
	assert.Equal(t, "testuser", foundAccount.Username)
	assert.Equal(t, "Test User", foundAccount.DisplayName)
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
		Locked:      0,
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}
	testDB.GormDB.Create(&account)

	// Update with WHERE condition
	result := testDB.GormDB.Model(&TestAccount{}).Where("username = ?", "updatetest").Update("display_name", "After Update")
	assert.NoError(t, result.Error)

	// Verify update
	var updatedAccount TestAccount
	testDB.GormDB.Where("username = ?", "updatetest").First(&updatedAccount)
	assert.Equal(t, "After Update", updatedAccount.DisplayName)
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
		Locked:      0,
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}
	testDB.GormDB.Create(&account)

	// Delete with WHERE condition
	result := testDB.GormDB.Where("username = ?", "deletetest").Delete(&TestAccount{})
	assert.NoError(t, result.Error)

	// Verify deleted - check count
	var count int64
	testDB.GormDB.Model(&TestAccount{}).Where("username = ?", "deletetest").Count(&count)
	assert.Equal(t, int64(0), count)
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
		{UserId: 1, Username: "multi1", DisplayName: "Multi One", Locked: 0, CreatedAt: &now, UpdatedAt: &now},
		{UserId: 1, Username: "multi2", DisplayName: "Multi Two", Locked: 0, CreatedAt: &now, UpdatedAt: &now},
		{UserId: 2, Username: "multi3", DisplayName: "Multi Three", Locked: 0, CreatedAt: &now, UpdatedAt: &now},
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

// ============================================================================
// Known sqlexec issues (documented for reference):
// 1. Auto-increment ID not returned after Create
// 2. bool type causes scan error: "couldn't convert 0 (float64) into type bool"
// 3. UPDATE requires WHERE condition (no update all)
// 4. LIKE operator returns empty results
// ============================================================================
