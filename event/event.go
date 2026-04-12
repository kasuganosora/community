package event

import "sync"

var (
	eventManagers = make(map[string]*EventManager)
	mu            sync.RWMutex
)

type Handler struct {
	Id   int
	Exec func(event Event) bool
}

type Event struct {
	Name string
	Data any
}

type EventManager struct {
	handlers map[string][]*Handler
	mu       sync.RWMutex
}

func GetEventManager(pool string) *EventManager {
	mu.RLock()
	em, ok := eventManagers[pool]
	mu.RUnlock()

	if !ok {
		mu.Lock()
		// 双重检查，防止并发创建
		if em, ok = eventManagers[pool]; !ok {
			em = &EventManager{
				handlers: make(map[string][]*Handler),
			}
			eventManagers[pool] = em
		}
		mu.Unlock()
	}
	return em
}

func (em *EventManager) Register(eventName string, handler *Handler) (handleId int) {
	em.mu.Lock()
	defer em.mu.Unlock()
	handler.Id = len(em.handlers[eventName]) + 1
	em.handlers[eventName] = append(em.handlers[eventName], handler)
	return handler.Id
}

func (em *EventManager) Remove(name string, id int) {
	em.mu.Lock()
	defer em.mu.Unlock()
	for i, handler := range em.handlers[name] {
		if handler.Id == id {
			em.handlers[name] = append(em.handlers[name][:i], em.handlers[name][i+1:]...)
			break
		}
	}
}

func (em *EventManager) Trigger(event Event) {
	em.mu.RLock()
	handlers := em.handlers[event.Name]
	wildcardHandlers := em.handlers["*"]
	em.mu.RUnlock()

	for _, handler := range handlers {
		if !handler.Exec(event) {
			break
		}
	}

	// 如果有通配符 * 的事件，也触发
	for _, handler := range wildcardHandlers {
		if !handler.Exec(event) {
			break
		}
	}
}

func Register(pool string, eventName string, handler *Handler) (handleId int) {
	return GetEventManager(pool).Register(eventName, handler)
}

func Remove(pool string, eventName string, id int) {
	GetEventManager(pool).Remove(eventName, id)
}

func Trigger(pool string, event Event) {
	GetEventManager(pool).Trigger(event)
}
