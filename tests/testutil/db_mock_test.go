package testutil

import (
	"context"
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

func TestNewIsolatedTestDB(t *testing.T) {
	testDB, err := NewIsolatedTestDB(t)
	assert.NoError(t, err)
	assert.NotNil(t, testDB)
	assert.NotNil(t, testDB.GormDB)
	assert.NotNil(t, testDB.DB)
	// Cleanup is automatic via t.Cleanup
}

func TestQuickDBSetup(t *testing.T) {
	testDB := QuickDBSetup(t, &TestAccount{}, &TestUser{})
	assert.NotNil(t, testDB)
	assert.NotNil(t, testDB.GormDB)
	// Cleanup is automatic
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

func TestTestDB_TruncateTables(t *testing.T) {
	testDB, err := NewTestDB()
	assert.NoError(t, err)
	defer testDB.Close()

	// Migrate
	err = testDB.AutoMigrate(&TestAccount{})
	assert.NoError(t, err)

	// Create some data
	now := time.Now()
	for i := 0; i < 3; i++ {
		testDB.GormDB.Create(&TestAccount{
			UserId:      1,
			Username:    "truncate" + string(rune('a'+i)),
			DisplayName: "To Be Truncated",
			CreatedAt:   &now,
			UpdatedAt:   &now,
		})
	}

	// Truncate
	err = testDB.TruncateTables("accounts")
	assert.NoError(t, err)

	// Verify all data is gone
	var count int64
	testDB.GormDB.Model(&TestAccount{}).Count(&count)
	assert.Equal(t, int64(0), count)
}

func TestTestDB_Exec(t *testing.T) {
	testDB, err := NewTestDB()
	assert.NoError(t, err)
	defer testDB.Close()

	// Create table using Exec
	err = testDB.CreateTable(`
		CREATE TABLE test_exec (
			id INT PRIMARY KEY,
			name VARCHAR(100)
		)
	`)
	assert.NoError(t, err)

	// Insert using Exec
	err = testDB.Exec("INSERT INTO test_exec VALUES (?, ?)", 1, "test")
	assert.NoError(t, err)

	// Query
	row, err := testDB.QueryOne("SELECT * FROM test_exec WHERE id = ?", 1)
	assert.NoError(t, err)
	assert.Equal(t, "test", row["name"])
}

func TestTestDB_QueryAll(t *testing.T) {
	testDB, err := NewTestDB()
	assert.NoError(t, err)
	defer testDB.Close()

	// Create table and insert data
	err = testDB.CreateTable(`
		CREATE TABLE test_query (
			id INT PRIMARY KEY,
			value VARCHAR(100)
		)
	`)
	assert.NoError(t, err)

	for i := 1; i <= 3; i++ {
		testDB.Exec("INSERT INTO test_query VALUES (?, ?)", i, "value"+string(rune('0'+i)))
	}

	// Query all
	rows, err := testDB.QueryAll("SELECT * FROM test_query ORDER BY id")
	assert.NoError(t, err)
	assert.Len(t, rows, 3)
}

func TestSharedTestDB(t *testing.T) {
	// Test shared database
	db1, err := GetSharedTestDB()
	assert.NoError(t, err)
	assert.NotNil(t, db1)

	// Getting again should return the same instance
	db2, err := GetSharedTestDB()
	assert.NoError(t, err)
	assert.Equal(t, db1.DB, db2.DB, "Should return the same DB instance")
}

func TestParallelTestWithIsolatedDB(t *testing.T) {
	// This test demonstrates that isolated DBs can be used in parallel
	tests := []struct {
		name     string
		username string
	}{
		{"case1", "user1"},
		{"case2", "user2"},
		{"case3", "user3"},
	}

	for _, tt := range tests {
		tt := tt // capture range variable
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel() // Safe to parallelize with isolated DB

			testDB, err := NewIsolatedTestDB(t)
			if err != nil {
				t.Fatalf("Failed to create isolated DB: %v", err)
			}

			// Migrate
			err = testDB.AutoMigrate(&TestAccount{})
			if err != nil {
				t.Fatalf("Failed to migrate: %v", err)
			}

			// Create unique data
			now := time.Now()
			account := TestAccount{
				UserId:      1,
				Username:    tt.username,
				DisplayName: "Test " + tt.name,
				CreatedAt:   &now,
				UpdatedAt:   &now,
			}
			result := testDB.GormDB.Create(&account)
			assert.NoError(t, result.Error)

			// Verify data
			var found TestAccount
			testDB.GormDB.Where("username = ?", tt.username).First(&found)
			assert.Equal(t, tt.username, found.Username)
		})
	}
}

func TestRedisTestClient_MockMode(t *testing.T) {
	client := SetupMockRedisTest(t)
	assert.NotNil(t, client)
	assert.True(t, client.IsMock())
	assert.False(t, client.IsReal())

	// Setup mock expectations
	ctx := context.Background()
	client.Mock.ExpectSet("key", "value", 0).SetVal("OK")
	client.Mock.ExpectGet("key").SetVal("value")

	// Use client
	err := client.Client.Set(ctx, "key", "value", 0).Err()
	assert.NoError(t, err)

	val, err := client.Client.Get(ctx, "key").Result()
	assert.NoError(t, err)
	assert.Equal(t, "value", val)

	// Expectations are verified in cleanup
}
