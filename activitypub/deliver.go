package activitypub

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"maps"
	"sync"
	"time"

	"github.com/kasuganosora/community/db"
)

// DeliveryStatus represents the status of a delivery.
type DeliveryStatus string

const (
	DeliveryStatusPending    DeliveryStatus = "pending"
	DeliveryStatusProcessing DeliveryStatus = "processing"
	DeliveryStatusDelivered  DeliveryStatus = "delivered"
	DeliveryStatusFailed     DeliveryStatus = "failed"
)

// Delivery represents a pending ActivityPub delivery.
type Delivery struct {
	ID          uint64         `gorm:"primaryKey;autoIncrement"`
	ActivityID  string         `gorm:"type:varchar(255);not null;index"`
	TargetInbox string         `gorm:"type:varchar(255);not null;index"`
	Payload     string         `gorm:"type:json;not null"`
	Status      DeliveryStatus `gorm:"type:varchar(20);default:'pending';index"`
	Retries     int            `gorm:"default:0"`
	LastError   string         `gorm:"type:text"`
	NextRetryAt *time.Time     `gorm:"index"`
	CreatedAt   *time.Time     `gorm:"autoCreateTime"`
	UpdatedAt   *time.Time     `gorm:"autoUpdateTime"`
}

func (Delivery) TableName() string {
	return "activitypub_deliveries"
}

// ProcessedActivity tracks already-processed activity IDs for inbox de-duplication.
// Per ActivityPub spec, inbox MUST de-duplicate incoming activities by their ID.
type ProcessedActivity struct {
	ID         uint64     `gorm:"primaryKey;autoIncrement"`
	ActivityID string     `gorm:"type:varchar(255);not null;uniqueIndex"`
	CreatedAt  *time.Time `gorm:"autoCreateTime"`
}

func (ProcessedActivity) TableName() string {
	return "activitypub_processed_activities"
}

// DeliveryQueue manages ActivityPub message delivery.
type DeliveryQueue struct {
	client    *Client
	workers   int
	stopChan  chan struct{}
	waitGroup sync.WaitGroup
}

// NewDeliveryQueue creates a new delivery queue.
func NewDeliveryQueue(privateKey any, actorID string, workers int) *DeliveryQueue {
	var rsaKey *rsa.PrivateKey
	if k, ok := privateKey.(*rsa.PrivateKey); ok {
		rsaKey = k
	}

	return &DeliveryQueue{
		client:   NewClient(rsaKey, actorID),
		workers:  workers,
		stopChan: make(chan struct{}),
	}
}

// Start starts the delivery workers.
func (q *DeliveryQueue) Start() {
	for i := range q.workers {
		q.waitGroup.Add(1)
		go q.worker(i)
	}
}

// Stop stops the delivery workers.
func (q *DeliveryQueue) Stop() {
	close(q.stopChan)
	q.waitGroup.Wait()
}

// worker processes deliveries from the queue.
func (q *DeliveryQueue) worker(id int) { //nolint:unparam // param reserved for future use
	defer q.waitGroup.Done()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-q.stopChan:
			return
		case <-ticker.C:
			q.processPending()
		}
	}
}

// processPending processes pending deliveries.
func (q *DeliveryQueue) processPending() {
	var deliveries []Delivery

	now := time.Now()

	// Find pending deliveries that are ready to be processed
	err := db.GetDB().Where("status IN ? AND (next_retry_at IS NULL OR next_retry_at <= ?)",
		[]DeliveryStatus{DeliveryStatusPending, DeliveryStatusProcessing}, now).
		Order("created_at ASC").
		Limit(10).
		Find(&deliveries).Error

	if err != nil || len(deliveries) == 0 {
		return
	}

	for _, delivery := range deliveries {
		q.deliver(delivery)
	}
}

// deliver attempts to deliver a single message.
func (q *DeliveryQueue) deliver(delivery Delivery) {
	// Mark as processing
	now := time.Now()
	db.GetDB().Model(&delivery).Updates(map[string]any{
		"status":     DeliveryStatusProcessing, //nolint:goconst
		"updated_at": now,                      //nolint:goconst
	})

	// Parse the payload
	var activity map[string]any
	if err := json.Unmarshal([]byte(delivery.Payload), &activity); err != nil {
		q.markFailed(&delivery, "invalid payload")

		return
	}

	// Attempt delivery
	err := q.client.Post(context.Background(), delivery.TargetInbox, activity)
	if err != nil {
		q.handleDeliveryError(&delivery, err)

		return
	}

	// Mark as delivered
	db.GetDB().Model(&delivery).Updates(map[string]any{
		"status":     DeliveryStatusDelivered,
		"updated_at": time.Now(),
	})
}

// handleDeliveryError handles a delivery error.
func (q *DeliveryQueue) handleDeliveryError(delivery *Delivery, err error) {
	delivery.Retries++
	delivery.LastError = err.Error()

	// Check if we should retry
	if delivery.Retries < 5 {
		// Calculate next retry time with exponential backoff
		nextRetry := time.Now().Add(time.Duration(delivery.Retries*delivery.Retries) * time.Minute)
		delivery.NextRetryAt = &nextRetry
		delivery.Status = DeliveryStatusPending

		db.GetDB().Model(delivery).Updates(map[string]any{
			"status":        delivery.Status,
			"retries":       delivery.Retries,
			"last_error":    delivery.LastError,
			"next_retry_at": delivery.NextRetryAt,
		})
	} else {
		q.markFailed(delivery, err.Error())
	}
}

// markFailed marks a delivery as permanently failed.
func (q *DeliveryQueue) markFailed(delivery *Delivery, reason string) {
	db.GetDB().Model(delivery).Updates(map[string]any{
		"status":     DeliveryStatusFailed,
		"last_error": reason,
		"updated_at": time.Now(),
	})
}

// Enqueue adds a new delivery to the queue.
func (q *DeliveryQueue) Enqueue(activityID, targetInbox string, payload map[string]any) error {
	// Strip privacy-sensitive fields before storing
	cleanPayload := stripPrivacyFields(payload)

	payloadBytes, err := json.Marshal(cleanPayload)
	if err != nil {
		return fmt.Errorf("marshaling payload: %w", err)
	}

	delivery := Delivery{
		ActivityID:  activityID,
		TargetInbox: targetInbox,
		Payload:     string(payloadBytes),
		Status:      DeliveryStatusPending,
		Retries:     0,
	}

	if err := db.GetDB().Create(&delivery).Error; err != nil {
		return fmt.Errorf("creating delivery: %w", err)
	}

	return nil
}

// EnqueueToMany adds a delivery to multiple inboxes.
func (q *DeliveryQueue) EnqueueToMany(activityID string, targetInboxes []string, payload map[string]any) error {
	// Strip privacy-sensitive fields before storing
	cleanPayload := stripPrivacyFields(payload)

	payloadBytes, err := json.Marshal(cleanPayload)
	if err != nil {
		return fmt.Errorf("marshaling payload: %w", err)
	}

	// Create deliveries in batch
	deliveries := make([]Delivery, 0, len(targetInboxes))
	for _, inbox := range targetInboxes {
		deliveries = append(deliveries, Delivery{
			ActivityID:  activityID,
			TargetInbox: inbox,
			Payload:     string(payloadBytes),
			Status:      DeliveryStatusPending,
			Retries:     0,
		})
	}

	if err := db.GetDB().CreateInBatches(deliveries, 100).Error; err != nil {
		return fmt.Errorf("creating deliveries: %w", err)
	}

	return nil
}

// stripPrivacyFields removes bto, bcc fields from activity before delivery
// These fields should not be exposed to recipients.
func stripPrivacyFields(activity map[string]any) map[string]any {
	// Create a copy to avoid modifying the original
	result := make(map[string]any)
	maps.Copy(result, activity)

	// Remove privacy-sensitive fields
	delete(result, "bto")
	delete(result, "bcc")

	// Also strip from nested object if present
	if obj, ok := result["object"].(map[string]any); ok {
		cleanObj := make(map[string]any)
		maps.Copy(cleanObj, obj)

		delete(cleanObj, "bto")
		delete(cleanObj, "bcc")
		result["object"] = cleanObj
	}

	return result
}

// RetryPolicy defines the retry intervals.
var RetryPolicy = []time.Duration{
	5 * time.Minute,
	30 * time.Minute,
	2 * time.Hour,
	12 * time.Hour,
	24 * time.Hour,
}

// GetNextRetryTime calculates the next retry time based on retry count.
func GetNextRetryTime(retries int) time.Time {
	if retries >= len(RetryPolicy) {
		retries = len(RetryPolicy) - 1
	}

	return time.Now().Add(RetryPolicy[retries])
}
