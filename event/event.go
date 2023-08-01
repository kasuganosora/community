package event

var eventManagers = make(map[string]*EventManager)

type Handler struct {
	Id        int
	EventName string
	Exec      func(event Event) bool
}

type Event struct {
	Name string
	Data any
}

type EventManager struct {
	handlers map[string][]*Handler
}

func GetEventManager(pool string) *EventManager {
	if _, ok := eventManagers[pool]; !ok {
		eventManagers[pool] = &EventManager{
			handlers: make(map[string][]*Handler),
		}
	}
	return eventManagers[pool]
}

func (em *EventManager) Register(name string, listenEventName string, handler *Handler) {
	handler.Id = len(em.handlers[name]) + 1
	handler.EventName = listenEventName
	em.handlers[name] = append(em.handlers[name], handler)
}

func (em *EventManager) Remove(name string, id int) {
	for i, handler := range em.handlers[name] {
		if handler.Id == id {
			em.handlers[name] = append(em.handlers[name][:i], em.handlers[name][i+1:]...)
			break
		}
	}
}

func (em *EventManager) Trigger(event Event) {
	go func() {
		for _, handler := range em.handlers[event.Name] {
			if handler.EventName != "*" && handler.EventName != event.Name {
				continue
			}

			if handler.Exec(event) {
				break
			}
		}
	}()
}

func Register(pool string, listenEventName string, name string, handler *Handler) {
	GetEventManager(pool).Register(name, listenEventName, handler)
}

func Remove(pool string, name string, id int) {
	GetEventManager(pool).Remove(name, id)
}

func Trigger(pool string, event Event) {
	GetEventManager(pool).Trigger(event)
}
