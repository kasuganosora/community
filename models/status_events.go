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
