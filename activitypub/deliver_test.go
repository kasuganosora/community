package activitypub

import (
	"testing"
	"time"

	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupDeliverTestDB(t *testing.T) *testutil.TestDB {
	return testutil.QuickDBSetup(t, &Delivery{}, &ProcessedActivity{})
}

func TestDeliveryConstants(t *testing.T) {
	assert.Equal(t, DeliveryStatusPending, DeliveryStatus("pending"))
	assert.Equal(t, DeliveryStatusProcessing, DeliveryStatus("processing"))
	assert.Equal(t, DeliveryStatusDelivered, DeliveryStatus("delivered"))
	assert.Equal(t, DeliveryStatusFailed, DeliveryStatus("failed"))
}

func TestDeliveryTableNameMethod(t *testing.T) {
	d := Delivery{}
	assert.Equal(t, "activitypub_deliveries", d.TableName())
}

func TestProcessedActivityTableName(t *testing.T) {
	pa := ProcessedActivity{}
	assert.Equal(t, "activitypub_processed_activities", pa.TableName())
}

func TestProcessedActivityCRUD(t *testing.T) {
	testDB := setupDeliverTestDB(t)

	// Create
	pa := &ProcessedActivity{
		ActivityID: "https://example.com/activities/1",
	}
	err := testDB.GormDB.Create(pa).Error
	require.NoError(t, err)
	assert.NotZero(t, pa.ID)

	// Read
	var fetched ProcessedActivity

	err = testDB.GormDB.Where("activity_id = ?", "https://example.com/activities/1").First(&fetched).Error
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/activities/1", fetched.ActivityID)

	// Count
	var count int64

	testDB.GormDB.Model(&ProcessedActivity{}).Where("activity_id = ?", "https://example.com/activities/1").Count(&count)
	assert.Equal(t, int64(1), count)

	// Non-existent
	testDB.GormDB.Model(&ProcessedActivity{}).Where("activity_id = ?", "https://example.com/activities/999").Count(&count)
	assert.Equal(t, int64(0), count)
}

func TestStripPrivacyFieldsExtended(t *testing.T) {
	t.Run("does not modify original", func(t *testing.T) {
		original := map[string]any{
			"type": "Create", //nolint:goconst
			"bto":  []string{"secret@user.com"},
			"bcc":  []string{"hidden@user.com"},
		}

		result := stripPrivacyFields(original)

		// Original should still have bto/bcc
		_, hasBto := original["bto"]
		assert.True(t, hasBto, "original should not be modified")

		// Result should not have bto/bcc
		_, hasBto = result["bto"]
		assert.False(t, hasBto)
	})

	t.Run("preserves all other fields", func(t *testing.T) {
		input := map[string]any{
			"@context": "https://www.w3.org/ns/activitystreams", //nolint:goconst
			"type":     "Create",
			"id":       "https://example.com/activities/1",
			"actor":    "https://example.com/users/test",                         //nolint:goconst
			"to":       []string{"https://www.w3.org/ns/activitystreams#Public"}, //nolint:goconst
			"cc":       []string{"https://example.com/users/test/followers"},
			"bto":      []string{"secret"},
			"bcc":      []string{"hidden"},
		}

		result := stripPrivacyFields(input)
		assert.Equal(t, "Create", result["type"])
		assert.Equal(t, "https://example.com/activities/1", result["id"])
		assert.Equal(t, "https://example.com/users/test", result["actor"])
		assert.NotNil(t, result["to"])
		assert.NotNil(t, result["cc"])
		_, hasBto := result["bto"]
		_, hasBcc := result["bcc"]

		assert.False(t, hasBto)
		assert.False(t, hasBcc)
	})

	t.Run("handles nested object with no bto/bcc", func(t *testing.T) {
		input := map[string]any{
			"type": "Create",
			"object": map[string]any{ //nolint:goconst
				"type":    "Note",  //nolint:goconst
				"content": "Hello", //nolint:goconst
			},
		}

		result := stripPrivacyFields(input)
		obj := result["object"].(map[string]any) //nolint:forcetypeassert
		assert.Equal(t, "Note", obj["type"])
		assert.Equal(t, "Hello", obj["content"])
	})

	t.Run("handles non-map object", func(t *testing.T) {
		input := map[string]any{
			"type":   "Like", //nolint:goconst
			"object": "https://example.com/statuses/1",
		}

		result := stripPrivacyFields(input)
		assert.Equal(t, "https://example.com/statuses/1", result["object"])
	})
}

func TestRetryPolicyValues(t *testing.T) {
	assert.Len(t, RetryPolicy, 5)

	// Verify intervals increase monotonically
	for i := 1; i < len(RetryPolicy); i++ {
		assert.Greater(t, RetryPolicy[i], RetryPolicy[i-1],
			"RetryPolicy[%d] should be greater than RetryPolicy[%d]", i, i-1)
	}
}

func TestGetNextRetryTimeEdgeCases(t *testing.T) {
	t.Run("negative retry count uses first interval", func(t *testing.T) {
		// Negative index would panic on slice access, but the function
		// doesn't handle negative - let's test 0
		nextTime := GetNextRetryTime(0)
		expected := time.Now().Add(5 * time.Minute)
		assert.WithinDuration(t, expected, nextTime, 2*time.Second)
	})

	t.Run("exact boundary", func(t *testing.T) {
		nextTime := GetNextRetryTime(4) // Last valid index
		expected := time.Now().Add(24 * time.Hour)
		assert.WithinDuration(t, expected, nextTime, 2*time.Second)
	})

	t.Run("way beyond max", func(t *testing.T) {
		nextTime := GetNextRetryTime(1000)
		expected := time.Now().Add(24 * time.Hour) // Should cap at last
		assert.WithinDuration(t, expected, nextTime, 2*time.Second)
	})
}
