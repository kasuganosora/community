package activitypub

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/kasuganosora/community/db"
)

// DeliveryStatus represents the status of a delivery
type DeliveryStatus string

const (
	DeliveryStatusPending    DeliveryStatus = "pending"
	DeliveryStatusProcessing DeliveryStatus = "processing"
	DeliveryStatusDelivered  DeliveryStatus = "delivered"
	DeliveryStatusFailed     DeliveryStatus = "failed"
)

// Delivery represents a pending ActivityPub delivery
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

// DeliveryQueue manages ActivityPub message delivery
type DeliveryQueue struct {
	client    *Client
	workers   int
	stopChan  chan struct{}
	waitGroup sync.WaitGroup
}

// NewDeliveryQueue creates a new delivery queue
func NewDeliveryQueue(privateKey interface{}, actorID string, workers int) *DeliveryQueue {
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

// Start starts the delivery workers
func (q *DeliveryQueue) Start() {
	for i := 0; i < q.workers; i++ {
		q.waitGroup.Add(1)
		go q.worker(i)
	}
}

// Stop stops the delivery workers
func (q *DeliveryQueue) Stop() {
	close(q.stopChan)
	q.waitGroup.Wait()
}

// worker processes deliveries from the queue
func (q *DeliveryQueue) worker(id int) {
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

// processPending processes pending deliveries
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

// deliver attempts to deliver a single message
func (q *DeliveryQueue) deliver(delivery Delivery) {
	// Mark as processing
	now := time.Now()
	db.GetDB().Model(&delivery).Updates(map[string]interface{}{
		"status":     DeliveryStatusProcessing,
		"updated_at": now,
	})

	// Parse the payload
	var activity map[string]interface{}
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
	db.GetDB().Model(&delivery).Updates(map[string]interface{}{
		"status":     DeliveryStatusDelivered,
		"updated_at": time.Now(),
	})
}

// handleDeliveryError handles a delivery error
func (q *DeliveryQueue) handleDeliveryError(delivery *Delivery, err error) {
	delivery.Retries++
	delivery.LastError = err.Error()

	// Check if we should retry
	if delivery.Retries < 5 {
		// Calculate next retry time with exponential backoff
		nextRetry := time.Now().Add(time.Duration(delivery.Retries*delivery.Retries) * time.Minute)
		delivery.NextRetryAt = &nextRetry
		delivery.Status = DeliveryStatusPending

		db.GetDB().Model(delivery).Updates(map[string]interface{}{
			"status":       delivery.Status,
			"retries":      delivery.Retries,
			"last_error":   delivery.LastError,
			"next_retry_at": delivery.NextRetryAt,
		})
	} else {
		q.markFailed(delivery, err.Error())
	}
}

// markFailed marks a delivery as permanently failed
func (q *DeliveryQueue) markFailed(delivery *Delivery, reason string) {
	db.GetDB().Model(delivery).Updates(map[string]interface{}{
		"status":     DeliveryStatusFailed,
		"last_error": reason,
		"updated_at": time.Now(),
	})
}

// Enqueue adds a new delivery to the queue
func (q *DeliveryQueue) Enqueue(activityID, targetInbox string, payload map[string]interface{}) error {
	payloadBytes, err := json.Marshal(payload)
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

// EnqueueToMany adds a delivery to multiple inboxes
func (q *DeliveryQueue) EnqueueToMany(activityID string, targetInboxes []string, payload map[string]interface{}) error {
	payloadBytes, err := json.Marshal(payload)
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

// RetryPolicy defines the retry intervals
var RetryPolicy = []time.Duration{
	5 * time.Minute,
	30 * time.Minute,
	2 * time.Hour,
	12 * time.Hour,
	24 * time.Hour,
}

// GetNextRetryTime calculates the next retry time based on retry count
func GetNextRetryTime(retries int) time.Time {
	if retries >= len(RetryPolicy) {
		retries = len(RetryPolicy) - 1
	}
	return time.Now().Add(RetryPolicy[retries])
}
