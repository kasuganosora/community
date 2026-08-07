# invitecode (邀请码系统)

社区实例的邀请码（invite code）核心逻辑模块。负责邀请码的**消费**、**验证**以及注册后的**回调动作**执行，并提供过期/用尽邀请码的批量清理能力。

> 邀请码本身的**创建**、**查询**、**校验**（`checkAndGetInviteCode` / `isInviteValid`）由控制器层完成，见 `controllers/api/account/invites.go` 与 `controllers/api/account/register.go`；本包聚焦于「消费计数 + 注册后动作」这一注册流程中必须保证原子性/幂等性的环节。

## 核心功能

| 功能 | 说明 | 主要入口 |
|------|------|----------|
| 消费邀请码 | 在满足可用条件（未禁用、未过期、未用尽）的前提下，原子地递增 `use_count` | [`ConsumeInviteCode`](#consumeinvitecode)、[`ConsumeInviteCodeTx`](#consumeinvitecodetx) |
| 注册后动作 | 消费成功后执行邀请码配置的回调（如自动关注拥有者） | [`ExecAfterRegisterAction`](#execafterregisteraction) |
| 可用性判断 | 判断邀请码是否仍可使用 | `models.InviteCode.IsAvailable()` |
| 过期清理 | 软删除已过期或已用尽的邀请码 | [`CleanupExpired`](#cleanupexpired) |
| 便捷常量/错误 | 标准化的动作常量与哨兵错误 | [`InviteCodeAfterRegisterActionFollowMe`](#常量)、[`ErrNoInviteCode`](#错误) |

## 用法示例

### 注册流程（推荐用法）

在注册事务内使用 `ConsumeInviteCodeTx`，使「邀请码计数递增」与「用户/账号创建」绑定在**同一事务**中——任意一步失败都会整体回滚，不会出现「用户没创建成功却消耗了名额」的情况。注册流程成功提交后，再调用 `ExecAfterRegisterAction` 执行回调动作。

```go
import (
    "errors"

    "github.com/kasuganosora/community/db"
    "github.com/kasuganosora/community/invitecode"
    "github.com/kasuganosora/community/models"
    "gorm.io/gorm"
)

func Register(reqInviteCode string) (*models.User, *models.Account, *models.InviteCode, error) {
    var created *models.InviteCode

    err := db.GetDB().Transaction(func(tx *gorm.DB) error {
        // 1. 在同一事务内消费邀请码（仅当提供了邀请码时）
        //    未提供邀请码时返回 (nil, ErrNoInviteCode)，调用方可按"邀请码可选"语义处理。
        consumed, consumeErr := invitecode.ConsumeInviteCodeTx(tx, reqInviteCode)
        if consumeErr != nil && !errors.Is(consumeErr, invitecode.ErrNoInviteCode) {
            return consumeErr
        }
        if consumed != nil {
            created = consumed
        }

        // 2. 创建 user / account ...（略）
        //    若此处失败，use_count 的递增会随事务回滚。
        return nil
    })
    if err != nil {
        return nil, nil, nil, err
    }

    // 3. 事务提交成功后，执行注册后动作（如 follow_me）。
    //    注意：此时传入的应是消费成功后回填的最新记录（UseCount 已更新）。
    invitecode.ExecAfterRegisterAction(db.GetDB(), created, user, account)

    return user, account, created, nil
}
```

> 在注册流程中，**推荐使用 `ConsumeInviteCodeTx` 而非 `ConsumeInviteCode`**：前者直接在传入的 `tx` 上执行，可与用户创建处于同一事务边界。

### 独立消费（自带事务）

无需与其它操作绑定事务时，可使用 `ConsumeInviteCode`——它在内部自行开启事务：

```go
inv, err := invitecode.ConsumeInviteCode(db.GetDB(), "ABC123XYZ")
if err != nil {
    // "invite code is empty" / "invite code not found"
    // "invite code is not available"（禁用/过期/用尽/并发竞争中未抢到名额）
    return err
}

// inv.UseCount 已是递增后的最新值
fmt.Println("current use count:", inv.UseCount)
```

### 区分「未提供」与「无效」

`ConsumeInviteCodeTx` 在 `code == ""` 时返回 `(nil, ErrNoInviteCode)`，调用方可用 `errors.Is` 区分「未提供邀请码」与「无效邀请码」：

```go
inv, err := invitecode.ConsumeInviteCodeTx(tx, code)
if err != nil {
    if errors.Is(err, invitecode.ErrNoInviteCode) {
        // 未提供邀请码——若系统要求邀请码注册，在此拒绝
    }
    // 其它错误：不存在 / 不可用 / 已用尽
    return err
}
```

### 定期清理过期/用尽邀请码

在后台定时任务中调用 `CleanupExpired`，软删除已过期（`expire_at < now`）或已用尽（`max_use_count > 0` 且 `use_count >= max_use_count`）的邀请码：

```go
n, err := invitecode.CleanupExpired(db.GetDB())
if err != nil {
    log.Error("cleanup invite codes failed: %v", err)
    return
}
log.Info("cleaned up %d invite codes", n)
```

## exec 管理命令

`ExecAfterRegisterAction` 是邀请码系统的「执行管理」入口，负责在邀请码被消费后执行其 `after_register_action` 配置的动作。该函数内置了多重保护：

1. **空指针保护**：`inv == nil` 或 `AfterRegisterAction == ""` 时直接返回。
2. **二次可用性校验**：在 `checkAndGetInviteCode` 与执行回调之间，邀请码状态可能变化（被禁用/用尽/过期），因此再次调用 `inv.IsAvailable()` 校验。
3. **审核/邮箱门控**：
   - 若系统设置 `config.registration.approval_required` 为真且用户处于 `UserStatusUnapproved`，则跳过；
   - 若 `config.registration.check_email` 为真且用户处于 `UserStatusUnverifiedMail`，则跳过（待邮箱验证通过后再执行）。
4. **幂等回调**：当前唯一的动作 `follow_me` 通过 `FirstOrCreate` 创建关注关系，重复触发不会产生重复记录。
5. **不 panic**：任何数据库错误都会被记录（`log.Error`）但不中断注册主流程。

### 当前支持的动作

| 动作常量 | 值 | 说明 |
|----------|------|------|
| `InviteCodeAfterRegisterActionFollowMe` | `"follow_me"` | 让新注册用户关注邀请码拥有者的**非机器人**账号；不能关注自己；关注关系幂等 |

## 核心类型与函数

### 常量

| 常量 | 值 | 说明 |
|------|------|------|
| `InviteCodeAfterRegisterActionFollowMe` | `"follow_me"` | 注册后自动关注邀请码拥有者 |

> 内部哨兵 `invalidMaxUseSentinel = 0`：当 `MaxUseCount <= 0` 时邀请码视为不限次数。

### 错误

| 变量 | 说明 |
|------|------|
| `ErrNoInviteCode` | 未提供邀请码（空字符串）。调用方可通过 `errors.Is(err, invitecode.ErrNoInviteCode)` 与「无效邀请码」区分 |

### 函数

#### `ConsumeInviteCode`

```go
func ConsumeInviteCode(db *gorm.DB, code string) (*models.InviteCode, error)
```

以原子方式消费一张邀请码：在满足可用条件的前提下，将 `use_count` 自增 1。

- 使用单条 `UPDATE ... WHERE ...` 完成「检查 + 递增」，避免「先读后写」的 TOCTOU 竞态。
- 内部自行开启事务。若需与用户创建处于同一事务，请使用 `ConsumeInviteCodeTx`。
- 成功返回更新后的记录（`UseCount` 已回填最新值）；邀请码不存在/不可用/已用尽返回错误（人类可读描述，如 `"invite code not found"` / `"invite code is not available"`）。

#### `ConsumeInviteCodeTx`

```go
func ConsumeInviteCodeTx(tx *gorm.DB, code string) (*models.InviteCode, error)
```

在给定事务 `tx` 上以原子方式消费邀请码，**不**自行开启事务，以便将「邀请码计数递增」与同一事务内的其它操作绑定在一起：事务回滚则 `use_count` 递增随之回滚。

- `code == ""` 时返回 `(nil, ErrNoInviteCode)`，表示「未提供邀请码」，便于调用方按「邀请码可选」语义处理。

#### `ExecAfterRegisterAction`

```go
func ExecAfterRegisterAction(db *gorm.DB, inv *models.InviteCode, user *models.User, account *models.Account)
```

执行邀请码的注册后动作（详见 [exec 管理命令](#exec-管理命令)）。

#### `CleanupExpired`

```go
func CleanupExpired(db *gorm.DB) (int64, error)
```

软删除已过期或已用尽的邀请码，返回被清理的记录数。过期判断使用绑定的时间参数（跨 SQLite/MySQL/PostgreSQL 一致）；用尽判断在 Go 侧完成以规避部分 SQL 引擎对列间比较支持不佳的问题。

### `models.InviteCode`

邀请码模型（定义于 `models/invite_code.go`，表名 `invite_codes`）：

| 字段 | 类型 | 说明 |
|------|------|------|
| `Id` | `int64` | 主键 |
| `Code` | `string` | 邀请码字符串（`varchar(64)`，唯一索引） |
| `UserId` | `uint64` | 拥有者用户 ID |
| `AccountId` | `string` | 拥有者账号 ID |
| `MaxUseCount` | `int` | 最大可用次数，`<= 0` 表示不限次数 |
| `UseCount` | `int` | 已使用次数 |
| `ExpireAt` | `*time.Time` | 过期时间，`nil` 表示永不过期 |
| `AfterRegisterAction` | `string` | 注册后动作（如 `follow_me`） |
| `AfterRegisterActionParam` | `map[string]any` | 注册后动作的参数（JSON） |
| `Disabled` | `bool` | 是否禁用 |
| `CreatedAt` / `UpdatedAt` | `*time.Time` | 创建/更新时间（自动） |
| `DeletedAt` | `gorm.DeletedAt` | 软删除标记 |

```go
func (inv *InviteCode) IsAvailable() bool
```

可用性判断：`Disabled` 为假、未用尽（`MaxUseCount > 0` 时 `UseCount < MaxUseCount`）、未过期（`ExpireAt` 为 `nil` 或晚于当前时间）时返回 `true`。

## 设计说明

### 原子消费与并发安全

`ConsumeInviteCode` / `ConsumeInviteCodeTx` 的核心是一条带条件的 `UPDATE`：

```sql
UPDATE invite_codes
   SET use_count = use_count + 1
 WHERE id = ?
   AND disabled = false
   AND (expire_at IS NULL OR expire_at > ?)   -- 下推过期判断
   AND use_count < ?                            -- 仅当有上限时（下推名额判断）
```

- 多个并发请求同时消费同一张邀请码时，数据库串行化这些 `UPDATE`，只有满足条件的请求才会真正递增计数。
- `result.RowsAffected == 0` 表示本轮未抢到名额（并发竞争失败，或邀请码在此期间变为不可用）。
- 时间判断使用绑定参数 `now`（而非 SQL 的 `CURRENT_TIMESTAMP`），保证跨数据库的时间值比较一致。

### 回调幂等性

`follow_me` 使用 `FirstOrCreate` 创建关注关系，`(account_id, follow_id)` 已存在则不重复创建，即使回调被重复触发也不会产生重复记录。

## 依赖关系

| 依赖 | 用途 |
|------|------|
| `github.com/kasuganosora/community/models` | `InviteCode` / `User` / `Account` / `Follow` 模型 |
| `github.com/kasuganosora/community/systemsettings` | 读取 `KeyRegistrationApprovalRequired`、`KeyRegistrationCheckEmail` 判断是否执行回调 |
| `github.com/kasuganosora/community/log` | 日志 |
| `gorm.io/gorm` | 数据库 ORM（事务、条件更新、软删除） |
| `github.com/pkg/errors` | 错误包装 |

### 集成位置

| 调用方 | 使用 |
|--------|------|
| `controllers/api/account/register.go` | `ConsumeInviteCodeTx`（注册事务内消费）、`ExecAfterRegisterAction`（注册成功后执行回调）、`ErrNoInviteCode` |
| `controllers/api/account/invites.go` | `models.InviteCode` 的创建/查询/校验（控制器层实现） |
