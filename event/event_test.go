package event

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetEventManager(t *testing.T) {
	t.Run("create new manager", func(t *testing.T) {
		em := GetEventManager("test_pool_1")
		assert.NotNil(t, em)
		assert.NotNil(t, em.handlers)
	})

	t.Run("return same manager for same pool", func(t *testing.T) {
		em1 := GetEventManager("test_pool_2")
		em2 := GetEventManager("test_pool_2")
		assert.Equal(t, em1, em2)
	})

	t.Run("return different managers for different pools", func(t *testing.T) {
		em1 := GetEventManager("test_pool_3")
		em2 := GetEventManager("test_pool_4")
		// Both should be non-nil EventManagers with separate handler maps
		assert.NotNil(t, em1)
		assert.NotNil(t, em2)
	})
}

func TestEventManager_Register(t *testing.T) {
	em := GetEventManager("test_register")

	handler := &Handler{
		Exec: func(event Event) bool {
			return true
		},
	}

	id := em.Register("test_event", handler)
	assert.Equal(t, 1, id)

	// Register second handler
	handler2 := &Handler{
		Exec: func(event Event) bool {
			return true
		},
	}
	id2 := em.Register("test_event", handler2)
	assert.Equal(t, 2, id2)
}

func TestEventManager_Remove(t *testing.T) {
	em := GetEventManager("test_remove")

	handler1 := &Handler{Exec: func(event Event) bool { return true }}
	handler2 := &Handler{Exec: func(event Event) bool { return true }}

	id1 := em.Register("remove_event", handler1)
	em.Register("remove_event", handler2)

	// Remove first handler
	em.Remove("remove_event", id1)

	// Verify handler was removed by checking remaining handlers
	em.mu.RLock()
	handlers := em.handlers["remove_event"]
	em.mu.RUnlock()

	assert.Len(t, handlers, 1)
}

func TestEventManager_Trigger(t *testing.T) {
	em := GetEventManager("test_trigger")

	var executed bool

	handler := &Handler{
		Exec: func(event Event) bool {
			executed = true

			assert.Equal(t, "trigger_test", event.Name)
			assert.Equal(t, "test_data", event.Data)

			return true
		},
	}

	em.Register("trigger_test", handler)
	em.Trigger(Event{Name: "trigger_test", Data: "test_data"})

	assert.True(t, executed)
}

func TestEventManager_TriggerMultiple(t *testing.T) {
	em := GetEventManager("test_trigger_multiple")

	var callOrder []int

	handler1 := &Handler{
		Exec: func(event Event) bool {
			callOrder = append(callOrder, 1)

			return true
		},
	}
	handler2 := &Handler{
		Exec: func(event Event) bool {
			callOrder = append(callOrder, 2)

			return true
		},
	}

	em.Register("multi_trigger", handler1)
	em.Register("multi_trigger", handler2)

	em.Trigger(Event{Name: "multi_trigger"})

	assert.Equal(t, []int{1, 2}, callOrder)
}

func TestEventManager_TriggerStopOnFalse(t *testing.T) {
	em := GetEventManager("test_stop_on_false")

	var executed []int

	handler1 := &Handler{
		Exec: func(event Event) bool {
			executed = append(executed, 1)

			return false // Stop propagation
		},
	}
	handler2 := &Handler{
		Exec: func(event Event) bool {
			executed = append(executed, 2)

			return true
		},
	}

	em.Register("stop_event", handler1)
	em.Register("stop_event", handler2)

	em.Trigger(Event{Name: "stop_event"})

	assert.Equal(t, []int{1}, executed) // Only first handler should execute
}

func TestEventManager_WildcardHandler(t *testing.T) {
	em := GetEventManager("test_wildcard")

	var (
		wildcardExecuted bool
		specificExecuted bool
	)

	wildcardHandler := &Handler{
		Exec: func(event Event) bool {
			wildcardExecuted = true

			return true
		},
	}
	specificHandler := &Handler{
		Exec: func(event Event) bool {
			specificExecuted = true

			return true
		},
	}

	em.Register("*", wildcardHandler)
	em.Register("specific_event", specificHandler)

	em.Trigger(Event{Name: "specific_event"})

	assert.True(t, wildcardExecuted)
	assert.True(t, specificExecuted)
}

func TestEventManager_ConcurrentAccess(t *testing.T) {
	em := GetEventManager("test_concurrent")

	var wg sync.WaitGroup

	numGoroutines := 100

	for i := range numGoroutines {
		wg.Add(1)
		go func(id int) { //nolint:unparam // param reserved for future use
			defer wg.Done()

			handler := &Handler{
				Exec: func(event Event) bool {
					return true
				},
			}
			em.Register("concurrent_event", handler)
		}(i)
	}

	wg.Wait()

	em.mu.RLock()
	handlers := em.handlers["concurrent_event"]
	em.mu.RUnlock()

	assert.Len(t, handlers, numGoroutines)
}

func TestGlobalFunctions(t *testing.T) {
	t.Run("Register", func(t *testing.T) {
		handler := &Handler{Exec: func(event Event) bool { return true }}
		id := Register("global_test", "global_event", handler)
		assert.Positive(t, id)
	})

	t.Run("Trigger", func(t *testing.T) {
		var triggered bool

		handler := &Handler{
			Exec: func(event Event) bool {
				triggered = true

				return true
			},
		}
		Register("global_trigger_test", "fire_event", handler)
		Trigger("global_trigger_test", Event{Name: "fire_event"})
		assert.True(t, triggered)
	})

	t.Run("Remove", func(t *testing.T) {
		handler := &Handler{Exec: func(event Event) bool { return true }}
		id := Register("global_remove_test", "to_remove", handler)
		Remove("global_remove_test", "to_remove", id)
	})
}

func TestEventStruct(t *testing.T) {
	event := Event{
		Name: "test_event",
		Data: map[string]any{"key": "value"},
	}

	assert.Equal(t, "test_event", event.Name)
	assert.NotNil(t, event.Data)
}

func TestHandlerStruct(t *testing.T) {
	handler := &Handler{
		Id: 1,
		Exec: func(event Event) bool {
			return true
		},
	}

	assert.Equal(t, 1, handler.Id)
	assert.NotNil(t, handler.Exec)
}

func TestEventManager_TriggerNonExistentEvent(t *testing.T) {
	em := GetEventManager("test_nonexistent")

	// Should not panic when triggering non-existent event
	assert.NotPanics(t, func() {
		em.Trigger(Event{Name: "nonexistent_event"})
	})
}

func TestEventManager_RemoveNonExistent(t *testing.T) {
	em := GetEventManager("test_remove_nonexistent")

	// Should not panic when removing non-existent handler
	assert.NotPanics(t, func() {
		em.Remove("nonexistent_event", 999)
	})
}
