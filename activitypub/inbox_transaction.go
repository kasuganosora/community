package activitypub

import (
	"bytes"
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
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
	original := c.Writer
	buffer := &inboxResponse{ResponseWriter: original, header: make(http.Header)}
	c.Writer = buffer
	defer func() { c.Writer = original }()
	ctx, publishReactions := models.BeginReactionNotifications(ctx)
	ctx, invalidateProfiles := beginProfileInvalidations(ctx)
	handlerFailure := errors.New("activity handler rejected request")
	err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if id != "" {
			marker := ProcessedActivity{ActivityID: id}
			result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&marker)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				c.JSON(200, gin.H{"status": "accepted"})
				return nil
			}
		}
		run(context.WithValue(ctx, inboxTransactionKey{}, tx))
		if buffer.Status() >= 400 {
			return handlerFailure
		}
		return nil
	})
	c.Writer = original
	if err == nil {
		publishReactions()
		invalidateProfiles()
	}
	if err != nil && !errors.Is(err, handlerFailure) {
		c.JSON(500, gin.H{"error": "failed to commit activity"})
		return
	}
	for key, values := range buffer.header {
		original.Header()[key] = values
	}
	original.WriteHeader(buffer.Status())
	_, _ = original.Write(buffer.body.Bytes())
}
