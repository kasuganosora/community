package models

import (
	"context"
	"testing"

	"github.com/kasuganosora/community/event"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDeferredStatusEventsOnlyPublishAfterExplicitCommit(t *testing.T) {
	var observed []event.Event
	handler := event.Register("status", "*", &event.Handler{Exec: func(e event.Event) bool {
		observed = append(observed, e)
		return true
	}})
	defer event.Remove("status", "*", handler)
	ctx, publish := BeginDeferredStatusEvents(context.Background())
	tx := &gorm.DB{Statement: &gorm.Statement{Context: ctx}}
	status := &Status{Id: 71}
	triggerStatusEvent(tx, event.Event{Name: "after_create", Data: status})
	triggerStatusEvent(tx, event.Event{Name: "after_save", Data: status})
	triggerStatusEvent(tx, event.Event{Name: "after_delete", Data: &Status{Id: 70}})
	require.Empty(t, observed)
	conversation := uint64(99)
	status.ConversationId = &conversation
	publish()
	require.Len(t, observed, 3)
	require.Equal(t, "after_create", observed[0].Name)
	require.Equal(t, "after_save", observed[1].Name)
	require.Equal(t, "after_delete", observed[2].Name)
	require.Equal(t, &conversation, observed[0].Data.(*Status).ConversationId)
	publish()
	require.Len(t, observed, 3, "publish must be idempotent")
}

func TestDeferredStatusEventsRollbackDoesNotBroadcast(t *testing.T) {
	var count int
	handler := event.Register("status", "*", &event.Handler{Exec: func(event.Event) bool { count++; return true }})
	defer event.Remove("status", "*", handler)
	ctx, _ := BeginDeferredStatusEvents(context.Background())
	tx := &gorm.DB{Statement: &gorm.Statement{Context: ctx}}
	triggerStatusEvent(tx, event.Event{Name: "after_delete", Data: &Status{Id: 70}})
	require.Zero(t, count)
	// An independent ordinary mutation remains unaffected by the abandoned scope.
	triggerStatusEvent(&gorm.DB{Statement: &gorm.Statement{Context: context.Background()}}, event.Event{Name: "after_save", Data: &Status{Id: 72}})
	require.Equal(t, 1, count)
}
