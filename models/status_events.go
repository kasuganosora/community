package models

import (
	"context"
	"sync"

	"github.com/kasuganosora/community/event"
	"gorm.io/gorm"
)

type deferredStatusEventsKey struct{}
type deferredStatusEvents struct {
	mu        sync.Mutex
	events    []event.Event
	published bool
}

// BeginDeferredStatusEvents scopes status broadcasts to a caller-controlled
// transaction. Call publish only after its successful commit; discarding the
// closure on rollback discards every broadcast. Unscoped writes are unchanged.
// Status pointers intentionally retain their final associations (in particular
// ConversationId, assigned in AfterSave) when consumers run after commit.
func BeginDeferredStatusEvents(ctx context.Context) (context.Context, func()) {
	buffer := &deferredStatusEvents{}
	publish := func() {
		buffer.mu.Lock()
		if buffer.published {
			buffer.mu.Unlock()
			return
		}
		buffer.published = true
		events := buffer.events
		buffer.events = nil
		buffer.mu.Unlock()
		for _, e := range events {
			event.Trigger("status", e)
		}
	}
	return context.WithValue(ctx, deferredStatusEventsKey{}, buffer), publish
}

func triggerStatusEvent(tx *gorm.DB, e event.Event) {
	if buffer, ok := tx.Statement.Context.Value(deferredStatusEventsKey{}).(*deferredStatusEvents); ok {
		buffer.mu.Lock()
		// The transaction has ended after publish. Never leak events from accidental
		// reuse of its scoped context into a later, uncommitted transaction.
		if !buffer.published {
			buffer.events = append(buffer.events, e)
		}
		buffer.mu.Unlock()
		return
	}
	event.Trigger("status", e)
}

// CreateStatusCommitted creates a status in its own transaction and broadcasts
// its status events only after the commit. Live-stream consumers reload the
// row from SQL as soon as they receive the event, so publishing from inside the
// still-open create transaction made them miss newly created local posts.
func CreateStatusCommitted(conn *gorm.DB, status *Status) error {
	ctx := conn.Statement.Context
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, publish := BeginDeferredStatusEvents(ctx)
	if err := conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return tx.Create(status).Error
	}); err != nil {
		return err
	}
	publish()
	return nil
}
