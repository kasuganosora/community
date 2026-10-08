package mention

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// 测试辅助函数
// ---------------------------------------------------------------------------

// integrationModels 是 mention 时间线相关数据模型，用于自动迁移。
var integrationModels = []any{
	&models.User{},
	&models.UserSetting{},
	&models.Account{},
	&models.AccountExtra{},
	&models.Status{},
	&models.StatusContent{},
	&models.StatusToAccount{},
	&models.Follow{},
	&models.UserSecretData{},
	&models.StatusEmoji{},
	&models.StatusTag{},
	&models.Tag{},
}

// mentionTestEnv 持有测试环境所需的全部句柄。
type mentionTestEnv struct {
	gormDB    *gorm.DB
	mockRedis *feed.MockRedisClient
}

// setupMentionEnv 为 mention 测试准备全局 DB（SQLite 内存库）与 Mock FeedService。
// Push/Unpush 等函数使用 db.GetDB() 查询提及用户，同时通过 feed 包全局服务操作时间线，
// 因此必须注入可用的全局 DB 和 Mock 服务。
// 返回 gorm.DB（用于插入测试数据）和 MockRedisClient（用于断言）。
// 使用 SQLite 内存库而非 sqlexec，因为 Push/Unpush 内部的嵌套子查询
// (id in (select ...)) 在 sqlexec 内存数据源下不被支持。
func setupMentionEnv(t *testing.T) *mentionTestEnv {
	t.Helper()

	gormDB, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	// SQLite :memory: 需要单连接以确保所有查询共享同一个内存数据库
	sqlDB, err := gormDB.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	// 自动迁移所需模型
	require.NoError(t, gormDB.AutoMigrate(integrationModels...))

	// 注入全局 DB，使 db.GetDB() 返回测试 DB
	database.SetTestDB(gormDB)
	feed.SetDefaultDBService(feed.NewDBService(gormDB))

	// 注入 Mock FeedService
	mockRedis := feed.NewMockRedisClient()
	feed.SetDefaultService(feed.NewFeedService(mockRedis))
	feed.ResetSubscriptionCache()

	t.Cleanup(func() {
		// 等待可能的 goroutine 完成，避免在 DB 关闭后访问
		time.Sleep(50 * time.Millisecond)
		database.ResetTestDB()
		_ = sqlDB.Close()
	})

	return &mentionTestEnv{
		gormDB:    gormDB,
		mockRedis: mockRedis,
	}
}

// createMentionedStatus 在 DB 中创建被提及账户、status 以及 status_to_accounts 关联。
// accountId 为被提及的本地账户 ID，statusAccountId 为发布 status 的账户 ID。
func createMentionedStatus(t *testing.T, gormDB *gorm.DB, statusId, accountId, statusAccountId uint64) *models.Status {
	t.Helper()
	now := time.Now()

	// 创建被提及的本地账户（domain 为 nil）
	account := &models.Account{
		Id:        accountId,
		UserId:    accountId,
		Username:  "mention_user_" + strconv.FormatUint(accountId, 10),
		CreatedAt: &now,
		UpdatedAt: &now,
	}
	require.NoError(t, gormDB.Create(account).Error, "Failed to create mentioned account")

	// 创建 status
	status := &models.Status{
		Id:         statusId,
		AccountId:  statusAccountId,
		Visibility: models.StatusVisibilityPublic,
		CreatedAt:  &now,
		UpdatedAt:  &now,
	}
	require.NoError(t, gormDB.Create(status).Error, "Failed to create status")

	// 创建 status_to_accounts 关联
	sta := &models.StatusToAccount{
		StatusId:  statusId,
		AccountId: accountId,
		CreatedAt: &now,
	}
	require.NoError(t, gormDB.Create(sta).Error, "Failed to create status_to_account")

	return status
}

// createAccountWithUser 在 DB 中创建带 User 和 Settings 的本地账户。
func createAccountWithUser(t *testing.T, gormDB *gorm.DB, accountId uint64, settings *models.UserSetting) *models.Account {
	t.Helper()
	now := time.Now()

	user := &models.User{
		Id:        accountId,
		UUID:      "user-" + strconv.FormatUint(accountId, 10),
		UserName:  "user_" + strconv.FormatUint(accountId, 10),
		EmailHash: "hash_" + strconv.FormatUint(accountId, 10),
		CreatedAt: &now,
		UpdatedAt: &now,
	}
	if settings != nil {
		user.Settings = settings
	}
	require.NoError(t, gormDB.Create(user).Error)

	account := &models.Account{
		Id:        accountId,
		UserId:    user.Id,
		Username:  "acc_" + strconv.FormatUint(accountId, 10),
		CreatedAt: &now,
		UpdatedAt: &now,
	}
	require.NoError(t, gormDB.Create(account).Error)

	return account
}

// skipIfNoGlobalRedis 当没有全局 Redis 配置时跳过测试。
func skipIfNoGlobalRedis(t *testing.T) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Skipf("Skipping test: no global Redis configured: %v", r)
		}
	}()
	if db.GetRedis() == nil {
		t.Skip("Skipping test: no global Redis configured")
	}
}

// ---------------------------------------------------------------------------
// Push —— 使用 MockRedisClient + 注入 DB
// ---------------------------------------------------------------------------

// Push 没有任何提及账户时不应报错，也不应向时间线写入数据。
func TestPush_NoMentionedAccounts(t *testing.T) {
	env := setupMentionEnv(t)

	status := &models.Status{
		Id:         1001,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
	}

	err := Push(status, false, false)
	require.NoError(t, err)

	// 没有任何提及记录，时间线不应有数据
	var count int64
	env.gormDB.Model(&models.StatusToAccount{}).Where("status_id = ?", status.Id).Count(&count)
	assert.Equal(t, int64(0), count)
}

// Push 有提及的本地账户时，应将 status 写入提及用户的时间线。
func TestPush_WithMentionedLocalAccount(t *testing.T) {
	env := setupMentionEnv(t)

	createMentionedStatus(t, env.gormDB, 2001, 10, 1)

	status := &models.Status{
		Id:         2001,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
	}

	err := Push(status, false, false)
	require.NoError(t, err)

	// 验证 status 被写入 feed:mention:10
	zSet := env.mockRedis.GetZSet("feed:mention:10")
	assert.Contains(t, zSet, "2001", "status 应被写入提及用户的时间线")
}

// Push 有多个提及的本地账户时，每个账户的时间线都应包含 status。
// 注意：多个账户会走 BatchAddToTimelineItems 的 Pipeline 路径，
// MockRedisClient.Pipelined 是 no-op，因此不验证 Redis 写入，
// 只验证 Push 不报错且能正确查询到多个提及账户。
func TestPush_WithMultipleMentionedAccounts(t *testing.T) {
	env := setupMentionEnv(t)

	// 创建两个被提及的本地账户，共享同一个 status
	createMentionedStatus(t, env.gormDB, 3001, 20, 1)
	now := time.Now()
	// 第二个账户共享同一个 status，只需创建账户和关联
	account2 := &models.Account{
		Id:        21,
		UserId:    21,
		Username:  "mention_user_21",
		CreatedAt: &now,
		UpdatedAt: &now,
	}
	require.NoError(t, env.gormDB.Create(account2).Error)
	require.NoError(t, env.gormDB.Create(&models.StatusToAccount{
		StatusId:  3001,
		AccountId: 21,
		CreatedAt: &now,
	}).Error)

	status := &models.Status{
		Id:         3001,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
	}

	// Push 内部会查询到 2 个本地提及账户，走 BatchAddToTimelineItems 路径
	err := Push(status, false, false)
	require.NoError(t, err)

	// 验证数据库中确实有 2 个提及关联
	var count int64
	env.gormDB.Model(&models.StatusToAccount{}).Where("status_id = ?", status.Id).Count(&count)
	assert.Equal(t, int64(2), count, "应有 2 个提及关联")
}

// update=true 时不应 panic 且应正常写入。
func TestPush_UpdateFlag(t *testing.T) {
	env := setupMentionEnv(t)

	createMentionedStatus(t, env.gormDB, 4001, 30, 1)

	status := &models.Status{
		Id:         4001,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
	}

	err := Push(status, true, false)
	require.NoError(t, err)

	zSet := env.mockRedis.GetZSet("feed:mention:30")
	assert.Contains(t, zSet, "4001")
}

// 有订阅者且 pushToClient=true 时应触发客户端推送且不报错。
func TestPush_WithClientPush(t *testing.T) {
	env := setupMentionEnv(t)

	// 模拟该时间线有订阅者
	env.mockRedis.SetChannel("subscribed:feed:mention:40", true)

	createMentionedStatus(t, env.gormDB, 5001, 40, 1)

	status := &models.Status{
		Id:         5001,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
	}

	err := Push(status, false, true)
	require.NoError(t, err)

	zSet := env.mockRedis.GetZSet("feed:mention:40")
	assert.Contains(t, zSet, "5001")
}

// pushToClient=false 时不应推送客户端消息，但仍应写入时间线。
func TestPush_WithoutClientPush(t *testing.T) {
	env := setupMentionEnv(t)

	createMentionedStatus(t, env.gormDB, 6001, 50, 1)

	status := &models.Status{
		Id:         6001,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
	}

	err := Push(status, false, false)
	require.NoError(t, err)

	zSet := env.mockRedis.GetZSet("feed:mention:50")
	assert.Contains(t, zSet, "6001")
}

// 转发 status（带 ReblogId）应走单条添加分支。
func TestPush_ReblogStatus(t *testing.T) {
	env := setupMentionEnv(t)

	createMentionedStatus(t, env.gormDB, 7001, 60, 1)

	reblogId := uint64(10000)
	status := &models.Status{
		Id:        7001,
		AccountId: 1,
		ReblogId:  &reblogId,
	}

	err := Push(status, false, false)
	require.NoError(t, err)
}

// 转发 status 且账户有 User.Settings（aggregatesReblogs=true）。
func TestPush_ReblogWithUserSettings(t *testing.T) {
	env := setupMentionEnv(t)

	account := createAccountWithUser(t, env.gormDB, 70, &models.UserSetting{
		Data: map[string]any{"aggregates_reblogs": true},
	})

	now := time.Now()
	status := &models.Status{
		Id:         8001,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
		CreatedAt:  &now,
		UpdatedAt:  &now,
	}
	require.NoError(t, env.gormDB.Create(status).Error)
	require.NoError(t, env.gormDB.Create(&models.StatusToAccount{
		StatusId:  8001,
		AccountId: account.Id,
		CreatedAt: &now,
	}).Error)

	reblogId := uint64(20000)
	status.ReblogId = &reblogId

	err := Push(status, false, false)
	require.NoError(t, err)
}

// 转发 status 且账户有 User.Settings（aggregatesReblogs=false）。
func TestPush_ReblogWithDisabledAggregates(t *testing.T) {
	env := setupMentionEnv(t)

	account := createAccountWithUser(t, env.gormDB, 71, &models.UserSetting{
		Data: map[string]any{"aggregates_reblogs": false},
	})

	now := time.Now()
	status := &models.Status{
		Id:         8101,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
		CreatedAt:  &now,
		UpdatedAt:  &now,
	}
	require.NoError(t, env.gormDB.Create(status).Error)
	require.NoError(t, env.gormDB.Create(&models.StatusToAccount{
		StatusId:  8101,
		AccountId: account.Id,
		CreatedAt: &now,
	}).Error)

	reblogId := uint64(21000)
	status.ReblogId = &reblogId

	err := Push(status, false, false)
	require.NoError(t, err)
}

// 连续推送多条 status 后时间线应包含所有 ID。
// 每条 status 提及同一个账户 80，多条 status 走单账户 BatchAdd 路径（实际是单条 Add）。
func TestPush_MultipleStatuses(t *testing.T) {
	env := setupMentionEnv(t)

	// 先创建账户 80（只创建一次）
	now := time.Now()
	account := &models.Account{
		Id:        80,
		UserId:    80,
		Username:  "mention_user_80",
		CreatedAt: &now,
		UpdatedAt: &now,
	}
	require.NoError(t, env.gormDB.Create(account).Error)

	// 创建 5 个不同的 status，都提及账户 80
	for i := range 5 {
		status := &models.Status{
			Id:         uint64(9000 + i),
			AccountId:  1,
			Visibility: models.StatusVisibilityPublic,
			CreatedAt:  &now,
			UpdatedAt:  &now,
		}
		require.NoError(t, env.gormDB.Create(status).Error)
		require.NoError(t, env.gormDB.Create(&models.StatusToAccount{
			StatusId:  status.Id,
			AccountId: 80,
			CreatedAt: &now,
		}).Error)
	}

	for i := range 5 {
		status := &models.Status{
			Id:         uint64(9000 + i),
			AccountId:  1,
			Visibility: models.StatusVisibilityPublic,
		}
		err := Push(status, false, false)
		require.NoError(t, err)
	}

	// 单个账户每次都走 AddToTimelineItem（BatchAdd 单条优化），数据应写入
	zSet := env.mockRedis.GetZSet("feed:mention:80")
	for i := range 5 {
		assert.Contains(t, zSet, strconv.Itoa(9000+i))
	}
}

// 被提及的账户是远程账户（domain 非 null）时不应写入时间线（只推送给本地用户）。
func TestPush_RemoteAccountFiltered(t *testing.T) {
	env := setupMentionEnv(t)

	now := time.Now()
	remoteDomain := "remote.example.com"
	// 创建远程账户
	remoteAccount := &models.Account{
		Id:        90,
		UserId:    90,
		Username:  "remote_mention",
		Domain:    &remoteDomain,
		CreatedAt: &now,
		UpdatedAt: &now,
	}
	require.NoError(t, env.gormDB.Create(remoteAccount).Error)

	status := &models.Status{
		Id:         9101,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
		CreatedAt:  &now,
		UpdatedAt:  &now,
	}
	require.NoError(t, env.gormDB.Create(status).Error)
	require.NoError(t, env.gormDB.Create(&models.StatusToAccount{
		StatusId:  9101,
		AccountId: 90,
		CreatedAt: &now,
	}).Error)

	err := Push(status, false, false)
	require.NoError(t, err)

	// 远程账户不应有时间线数据（Push 内部 WHERE domain is null 过滤掉）
	zSet := env.mockRedis.GetZSet("feed:mention:90")
	assert.NotContains(t, zSet, "9101")
}

// ---------------------------------------------------------------------------
// Unpush —— 使用 MockRedisClient + 注入 DB
// ---------------------------------------------------------------------------

// Unpush 应从提及用户的时间线移除 status。
func TestUnpush_RemovesFromTimeline(t *testing.T) {
	env := setupMentionEnv(t)

	createMentionedStatus(t, env.gormDB, 11001, 100, 1)

	status := &models.Status{
		Id:         11001,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
	}

	// 先 Push
	err := Push(status, false, false)
	require.NoError(t, err)
	assert.Contains(t, env.mockRedis.GetZSet("feed:mention:100"), "11001")

	// 再 Unpush
	err = Unpush(status, false)
	require.NoError(t, err)

	zSet := env.mockRedis.GetZSet("feed:mention:100")
	assert.NotContains(t, zSet, "11001")
}

// Unpush 有订阅者且 pushToClient=true 时应触发删除推送。
func TestUnpush_WithClientPush(t *testing.T) {
	env := setupMentionEnv(t)

	env.mockRedis.SetChannel("subscribed:feed:mention:110", true)

	createMentionedStatus(t, env.gormDB, 12001, 110, 1)

	status := &models.Status{
		Id:         12001,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
	}

	// 先 Push
	err := Push(status, false, false)
	require.NoError(t, err)

	// Unpush 并推送
	err = Unpush(status, true)
	require.NoError(t, err)

	zSet := env.mockRedis.GetZSet("feed:mention:110")
	assert.NotContains(t, zSet, "12001")
}

// Unpush 读取 User.Settings 分支。
func TestUnpush_WithUserSettings(t *testing.T) {
	env := setupMentionEnv(t)

	account := createAccountWithUser(t, env.gormDB, 120, &models.UserSetting{
		Data: map[string]any{"aggregates_reblogs": true},
	})

	now := time.Now()
	status := &models.Status{
		Id:         13001,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
		CreatedAt:  &now,
		UpdatedAt:  &now,
	}
	require.NoError(t, env.gormDB.Create(status).Error)
	require.NoError(t, env.gormDB.Create(&models.StatusToAccount{
		StatusId:  13001,
		AccountId: account.Id,
		CreatedAt: &now,
	}).Error)

	err := Push(status, false, false)
	require.NoError(t, err)

	err = Unpush(status, false)
	require.NoError(t, err)
}

// Unpush 没有提及关联时不报错。
func TestUnpush_NoMentionedAccounts(t *testing.T) {
	setupMentionEnv(t)

	status := &models.Status{
		Id:         99999,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
	}

	err := Unpush(status, false)
	require.NoError(t, err)
}

// Unpush 有订阅者且 pushToClient=false 时不应推送删除消息。
func TestUnpush_WithoutClientPush(t *testing.T) {
	env := setupMentionEnv(t)

	env.mockRedis.SetChannel("subscribed:feed:mention:130", true)

	createMentionedStatus(t, env.gormDB, 14001, 130, 1)

	status := &models.Status{
		Id:         14001,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
	}

	err := Push(status, false, false)
	require.NoError(t, err)

	// pushToClient=false，不应推送但应移除
	err = Unpush(status, false)
	require.NoError(t, err)

	zSet := env.mockRedis.GetZSet("feed:mention:130")
	assert.NotContains(t, zSet, "14001")
}

// Unpush 远程账户不应影响时间线（Push 时远程账户被过滤，Unpush 同理）。
func TestUnpush_RemoteAccountFiltered(t *testing.T) {
	env := setupMentionEnv(t)

	now := time.Now()
	remoteDomain := "remote.example.com"
	remoteAccount := &models.Account{
		Id:        140,
		UserId:    140,
		Username:  "remote_unpush",
		Domain:    &remoteDomain,
		CreatedAt: &now,
		UpdatedAt: &now,
	}
	require.NoError(t, env.gormDB.Create(remoteAccount).Error)

	status := &models.Status{
		Id:         15001,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
		CreatedAt:  &now,
		UpdatedAt:  &now,
	}
	require.NoError(t, env.gormDB.Create(status).Error)
	require.NoError(t, env.gormDB.Create(&models.StatusToAccount{
		StatusId:  15001,
		AccountId: 140,
		CreatedAt: &now,
	}).Error)

	// Unpush 不应报错（远程账户被过滤，accounts 为空）
	err := Unpush(status, false)
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// 事件处理器（init 中注册的 "status"/"*" handler）
// ---------------------------------------------------------------------------

// nil Data 不应 panic。
func TestEventHandler_NilData(t *testing.T) {
	assert.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "after_create", Data: nil})
	})
}

// nil status（Data 非 nil 但类型断言后为 nil）不应 panic。
func TestEventHandler_NilStatus(t *testing.T) {
	assert.NotPanics(t, func() {
		var status *models.Status
		event.Trigger("status", event.Event{Name: "after_create", Data: status})
	})
}

// A direct event without recipient links must be harmless.
func TestEventHandler_DirectWithoutRecipients(t *testing.T) {
	setupMentionEnv(t)

	status := &models.Status{
		Id:         14001,
		AccountId:  1,
		Visibility: models.StatusVisibilityDirect,
	}

	assert.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "after_create", Data: status})
	})
	time.Sleep(100 * time.Millisecond)
}

// after_create 事件触发 Push。
func TestEventHandler_AfterCreate(t *testing.T) {
	setupMentionEnv(t)

	status := &models.Status{
		Id:         15001,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
	}

	assert.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "after_create", Data: status})
	})
	time.Sleep(100 * time.Millisecond)
}

// before_update 事件触发 Unpush（在 Push 之前清除旧提及）。
func TestEventHandler_BeforeUpdate(t *testing.T) {
	setupMentionEnv(t)

	status := &models.Status{
		Id:         15002,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
	}

	assert.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "before_update", Data: status})
	})
	time.Sleep(100 * time.Millisecond)
}

// after_update 事件触发 Push(update=true)。
func TestEventHandler_AfterUpdate(t *testing.T) {
	setupMentionEnv(t)

	status := &models.Status{
		Id:         15003,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
	}

	assert.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "after_update", Data: status})
	})
	time.Sleep(100 * time.Millisecond)
}

// after_delete 事件触发 Unpush。
func TestEventHandler_AfterDelete(t *testing.T) {
	setupMentionEnv(t)

	status := &models.Status{
		Id:         15004,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
	}

	assert.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "after_delete", Data: status})
	})
	time.Sleep(100 * time.Millisecond)
}

// ---------------------------------------------------------------------------
// 集成测试 —— 需要真实 Redis + 真实 DB（无 Redis 时自动跳过）
// ---------------------------------------------------------------------------

// integrationEnv 集成测试环境（真实 Redis + SQLite 内存库）。
type integrationEnv struct {
	gormDB    *gorm.DB
	redisCli  *redis.Client
	sqlHandle interface{ Close() error }
}

// setupIntegrationEnv 准备一个带有真实 Redis 与全局 DB 的集成测试环境。
// 如果真实 Redis 不可用，测试会被跳过。
func setupIntegrationEnv(t *testing.T) *integrationEnv {
	t.Helper()
	skipIfNoGlobalRedis(t)

	gormDB, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	sqlDB, err := gormDB.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	require.NoError(t, gormDB.AutoMigrate(integrationModels...))

	redisCli := db.GetRedis()
	require.NotNil(t, redisCli)

	database.SetTestDB(gormDB)
	feed.SetDefaultDBService(feed.NewDBService(gormDB))
	feed.SetDefaultService(feed.NewFeedService(feed.NewRedisClient(redisCli)))
	feed.ResetSubscriptionCache()

	env := &integrationEnv{
		gormDB:    gormDB,
		redisCli:  redisCli,
		sqlHandle: sqlDB,
	}

	t.Cleanup(func() {
		// 清理 Redis 测试数据
		_ = redisCli.FlushDB(context.Background()).Err()
		database.ResetTestDB()
		_ = sqlDB.Close()
	})

	return env
}

// Rebuild 应清空并重建提及时间线。
func TestMentionTimeline_Rebuild_RealRedis(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	env := setupIntegrationEnv(t)
	gormDB := env.gormDB

	// 1. 创建用户与账号
	user := &models.User{UUID: "mention-rebuild-user", UserName: "mention_rebuild_user", EmailHash: "mention_rebuild_hash"}
	require.NoError(t, gormDB.Create(user).Error)
	account := &models.Account{UserId: user.Id, Username: "mention_rebuild_user"}
	require.NoError(t, gormDB.Create(account).Error)

	// 2. 创建提及该账号的 status
	now := time.Now()
	status := &models.Status{AccountId: account.Id, Visibility: models.StatusVisibilityPublic, CreatedAt: &now}
	require.NoError(t, gormDB.Create(status).Error)

	// 建立 status_to_accounts 关联（account 被 status 提及）
	require.NoError(t, gormDB.Create(&models.StatusToAccount{
		StatusId:  status.Id,
		AccountId: account.Id,
		CreatedAt: &now,
	}).Error)

	// 3. 执行 Rebuild
	timeline := &MentionTimeline{Account: account}
	err := timeline.Rebuild()
	require.NoError(t, err)

	// 4. 验证 Redis 中时间线包含 status
	ctx := context.Background()
	key := "feed:mention:" + strconv.FormatUint(account.Id, 10)
	members, err := env.redisCli.ZRange(ctx, key, 0, -1).Result()
	require.NoError(t, err)

	idStrs := make(map[string]bool, len(members))
	for _, m := range members {
		idStrs[m] = true
	}
	assert.True(t, idStrs[strconv.FormatUint(status.Id, 10)], "被提及的 status 应在时间线")
}

// Rebuild 无数据时应正常完成。
func TestMentionTimeline_Rebuild_Empty(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	env := setupIntegrationEnv(t)
	gormDB := env.gormDB

	user := &models.User{UUID: "mention-rebuild-empty", UserName: "mention_rebuild_empty", EmailHash: "mention_rebuild_empty_hash"}
	require.NoError(t, gormDB.Create(user).Error)
	account := &models.Account{UserId: user.Id, Username: "mention_rebuild_empty"}
	require.NoError(t, gormDB.Create(account).Error)

	timeline := &MentionTimeline{Account: account}
	err := timeline.Rebuild()
	require.NoError(t, err)
}

// GetTimelineContent 应返回提及该账号的 status。
func TestGetTimelineContent_RealRedis(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	env := setupIntegrationEnv(t)
	gormDB := env.gormDB

	user := &models.User{UUID: "mention-gtc-user", UserName: "mention_gtc_user", EmailHash: "mention_gtc_hash"}
	require.NoError(t, gormDB.Create(user).Error)
	account := &models.Account{UserId: user.Id, Username: "mention_gtc_user"}
	require.NoError(t, gormDB.Create(account).Error)

	now := time.Now()
	status := &models.Status{AccountId: account.Id, Visibility: models.StatusVisibilityPublic, CreatedAt: &now}
	require.NoError(t, gormDB.Create(status).Error)

	require.NoError(t, gormDB.Create(&models.StatusToAccount{
		StatusId:  status.Id,
		AccountId: account.Id,
		CreatedAt: &now,
	}).Error)

	// 调用 GetTimelineContent（第一页会触发 Rebuild）
	statuses, err := GetTimelineContent(account, nil)
	require.NoError(t, err)

	idSet := make(map[uint64]bool, len(statuses))
	for _, s := range statuses {
		idSet[s.Id] = true
	}
	assert.True(t, idSet[status.Id], "被提及的 status 应在时间线内容中")
}

// GetTimelineContent 带 Limit 参数应实现分页。
func TestGetTimelineContent_WithLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	env := setupIntegrationEnv(t)
	gormDB := env.gormDB

	user := &models.User{UUID: "mention-gtc-limit", UserName: "mention_gtc_limit", EmailHash: "mention_gtc_limit_hash"}
	require.NoError(t, gormDB.Create(user).Error)
	account := &models.Account{UserId: user.Id, Username: "mention_gtc_limit"}
	require.NoError(t, gormDB.Create(account).Error)

	now := time.Now()
	for i := 0; i < 10; i++ {
		s := &models.Status{AccountId: account.Id, Visibility: models.StatusVisibilityPublic, CreatedAt: &now}
		require.NoError(t, gormDB.Create(s).Error)
		require.NoError(t, gormDB.Create(&models.StatusToAccount{
			StatusId:  s.Id,
			AccountId: account.Id,
			CreatedAt: &now,
		}).Error)
	}

	// 先 Rebuild
	timeline := &MentionTimeline{Account: account}
	require.NoError(t, timeline.Rebuild())

	params := &feed.ListParam{Limit: 3}
	statuses, err := GetTimelineContent(account, params)
	require.NoError(t, err)
	assert.Len(t, statuses, 3, "Limit=3 应返回 3 条")
}

// GetTimelineContent 无数据时应返回空切片。
func TestGetTimelineContent_EmptyTimeline(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	env := setupIntegrationEnv(t)
	gormDB := env.gormDB

	user := &models.User{UUID: "mention-gtc-empty", UserName: "mention_gtc_empty", EmailHash: "mention_gtc_empty_hash"}
	require.NoError(t, gormDB.Create(user).Error)
	account := &models.Account{UserId: user.Id, Username: "mention_gtc_empty"}
	require.NoError(t, gormDB.Create(account).Error)

	statuses, err := GetTimelineContent(account, nil)
	require.NoError(t, err)
	assert.Empty(t, statuses)
}

// Push 集成测试：有真实提及时应写入真实 Redis 时间线。
func TestPush_RealRedis(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	env := setupIntegrationEnv(t)
	gormDB := env.gormDB
	ctx := context.Background()

	user := &models.User{UUID: "mention-push-user", UserName: "mention_push_user", EmailHash: "mention_push_hash"}
	require.NoError(t, gormDB.Create(user).Error)
	account := &models.Account{UserId: user.Id, Username: "mention_push_user"}
	require.NoError(t, gormDB.Create(account).Error)

	now := time.Now()
	status := &models.Status{AccountId: account.Id, Visibility: models.StatusVisibilityPublic, CreatedAt: &now}
	require.NoError(t, gormDB.Create(status).Error)
	require.NoError(t, gormDB.Create(&models.StatusToAccount{
		StatusId:  status.Id,
		AccountId: account.Id,
		CreatedAt: &now,
	}).Error)

	err := Push(status, false, false)
	require.NoError(t, err)

	key := "feed:mention:" + strconv.FormatUint(account.Id, 10)
	members, err := env.redisCli.ZRange(ctx, key, 0, -1).Result()
	require.NoError(t, err)

	found := false
	for _, m := range members {
		if m == strconv.FormatUint(status.Id, 10) {
			found = true
			break
		}
	}
	assert.True(t, found, "被提及的 status 应写入真实 Redis 时间线")
}

// Unpush 集成测试：应从真实 Redis 时间线移除 status。
func TestUnpush_RealRedis(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	env := setupIntegrationEnv(t)
	gormDB := env.gormDB
	ctx := context.Background()

	user := &models.User{UUID: "mention-unpush-user", UserName: "mention_unpush_user", EmailHash: "mention_unpush_hash"}
	require.NoError(t, gormDB.Create(user).Error)
	account := &models.Account{UserId: user.Id, Username: "mention_unpush_user"}
	require.NoError(t, gormDB.Create(account).Error)

	now := time.Now()
	status := &models.Status{AccountId: account.Id, Visibility: models.StatusVisibilityPublic, CreatedAt: &now}
	require.NoError(t, gormDB.Create(status).Error)
	require.NoError(t, gormDB.Create(&models.StatusToAccount{
		StatusId:  status.Id,
		AccountId: account.Id,
		CreatedAt: &now,
	}).Error)

	// 先 Push
	err := Push(status, false, false)
	require.NoError(t, err)

	key := "feed:mention:" + strconv.FormatUint(account.Id, 10)
	// 确认已写入
	members, err := env.redisCli.ZRange(ctx, key, 0, -1).Result()
	require.NoError(t, err)
	found := false
	for _, m := range members {
		if m == strconv.FormatUint(status.Id, 10) {
			found = true
			break
		}
	}
	require.True(t, found, "Push 后 status 应在时间线")

	// 再 Unpush
	err = Unpush(status, false)
	require.NoError(t, err)

	// 确认已移除
	members, err = env.redisCli.ZRange(ctx, key, 0, -1).Result()
	require.NoError(t, err)
	for _, m := range members {
		if m == strconv.FormatUint(status.Id, 10) {
			t.Fatal("Unpush 后 status 不应在时间线")
		}
	}
}
