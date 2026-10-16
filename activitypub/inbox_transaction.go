package activitypub

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type inboxTransactionKey struct{}

func activityDB(ctx context.Context) *gorm.DB {
	if tx, ok := ctx.Value(inboxTransactionKey{}).(*gorm.DB); ok {
		return tx
	}
	return db.GetDB().WithContext(ctx)
}

// Buffer the response until the effects and deduplication marker commit.
// A failed handler rolls everything back and remains retryable by its sender.
type inboxResponse struct {
	gin.ResponseWriter
	header http.Header
	body   bytes.Buffer
	status int
}

func (w *inboxResponse) Header() http.Header    { return w.header }
func (w *inboxResponse) WriteHeader(status int) { w.status = status }
func (w *inboxResponse) WriteHeaderNow() {
	if w.status == 0 {
		w.status = 200
	}
}
func (w *inboxResponse) Write(b []byte) (int, error)       { w.WriteHeaderNow(); return w.body.Write(b) }
func (w *inboxResponse) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
func (w *inboxResponse) Status() int {
	if w.status == 0 {
		return 200
	}
	return w.status
}
func (w *inboxResponse) Size() int     { return w.body.Len() }
func (w *inboxResponse) Written() bool { return w.body.Len() > 0 }

func processInboxTransaction(ctx context.Context, c *gin.Context, id string, run func(context.Context)) {
	// Already processed: acknowledge without opening a write transaction.
	if alreadyProcessedActivity(ctx, id) {
		c.JSON(200, gin.H{"status": "accepted"})
		return
	}
	original := c.Writer
	buffer := &inboxResponse{ResponseWriter: original, header: make(http.Header)}
	c.Writer = buffer
	defer func() { c.Writer = original }()
	ctx, publishReactions := models.BeginReactionNotifications(ctx)
	// Status events (live streams reload the row from SQL on receipt) must not
	// fire before the inbox transaction commits, or remote posts are dropped.
	ctx, publishStatusEvents := models.BeginDeferredStatusEvents(ctx)
	ctx, invalidateProfiles := beginProfileInvalidations(ctx)
	handlerFailure := errors.New("activity handler rejected request")
	duplicate := errors.New("activity already processed")
	err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if dialect.IsPostgres(tx) {
			// Fail fast instead of queueing behind another long transaction
			// until the HTTP deadline (504). The sender retries a 5xx.
			if err := tx.Exec("SET LOCAL lock_timeout = '3s'").Error; err != nil {
				return err
			}
			// Inbox effects are replayable: a crash that loses the last
			// commits loses the dedup marker with them, and the sender
			// redelivers. Do not make the HTTP response wait for a WAL fsync
			// (2s+ on the production disks).
			if err := tx.Exec("SET LOCAL synchronous_commit = off").Error; err != nil {
				return err
			}
		}
		run(context.WithValue(ctx, inboxTransactionKey{}, tx))
		if buffer.Status() >= 400 {
			return handlerFailure
		}
		if id != "" {
			// The marker is inserted after the handler so its unique-index
			// entry is held only until commit: a concurrent redelivery waits
			// for that commit alone, sees the conflict and rolls back.
			result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&ProcessedActivity{ActivityID: id})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return duplicate
			}
		}
		return nil
	})
	if errors.Is(err, duplicate) {
		c.Writer = original
		c.JSON(200, gin.H{"status": "accepted"})
		return
	}
	c.Writer = original
	if err == nil {
		publishStatusEvents()
		publishReactions()
		invalidateProfiles()
	}
	if err != nil && !errors.Is(err, handlerFailure) {
		if isLockTimeout(err) {
			c.Header("Retry-After", "60")
			c.JSON(503, gin.H{"error": "busy, retry later"})
			return
		}
		c.JSON(500, gin.H{"error": "failed to commit activity"})
		return
	}
	for key, values := range buffer.header {
		original.Header()[key] = values
	}
	original.WriteHeader(buffer.Status())
	_, _ = original.Write(buffer.body.Bytes())
}

func isLockTimeout(err error) bool {
	// SQLSTATE 55P03 lock_not_available.
	return err != nil && (strings.Contains(err.Error(), "55P03") || strings.Contains(err.Error(), "lock timeout"))
}
