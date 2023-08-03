package event

var eventManagers = make(map[string]*EventManager)

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
}

func GetEventManager(pool string) *EventManager {
	if _, ok := eventManagers[pool]; !ok {
		eventManagers[pool] = &EventManager{
			handlers: make(map[string][]*Handler),
		}
	}
	return eventManagers[pool]
}

func (em *EventManager) Register(eventName string, handler *Handler) (handleId int) {
	handler.Id = len(em.handlers[eventName]) + 1
	em.handlers[eventName] = append(em.handlers[eventName], handler)
	return handler.Id
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
	for _, handler := range em.handlers[event.Name] {
		if handler.Exec(event) == false {
			break
		}
	}

	// 如果有通配符 * 的事件，也触发
	if _, ok := em.handlers["*"]; ok {
		for _, handler := range em.handlers["*"] {
			if handler.Exec(event) == false {
				break
			}
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
