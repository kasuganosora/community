# event (事件总线)

一个轻量级、线程安全的事件总线（event bus）模块，基于发布/订阅（publish/subscribe）模式实现模块间的解耦通信。

通过命名事件池（event pool）隔离不同业务领域的事件，支持多 handler 监听同一事件、通配符监听以及基于返回值的传播中断。所有导出的 API 都是并发安全的，可直接在 goroutine 中使用。

## 核心功能

- **发布/订阅模式**：handler 订阅具名事件，发布者触发事件即可通知所有订阅者，发布者与订阅者互不依赖。
- **命名事件池**：通过 `pool` 参数将事件划分到独立的 `EventManager` 中，不同业务域（如 `user`、`federation`、`notification`）互不干扰。
- **通配符监听**：注册到特殊事件名 `"*"` 的 handler 会接收所有事件，适合用于全局日志、审计或调试。
- **传播中断**：handler 的 `Exec` 返回 `false` 可中止后续 handler 的执行，实现短路逻辑。
- **并发安全**：`EventManager` 内部使用 `sync.RWMutex`，注册、移除与触发均可安全地并发调用。
- **双重检查创建**：事件池按需懒加载，并使用双重检查锁定（double-checked locking）避免并发重复创建。

## 用法示例

### 基本用法

```go
import "github.com/kasuganosora/community/event"

// 订阅事件：注册一个 handler
handler := &event.Handler{
    Exec: func(e event.Event) bool {
        fmt.Printf("收到事件 %s: %v\n", e.Name, e.Data)
        return true // 返回 false 可中断后续 handler 的传播
    },
}
id := event.Register("notification", "user.created", handler)

// 发布事件：触发所有监听 "user.created" 的 handler
event.Trigger("notification", event.Event{
    Name: "user.created",
    Data: map[string]any{"userID": 42, "username": "alice"},
})

// 取消订阅
event.Remove("notification", "user.created", id)
```

### 通配符监听

注册到 `"*"` 的 handler 会被所有事件触发，常用于全局审计或调试：

```go
event.Register("audit", "*", &event.Handler{
    Exec: func(e event.Event) bool {
        log.Printf("[AUDIT] %s -> %v", e.Name, e.Data)
        return true
    },
})

// 触发任意事件时，通配符 handler 都会收到通知
event.Trigger("audit", event.Event{Name: "user.login", Data: "alice"})
event.Trigger("audit", event.Event{Name: "post.published", Data: 123})
```

### 基于 EventManager 的实例化用法

包级函数 `Register` / `Trigger` / `Remove` 都是 `GetEventManager(pool)` 的便捷封装。如需对同一个事件池多次操作，可直接获取 `EventManager` 实例：

```go
em := event.GetEventManager("federation")

// 订阅
id := em.Register("activity.received", &event.Handler{
    Exec: func(e event.Event) bool {
        // 处理联邦活动
        return true
    },
})

// 发布
em.Trigger(event.Event{Name: "activity.received", Data: activity})

// 移除
em.Remove("activity.received", id)
```

### 传播中断

当某个 handler 返回 `false` 时，后续注册到同一事件名的 handler 不会被调用：

```go
em := event.GetEventManager("validation")

em.Register("submit", &event.Handler{
    Exec: func(e event.Event) bool {
        if !validate(e.Data) {
            return false // 校验失败，阻止后续 handler 执行
        }
        return true
    },
})
em.Register("submit", &event.Handler{
    Exec: func(e event.Event) bool {
        // 仅当上一步返回 true 时才会执行
        persist(e.Data)
        return true
    },
})

em.Trigger(event.Event{Name: "submit", Data: payload})
```

## 核心类型与函数

### 类型

#### `Event`

事件结构体，由发布者构造并传递给所有订阅的 handler：

| 字段 | 类型 | 说明 |
|------|------|------|
| `Name` | `string` | 事件名称，用于匹配已注册的 handler |
| `Data` | `any` | 事件载荷（payload），可携带任意类型的数据 |

#### `Handler`

事件处理器，包含执行函数与由 `Register` 自动分配的 ID：

| 字段 | 类型 | 说明 |
|------|------|------|
| `Id` | `int` | handler 标识，由 `Register` 自动赋值，用于后续移除 |
| `Exec` | `func(event Event) bool` | 处理函数，返回 `false` 可中断事件传播 |

#### `EventManager`

事件管理器，负责在单个事件池内维护事件名到 handler 列表的映射。线程安全（内部使用 `sync.RWMutex` 保护）。通常通过 `GetEventManager(pool)` 获取，无需手动构造。

### 包级函数

| 函数 | 说明 |
|------|------|
| `GetEventManager(pool string) *EventManager` | 获取（或按需创建）指定事件池的管理器实例，同一 `pool` 返回同一实例 |
| `Register(pool, eventName string, handler *Handler) int` | 在指定事件池订阅事件，返回 handler ID |
| `Remove(pool, eventName string, id int)` | 在指定事件池按 ID 移除 handler |
| `Trigger(pool string, event Event)` | 在指定事件池触发事件，依次调用匹配的 handler 与通配符 handler |

### `EventManager` 方法

| 方法 | 说明 |
|------|------|
| `Register(eventName string, handler *Handler) int` | 订阅事件，返回 handler ID（按事件名内自增） |
| `Remove(name string, id int)` | 按 ID 移除指定事件名的 handler，handler 不存在时安全无操作 |
| `Trigger(event Event)` | 触发事件；先调用匹配 `event.Name` 的 handler，再调用 `"*"` 通配符 handler；某 handler 返回 `false` 则中断该组的传播 |

## 依赖关系

本包无任何外部依赖，仅使用 Go 标准库：

| 依赖 | 说明 |
|------|------|
| `sync` | 用于 `sync.RWMutex` 实现并发安全的注册、移除与触发 |

> 事件总线是社区项目多个模块（如 `notifications`、`feed`、`activitypub` 联邦等）之间松耦合通信的基础设施，本身不依赖这些业务模块。
