package activitypub

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupRelayTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to connect to database: %v", err)
	}

	err = db.AutoMigrate(&models.Relay{}, &Delivery{})
	if err != nil {
		t.Fatalf("Failed to migrate database: %v", err)
	}

	return db
}

// TestRelayState tests the relay state methods
func TestRelayState(t *testing.T) {
	tests := []struct {
		state    models.RelayState
		expected string
	}{
		{models.RelayStateIdle, "idle"},
		{models.RelayStatePending, "pending"},
		{models.RelayStateAccepted, "accepted"},
		{models.RelayStateRejected, "rejected"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			relay := &models.Relay{State: tt.state}
			switch tt.expected {
			case "idle":
				assert.False(t, relay.IsEnabled())
				assert.False(t, relay.IsPending())
			case "pending":
				assert.False(t, relay.IsEnabled())
				assert.True(t, relay.IsPending())
			case "accepted":
				assert.True(t, relay.IsEnabled())
				assert.False(t, relay.IsPending())
			case "rejected":
				assert.False(t, relay.IsEnabled())
				assert.False(t, relay.IsPending())
			}
		})
	}
}

// TestRelayCRUD tests basic CRUD operations for relays
func TestRelayCRUD(t *testing.T) {
	db := setupRelayTestDB(t)

	// Test Create
	relay, err := models.CreateRelay(db, "https://relay.example.com/inbox")
	assert.NoError(t, err)
	assert.NotNil(t, relay)
	assert.Equal(t, "https://relay.example.com/inbox", relay.InboxURL)
	assert.Equal(t, models.RelayStateIdle, relay.State)

	// Test Get by ID
	fetched, err := models.GetRelayByID(db, relay.ID)
	assert.NoError(t, err)
	assert.Equal(t, relay.InboxURL, fetched.InboxURL)

	// Test Get by InboxURL
	fetchedByURL, err := models.GetRelayByInboxURL(db, "https://relay.example.com/inbox")
	assert.NoError(t, err)
	assert.Equal(t, relay.ID, fetchedByURL.ID)

	// Test Update State
	fetched.SetState(models.RelayStatePending)
	db.Save(fetched)
	assert.Equal(t, models.RelayStatePending, fetched.State)

	// Test Delete
	err = models.DeleteRelay(db, relay.ID)
	assert.NoError(t, err)

	// Verify deleted
	_, err = models.GetRelayByID(db, relay.ID)
	assert.Error(t, err)
}

// TestGetEnabledRelays tests getting enabled relays
func TestGetEnabledRelays(t *testing.T) {
	db := setupRelayTestDB(t)

	// Create multiple relays
	relay1, _ := models.CreateRelay(db, "https://relay1.example.com/inbox")
	relay2, _ := models.CreateRelay(db, "https://relay2.example.com/inbox")
	relay3, _ := models.CreateRelay(db, "https://relay3.example.com/inbox")

	// Set states
	relay1.State = models.RelayStateAccepted
	db.Save(relay1)
	relay2.State = models.RelayStatePending
	db.Save(relay2)
	relay3.State = models.RelayStateAccepted
	db.Save(relay3)

	// Get enabled relays
	enabled, err := models.GetEnabledRelays(db)
	assert.NoError(t, err)
	assert.Len(t, enabled, 2)

	// Get enabled inbox URLs
	inboxes, err := models.GetEnabledRelayInboxes(db)
	assert.NoError(t, err)
	assert.Len(t, inboxes, 2)
	assert.Contains(t, inboxes, "https://relay1.example.com/inbox")
	assert.Contains(t, inboxes, "https://relay3.example.com/inbox")
}

// TestGetRelayByFollowActivityID tests getting relay by follow activity ID
func TestGetRelayByFollowActivityID(t *testing.T) {
	db := setupRelayTestDB(t)

	relay, _ := models.CreateRelay(db, "https://relay.example.com/inbox")
	relay.FollowActivityID = "https://example.com/activities/follow/123"
	db.Save(relay)

	// Test Get by FollowActivityID
	fetched, err := models.GetRelayByFollowActivityID(db, "https://example.com/activities/follow/123")
	assert.NoError(t, err)
	assert.Equal(t, relay.ID, fetched.ID)

	// Test non-existent
	_, err = models.GetRelayByFollowActivityID(db, "https://example.com/activities/follow/999")
	assert.Error(t, err)
}

// TestSerializeRelay tests relay serialization
func TestSerializeRelay(t *testing.T) {
	now := time.Now()
	relay := &models.Relay{
		ID:               1,
		InboxURL:         "https://relay.example.com/inbox",
		FollowActivityID: "https://example.com/activities/follow/123",
		State:            models.RelayStateAccepted,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	serialized := SerializeRelay(relay)
	assert.Equal(t, uint64(1), serialized.ID)
	assert.Equal(t, "https://relay.example.com/inbox", serialized.InboxURL)
	assert.Equal(t, "accepted", serialized.State)
}

// TestSerializeRelays tests serializing multiple relays
func TestSerializeRelays(t *testing.T) {
	relays := []models.Relay{
		{ID: 1, InboxURL: "https://relay1.example.com/inbox", State: models.RelayStateAccepted},
		{ID: 2, InboxURL: "https://relay2.example.com/inbox", State: models.RelayStatePending},
	}

	serialized := SerializeRelays(relays)
	assert.Len(t, serialized, 2)
	assert.Equal(t, "accepted", serialized[0].State)
	assert.Equal(t, "pending", serialized[1].State)
}

// TestRelayServiceWithMockDB tests RelayService with injected DB
func TestRelayServiceWithMockDB(t *testing.T) {
	db := setupRelayTestDB(t)
	service := &RelayService{db: db}

	// Test CreateRelay
	relay, err := service.CreateRelay("https://relay-test.example.com/inbox")
	assert.NoError(t, err)
	assert.Equal(t, "https://relay-test.example.com/inbox", relay.InboxURL)

	// Test duplicate
	_, err = service.CreateRelay("https://relay-test.example.com/inbox")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")

	// Test GetAllRelays
	relays, err := service.GetAllRelays()
	assert.NoError(t, err)
	assert.Len(t, relays, 1)

	// Test GetRelayByID
	fetched, err := service.GetRelayByID(relay.ID)
	assert.NoError(t, err)
	assert.Equal(t, relay.InboxURL, fetched.InboxURL)

	// Test GetRelayByID non-existent
	_, err = service.GetRelayByID(9999)
	assert.Error(t, err)
}

// TestRelayServiceAcceptReject tests AcceptRelay and RejectRelay
func TestRelayServiceAcceptReject(t *testing.T) {
	db := setupRelayTestDB(t)
	service := &RelayService{db: db}

	// Create relay with follow activity ID
	relay, _ := service.CreateRelay("https://relay-accept.example.com/inbox")
	relay.FollowActivityID = "https://example.com/activities/follow/123"
	relay.State = models.RelayStatePending
	db.Save(relay)

	// Test AcceptRelay
	err := service.AcceptRelay(relay.FollowActivityID)
	assert.NoError(t, err)

	// Verify state changed
	accepted, _ := service.GetRelayByID(relay.ID)
	assert.Equal(t, models.RelayStateAccepted, accepted.State)

	// Test RejectRelay for another relay
	relay2, _ := service.CreateRelay("https://relay-reject.example.com/inbox")
	relay2.FollowActivityID = "https://example.com/activities/follow/456"
	relay2.State = models.RelayStatePending
	db.Save(relay2)

	err = service.RejectRelay(relay2.FollowActivityID)
	assert.NoError(t, err)

	// Verify state changed
	rejected, _ := service.GetRelayByID(relay2.ID)
	assert.Equal(t, models.RelayStateRejected, rejected.State)

	// Test AcceptRelay with non-existent activity ID (should return nil, no error)
	err = service.AcceptRelay("https://non-existent/activity")
	assert.NoError(t, err)
}

// TestRelayServiceDelete tests DeleteRelay for idle relay
func TestRelayServiceDeleteIdleRelay(t *testing.T) {
	db := setupRelayTestDB(t)
	service := &RelayService{db: db}

	// Create idle relay
	relay, _ := service.CreateRelay("https://relay-delete.example.com/inbox")

	// Test DeleteRelay
	err := service.DeleteRelay(relay.ID)
	assert.NoError(t, err)

	// Verify deleted
	_, err = service.GetRelayByID(relay.ID)
	assert.Error(t, err)

	// Test delete non-existent
	err = service.DeleteRelay(9999)
	assert.Error(t, err)
}

// TestRelayServiceInboxes tests GetRelayInboxes and IsRelayInbox
func TestRelayServiceInboxes(t *testing.T) {
	db := setupRelayTestDB(t)
	service := &RelayService{db: db}

	// Create relays
	relay1, _ := service.CreateRelay("https://relay-inbox1.example.com/inbox")
	_ = relay1

	// Create second relay but don't use the variable directly
	service.CreateRelay("https://relay-inbox2.example.com/inbox")

	// Enable relay1
	relay1.State = models.RelayStateAccepted
	db.Save(relay1)

	// Test GetRelayInboxes
	inboxes, err := service.GetRelayInboxes()
	assert.NoError(t, err)
	assert.Len(t, inboxes, 1)
	assert.Contains(t, inboxes, "https://relay-inbox1.example.com/inbox")

	// Test IsRelayInbox
	assert.True(t, service.IsRelayInbox("https://relay-inbox1.example.com/inbox"))
	assert.False(t, service.IsRelayInbox("https://relay-inbox2.example.com/inbox"))
	assert.False(t, service.IsRelayInbox("https://non-existent.example.com/inbox"))
}

// TestStatusReachFinderIsPublicVisibility tests isPublicVisibility
func TestStatusReachFinderIsPublicVisibility(t *testing.T) {
	tests := []struct {
		visibility uint8
		expected   bool
	}{
		{models.StatusVisibilityPublic, true},
		{models.StatusVisibilityUnlisted, true},
		{models.StatusVisibilityPrivate, false},
		{models.StatusVisibilityDirect, false},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			status := &models.Status{Visibility: tt.visibility}
			finder := &StatusReachFinder{status: status}
			assert.Equal(t, tt.expected, finder.isPublicVisibility())
		})
	}
}

// TestRelayContext tests relay context constant
func TestRelayContext(t *testing.T) {
	assert.Contains(t, RelayContext, "https://www.w3.org/ns/activitystreams")
}

// TestDeliveryModel tests Delivery model
func TestDeliveryModel(t *testing.T) {
	db := setupRelayTestDB(t)

	delivery := &Delivery{
		ActivityID:  "https://example.com/activities/123",
		TargetInbox: "https://target.example.com/inbox",
		Payload:     `{"type":"Follow"}`,
		Status:      DeliveryStatusPending,
		Retries:     0,
	}

	err := db.Create(delivery).Error
	assert.NoError(t, err)
	assert.NotZero(t, delivery.ID)

	// Test status constants
	assert.Equal(t, DeliveryStatus("pending"), DeliveryStatusPending)
	assert.Equal(t, DeliveryStatus("delivered"), DeliveryStatusDelivered)
	assert.Equal(t, DeliveryStatus("failed"), DeliveryStatusFailed)
}

// TestSerializeRelayAllStates tests serialization of all relay states
func TestSerializeRelayAllStates(t *testing.T) {
	states := []struct {
		state    models.RelayState
		expected string
	}{
		{models.RelayStateIdle, "idle"},
		{models.RelayStatePending, "pending"},
		{models.RelayStateAccepted, "accepted"},
		{models.RelayStateRejected, "rejected"},
	}

	for _, tt := range states {
		t.Run(tt.expected, func(t *testing.T) {
			relay := &models.Relay{
				ID:        1,
				State:     tt.state,
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			}
			serialized := SerializeRelay(relay)
			assert.Equal(t, tt.expected, serialized.State)
		})
	}
}

// TestGetAllRelays tests GetAllRelays ordering
func TestGetAllRelays(t *testing.T) {
	db := setupRelayTestDB(t)

	// Create relays with slight delay to ensure different timestamps
	relay1, _ := models.CreateRelay(db, "https://relay1.example.com/inbox")
	time.Sleep(10 * time.Millisecond)
	relay2, _ := models.CreateRelay(db, "https://relay2.example.com/inbox")
	time.Sleep(10 * time.Millisecond)
	relay3, _ := models.CreateRelay(db, "https://relay3.example.com/inbox")

	relays, err := models.GetAllRelays(db)
	assert.NoError(t, err)
	assert.Len(t, relays, 3)

	// Should be ordered by created_at DESC (newest first)
	assert.Equal(t, relay3.ID, relays[0].ID)
	assert.Equal(t, relay2.ID, relays[1].ID)
	assert.Equal(t, relay1.ID, relays[2].ID)
}

// TestRelayTableName tests the TableName method
func TestRelayTableName(t *testing.T) {
	relay := models.Relay{}
	assert.Equal(t, "relays", relay.TableName())
}

// TestDeliveryTableName tests Delivery TableName method
func TestDeliveryTableName(t *testing.T) {
	delivery := Delivery{}
	assert.Equal(t, "activitypub_deliveries", delivery.TableName())
}

// TestRelaySetState tests SetState method
func TestRelaySetState(t *testing.T) {
	relay := &models.Relay{}
	relay.SetState(models.RelayStateAccepted)
	assert.Equal(t, models.RelayStateAccepted, relay.State)

	relay.SetState(models.RelayStatePending)
	assert.Equal(t, models.RelayStatePending, relay.State)
}

// TestDeliveryStatusValues tests DeliveryStatus string values
func TestDeliveryStatusValues(t *testing.T) {
	assert.Equal(t, "pending", string(DeliveryStatusPending))
	assert.Equal(t, "processing", string(DeliveryStatusProcessing))
	assert.Equal(t, "delivered", string(DeliveryStatusDelivered))
	assert.Equal(t, "failed", string(DeliveryStatusFailed))
}

// TestRelaySerializerFields tests RelaySerializer field JSON tags
func TestRelaySerializerFields(t *testing.T) {
	now := time.Now()
	serializer := RelaySerializer{
		ID:               123,
		InboxURL:         "https://test.example.com/inbox",
		FollowActivityID: "https://example.com/activities/456",
		State:            "accepted",
		CreatedAt:        now.Format(time.RFC3339),
		UpdatedAt:        now.Format(time.RFC3339),
	}

	// Marshal to JSON to verify field names
	data, err := json.Marshal(serializer)
	assert.NoError(t, err)

	var result map[string]interface{}
	json.Unmarshal(data, &result)

	assert.Equal(t, float64(123), result["id"])
	assert.Equal(t, "https://test.example.com/inbox", result["inbox_url"])
	assert.Equal(t, "https://example.com/activities/456", result["follow_activity_id"])
	assert.Equal(t, "accepted", result["state"])
}

// TestGetEnabledRelayInboxesEmpty tests GetEnabledRelayInboxes with no enabled relays
func TestGetEnabledRelayInboxesEmpty(t *testing.T) {
	db := setupRelayTestDB(t)

	// Create only non-accepted relays
	models.CreateRelay(db, "https://relay1.example.com/inbox")
	relay2, _ := models.CreateRelay(db, "https://relay2.example.com/inbox")
	relay2.State = models.RelayStatePending
	db.Save(relay2)

	inboxes, err := models.GetEnabledRelayInboxes(db)
	assert.NoError(t, err)
	assert.Len(t, inboxes, 0)
}

// TestGetRelayByInboxURLNotFound tests GetRelayByInboxURL with non-existent URL
func TestGetRelayByInboxURLNotFound(t *testing.T) {
	db := setupRelayTestDB(t)

	_, err := models.GetRelayByInboxURL(db, "https://non-existent.example.com/inbox")
	assert.Error(t, err)
	assert.Equal(t, gorm.ErrRecordNotFound, err)
}

// TestCreateRelayDuplicate tests creating duplicate relay
func TestCreateRelayDuplicate(t *testing.T) {
	db := setupRelayTestDB(t)

	// Create first relay
	_, err := models.CreateRelay(db, "https://duplicate.example.com/inbox")
	assert.NoError(t, err)

	// Try to create duplicate
	_, err = models.CreateRelay(db, "https://duplicate.example.com/inbox")
	assert.Error(t, err) // Should fail due to unique constraint
}

// TestDeliveryCRUD tests basic Delivery CRUD operations
func TestDeliveryCRUD(t *testing.T) {
	db := setupRelayTestDB(t)

	// Create
	delivery := &Delivery{
		ActivityID:  "https://example.com/activities/create",
		TargetInbox: "https://target.example.com/inbox",
		Payload:     `{"type":"Create"}`,
		Status:      DeliveryStatusPending,
		Retries:     0,
	}
	err := db.Create(delivery).Error
	assert.NoError(t, err)

	// Read
	var fetched Delivery
	err = db.First(&fetched, delivery.ID).Error
	assert.NoError(t, err)
	assert.Equal(t, delivery.ActivityID, fetched.ActivityID)

	// Update
	fetched.Status = DeliveryStatusDelivered
	err = db.Save(&fetched).Error
	assert.NoError(t, err)

	// Verify update
	var updated Delivery
	db.First(&updated, delivery.ID)
	assert.Equal(t, DeliveryStatusDelivered, updated.Status)

	// Delete
	err = db.Delete(&fetched).Error
	assert.NoError(t, err)

	// Verify delete
	var count int64
	db.Model(&Delivery{}).Where("id = ?", delivery.ID).Count(&count)
	assert.Equal(t, int64(0), count)
}
