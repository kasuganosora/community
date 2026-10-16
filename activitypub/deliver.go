package activitypub

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sync"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
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
	ID            uint64         `gorm:"primaryKey;autoIncrement"`
	ActivityID    string         `gorm:"type:varchar(255);not null;index"`
	TargetInbox   string         `gorm:"type:varchar(255);not null;index"`
	Payload       string         `gorm:"type:json;not null"`
	Authorization string         `gorm:"type:json;default:null"`
	Status        DeliveryStatus `gorm:"type:varchar(20);default:'pending';index"`
	Retries       int            `gorm:"default:0"`
	LastError     string         `gorm:"type:text"`
	NextRetryAt   *time.Time     `gorm:"index"`
	CreatedAt     *time.Time     `gorm:"autoCreateTime"`
	UpdatedAt     *time.Time     `gorm:"autoUpdateTime"`
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
		// Exclude successors of a deferred retry before applying the batch limit.
		// Otherwise one offline inbox can fill the entire batch with blocked rows
		// and prevent deliveries to healthy, unrelated instances.
		Where(`NOT EXISTS (SELECT 1 FROM activitypub_deliveries AS prior
			WHERE prior.target_inbox = activitypub_deliveries.target_inbox
			AND prior.id < activitypub_deliveries.id AND prior.status IN ?
			AND prior.next_retry_at > ?)`, []DeliveryStatus{DeliveryStatusPending, DeliveryStatusProcessing}, now).
		Order("id ASC").
		Limit(10).
		Find(&deliveries).Error

	if err != nil || len(deliveries) == 0 {
		return
	}

	for _, delivery := range deliveries {
		// Do not overtake an earlier retried Create with an Update/Delete to
		// the same inbox. At-least-once delivery retains stable activity IDs.
		var older int64
		if err := db.GetDB().Model(&Delivery{}).Where("target_inbox = ? AND id < ? AND status IN ?", delivery.TargetInbox, delivery.ID, []DeliveryStatus{DeliveryStatusPending, DeliveryStatusProcessing}).Count(&older).Error; err != nil || older > 0 {
			continue
		}
		q.deliver(delivery)
	}
}

// deliver attempts to deliver a single message.
func (q *DeliveryQueue) deliver(delivery Delivery) {
	// Lease atomically; other workers and restarted processes cannot take
	// an in-flight row until its lease expires.
	now := time.Now()
	lease := now.Add(2 * time.Minute)
	claim := db.GetDB().Model(&Delivery{}).Where("id = ? AND status IN ? AND (next_retry_at IS NULL OR next_retry_at <= ?)", delivery.ID, []DeliveryStatus{DeliveryStatusPending, DeliveryStatusProcessing}, now).Updates(map[string]any{"status": DeliveryStatusProcessing, "next_retry_at": lease, "updated_at": now})
	if claim.Error != nil || claim.RowsAffected != 1 {
		return
	}

	// Parse the payload
	var activity map[string]any
	if err := json.Unmarshal([]byte(delivery.Payload), &activity); err != nil {
		q.markFailed(&delivery, "invalid payload")

		return
	}

	activity, allowed, err := authorizeQueuedDelivery(&delivery, activity)
	if err != nil {
		q.handleDeliveryError(&delivery, err)
		return
	}
	if !allowed {
		db.GetDB().Model(&delivery).Updates(map[string]any{"status": DeliveryStatusDelivered, "next_retry_at": nil, "last_error": "recipient authorization revoked"})
		return
	}

	// Resolve the signing identity for this activity. A global queue must
	// not sign every user's activity as one server actor or send unsigned.
	client, err := q.signingClient(activity)
	if err != nil {
		q.handleDeliveryError(&delivery, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	err = client.Post(ctx, delivery.TargetInbox, activity)
	if errors.Is(err, ErrDeliveryBlocked) {
		db.GetDB().Model(&delivery).Updates(map[string]any{"status": DeliveryStatusDelivered, "next_retry_at": nil, "last_error": "skipped: blocked or suspended instance", "updated_at": time.Now()})
		return
	}
	if err != nil {
		q.handleDeliveryError(&delivery, err)

		return
	}

	// Mark as delivered
	db.GetDB().Model(&delivery).Updates(map[string]any{
		"status":        DeliveryStatusDelivered,
		"next_retry_at": nil,
		"last_error":    "",
		"updated_at":    time.Now(),
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

func (q *DeliveryQueue) signingClient(activity map[string]any) (*Client, error) {
	actorURI, ok := activity["actor"].(string)
	if !ok || actorURI == "" {
		return nil, fmt.Errorf("activity has no signing actor")
	}
	if q.client != nil && q.client.privateKey != nil && q.client.actorID == actorURI {
		return q.client, nil
	}
	var account models.Account
	if err := localActorQuery(db.GetDB(), actorURI).First(&account).Error; err != nil {
		return nil, err
	}
	return localAccountClient(&account, actorURI)
}
