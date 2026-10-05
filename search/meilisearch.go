package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/log"
	"github.com/meilisearch/meilisearch-go"
)

// Meilisearch 索引名称.
const (
	IndexAccounts = "accounts"
	IndexStatuses = "statuses"
	IndexHashtags = "hashtags"
)

// 搜索和连接相关的安全限制常量.
const (
	// maxSearchLimit 限制单次搜索返回的最大结果数量，防止滥用.
	maxSearchLimit = 40
	// defaultSearchTimeout 是每次搜索请求的超时时间。超时后调用方改走数据库，
	// 所以这里故意短，避免搜索引擎无响应时把整次搜索拖住。
	defaultSearchTimeout = 4 * time.Second
	// defaultHTTPTimeout 是 Meilisearch HTTP 客户端的整体超时时间.
	defaultHTTPTimeout = 30 * time.Second
	// initCheckTimeout 是初始化时连接检查的超时时间.
	initCheckTimeout = 5 * time.Second
	// indexOpsTimeout 是索引写操作的超时时间。建索引、改设置和写入都要等任务完成，
	// 所以比一次搜索宽裕，避免设置还没生效就把文档写进去。
	indexOpsTimeout = 60 * time.Second
	// batchSize 是批量索引时的批次大小.
	batchSize = 1000
)

// SearchDocument 搜索文档接口.
type SearchDocument interface {
	GetID() string
	GetType() string
}

// AccountSearchDocument 账户搜索文档.
type AccountSearchDocument struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Domain      string `json:"domain"`
	Bio         string `json:"bio"`
	CreatedAt   int64  `json:"created_at"`
}

func (d *AccountSearchDocument) GetID() string {
	return d.ID
}

func (d *AccountSearchDocument) GetType() string {
	return "account"
}

// StatusSearchDocument 嘟文搜索文档.
type StatusSearchDocument struct {
	ID          string `json:"id"`
	Content     string `json:"content"`
	SpoilerText string `json:"spoiler_text"`
	AccountID   uint64 `json:"account_id"`
	Username    string `json:"username"`
	Visibility  int    `json:"visibility"`
	CreatedAt   int64  `json:"created_at"`
}

func (d *StatusSearchDocument) GetID() string {
	return d.ID
}

func (d *StatusSearchDocument) GetType() string {
	return "status"
}

// HashtagSearchDocument 标签搜索文档.
type HashtagSearchDocument struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	StatusCount int64  `json:"status_count"`
	CreatedAt   int64  `json:"created_at"`
}

func (d *HashtagSearchDocument) GetID() string {
	return d.ID
}

func (d *HashtagSearchDocument) GetType() string {
	return "hashtag"
}

// SearchResult 搜索结果.
type SearchResult struct {
	Accounts []string `json:"accounts"`
	Statuses []string `json:"statuses"`
	Hashtags []string `json:"hashtags"`
}

// MeilisearchClient Meilisearch 客户端封装.
type MeilisearchClient struct {
	client  meilisearch.ServiceManager
	host    string
	apiKey  string
	enabled bool
	indexed bool
	mu      sync.RWMutex // 保护 enabled 字段的并发读写
}

var (
	meiliClient *MeilisearchClient
	clientOnce  sync.Once
)

// GetMeilisearchClient 获取 Meilisearch 客户端单例.
//
// MEILISEARCH_ENABLED 未设置或为 auto 时，后台探测默认地址：通了就建索引并
// 回填，断了就关掉并继续探测。显式 true 会在返回前先连一次。false 完全不用。
func GetMeilisearchClient() *MeilisearchClient {
	clientOnce.Do(func() {
		meiliClient = &MeilisearchClient{
			host:   getMeilisearchHost(),
			apiKey: getMeilisearchAPIKey(),
		}
		switch meiliMode() {
		case "off":
			meiliClient.setEnabled(false)
		case "on":
			meiliClient.bootstrap(true)
		default:
			go meiliClient.bootstrap(false)
		}
	})

	return meiliClient
}

func meiliMode() string {
	enabled, ok := config.C.Other["MEILISEARCH_ENABLED"]
	if !ok {
		return "auto"
	}
	switch strings.ToLower(strings.TrimSpace(enabled)) {
	case "", "auto":
		return "auto"
	case "1", "true", "on", "yes":
		return "on"
	default:
		return "off"
	}
}

func getMeilisearchHost() string {
	if host, ok := config.C.Other["MEILISEARCH_HOST"]; ok {
		return host
	}

	return "http://127.0.0.1:7700" //nolint:goconst
}

func getMeilisearchAPIKey() string {
	if apiKey, ok := config.C.Other["MEILISEARCH_API_KEY"]; ok {
		return apiKey
	}

	return ""
}

func isMeilisearchEnabled() bool {
	if enabled, ok := config.C.Other["MEILISEARCH_ENABLED"]; ok {
		return strings.ToLower(enabled) == "true"
	}

	return false
}

// bootstrap 建立客户端并在后台保持连接。syncFirst 为 true 时先阻塞探测一次。
func (m *MeilisearchClient) bootstrap(syncFirst bool) {
	m.ensureClient()
	if syncFirst {
		m.tryConnect()
	}
	go m.reconnectLoop()
}

func (m *MeilisearchClient) ensureClient() {
	if m.client != nil {
		return
	}
	m.client = meilisearch.New(m.host,
		meilisearch.WithAPIKey(m.apiKey),
		meilisearch.WithCustomClient(&http.Client{
			Timeout: defaultHTTPTimeout,
		}))
}

// tryConnect 在不可用和可用之间切换。已经连着时不重复建索引。
func (m *MeilisearchClient) tryConnect() {
	m.ensureClient()
	ctx, cancel := context.WithTimeout(context.Background(), initCheckTimeout)
	_, err := m.client.VersionWithContext(ctx)
	cancel()
	if err != nil {
		if m.IsEnabled() {
			log.Warn("Meilisearch unavailable, using database search: %v", err)
			m.setEnabled(false)
		}
		return
	}
	// 从断开恢复时重新应用设置。只在「已经启用」的轮询里跳过，避免每 15 秒重建设置。
	if m.IsEnabled() {
		return
	}
	if err = m.initIndexes(); err != nil {
		log.Warn("Meilisearch index setup failed, will retry: %v", err)
		return
	}
	m.indexed = true
	log.Info("Meilisearch connected at %s", m.host)
	m.setEnabled(true)
	if hook := currentReadyHook(); hook != nil {
		go hook()
	}
}

// NoteFailure 在一次搜索失败后确认引擎是否还活着，死了就立刻改走数据库。
func (m *MeilisearchClient) NoteFailure() {
	if !m.IsEnabled() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	_, err := m.client.VersionWithContext(ctx)
	cancel()
	if err == nil {
		return
	}
	log.Warn("Meilisearch search failed, using database search: %v", err)
	m.setEnabled(false)
}

var (
	readyMu        sync.RWMutex
	readyHook      func()
	accountIndexMu sync.RWMutex
	accountIndexer func(uint64)
)

// SetReadyHook 注册连接成功（含断线重连）后的回调，用来回填索引。
func SetReadyHook(fn func()) {
	readyMu.Lock()
	readyHook = fn
	readyMu.Unlock()
}

func currentReadyHook() func() {
	readyMu.RLock()
	defer readyMu.RUnlock()
	return readyHook
}

// SetAccountIndexer 注册账户资料变更后的索引函数。
func SetAccountIndexer(fn func(uint64)) {
	accountIndexMu.Lock()
	accountIndexer = fn
	accountIndexMu.Unlock()
}

// QueueAccountIndex 在资料更新后异步刷新该账户的搜索文档。引擎没开时什么都不做。
func QueueAccountIndex(id uint64) {
	if id == 0 {
		return
	}
	accountIndexMu.RLock()
	fn := accountIndexer
	accountIndexMu.RUnlock()
	if fn != nil {
		go fn(id)
	}
}

// reconnectLoop 持续探测。连上后保持启用，断开后自动降级，恢复后再建索引并回填。
func (m *MeilisearchClient) reconnectLoop() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	m.tryConnect()
	for range ticker.C {
		m.tryConnect()
	}
}

// setEnabled 线程安全地设置 enabled 标志.
func (m *MeilisearchClient) setEnabled(v bool) {
	m.mu.Lock()
	m.enabled = v
	m.mu.Unlock()
}

// initIndexes 初始化索引设置. 索引不存在时先创建，并等到设置任务成功再返回。
func (m *MeilisearchClient) initIndexes() error {
	specs := []struct {
		uid      string
		settings *meilisearch.Settings
	}{
		{IndexAccounts, &meilisearch.Settings{
			SearchableAttributes: []string{"username", "display_name", "bio", "domain"},
			FilterableAttributes: []string{"id", "created_at"}, //nolint:goconst
			SortableAttributes:   []string{"created_at"},
			RankingRules:         []string{"words", "typo", "proximity", "attribute", "sort", "exactness"}, //nolint:goconst
		}},
		{IndexStatuses, &meilisearch.Settings{
			SearchableAttributes: []string{"content", "spoiler_text", "username"},
			FilterableAttributes: []string{"account_id", "visibility", "created_at"},
			SortableAttributes:   []string{"created_at"},
			RankingRules:         []string{"words", "typo", "proximity", "attribute", "sort", "exactness"},
		}},
		{IndexHashtags, &meilisearch.Settings{
			SearchableAttributes: []string{"name"},
			FilterableAttributes: []string{"status_count", "created_at"},
			SortableAttributes:   []string{"status_count", "created_at"},
			RankingRules:         []string{"words", "typo", "proximity", "attribute", "sort", "exactness"},
		}},
	}
	var first error
	for _, spec := range specs {
		if err := m.createIndexWithSettings(spec.uid, spec.settings); err != nil && first == nil {
			first = err
		}
	}
	return first
}

type taskWaiter interface {
	WaitForTaskWithContext(ctx context.Context, taskUID int64, interval time.Duration) (*meilisearch.Task, error)
}

func finishTask(ctx context.Context, waiter taskWaiter, task *meilisearch.TaskInfo) error {
	if task == nil {
		return nil
	}
	done, err := waiter.WaitForTaskWithContext(ctx, task.TaskUID, 50*time.Millisecond)
	if err != nil {
		return err
	}
	if done != nil && done.Status != meilisearch.TaskStatusSucceeded {
		return fmt.Errorf("meilisearch task %d %s", task.TaskUID, done.Status)
	}
	return nil
}

func finishTasks(ctx context.Context, waiter taskWaiter, tasks []meilisearch.TaskInfo) error {
	for i := range tasks {
		if err := finishTask(ctx, waiter, &tasks[i]); err != nil {
			return err
		}
	}
	return nil
}

// createIndexWithSettings 创建带设置的索引.
func (m *MeilisearchClient) createIndexWithSettings(indexUID string, settings *meilisearch.Settings) error {
	ctx, cancel := context.WithTimeout(context.Background(), indexOpsTimeout)
	defer cancel()

	if _, err := m.client.GetIndexWithContext(ctx, indexUID); err != nil {
		task, cerr := m.client.CreateIndexWithContext(ctx, &meilisearch.IndexConfig{
			Uid:        indexUID,
			PrimaryKey: "id",
		})
		if cerr != nil {
			log.Error("Failed to create index %s: %v", indexUID, cerr)
			return cerr
		}
		if werr := finishTask(ctx, m.client, task); werr != nil {
			if _, gerr := m.client.GetIndexWithContext(ctx, indexUID); gerr != nil {
				log.Error("Failed to create index %s: %v", indexUID, werr)
				return werr
			}
		}
	}

	index := m.client.Index(indexUID)
	task, err := index.UpdateSettingsWithContext(ctx, settings)
	if err != nil {
		log.Error("Failed to update settings for index %s: %v", indexUID, err)
		return err
	}
	if err = finishTask(ctx, index, task); err != nil {
		log.Error("Failed to apply settings for index %s: %v", indexUID, err)
		return err
	}
	return nil
}

// IsEnabled 检查是否启用.
func (m *MeilisearchClient) IsEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.enabled && m.client != nil
}

// IndexAccount 索引账户.
func (m *MeilisearchClient) IndexAccount(doc *AccountSearchDocument) error {
	if !m.IsEnabled() {
		return nil
	}

	if doc == nil || doc.ID == "" {
		return errors.New("invalid document: id is required")
	}

	index := m.client.Index(IndexAccounts)
	ctx, cancel := context.WithTimeout(context.Background(), indexOpsTimeout)
	defer cancel()
	// AddDocuments 需要传入数组，单个文档须包装为切片
	task, err := index.AddDocumentsWithContext(ctx, []*AccountSearchDocument{doc}, nil)
	if err != nil {
		return err
	}
	return finishTask(ctx, index, task)
}

// IndexStatus 索引嘟文.
func (m *MeilisearchClient) IndexStatus(doc *StatusSearchDocument) error {
	if !m.IsEnabled() {
		return nil
	}

	if doc == nil || doc.ID == "" {
		return errors.New("invalid document: id is required")
	}

	index := m.client.Index(IndexStatuses)
	ctx, cancel := context.WithTimeout(context.Background(), indexOpsTimeout)
	defer cancel()
	task, err := index.AddDocumentsWithContext(ctx, []*StatusSearchDocument{doc}, nil)
	if err != nil {
		return err
	}
	return finishTask(ctx, index, task)
}

// IndexHashtag 索引标签.
func (m *MeilisearchClient) IndexHashtag(doc *HashtagSearchDocument) error {
	if !m.IsEnabled() {
		return nil
	}

	if doc == nil || doc.ID == "" {
		return errors.New("invalid document: id is required")
	}

	index := m.client.Index(IndexHashtags)
	ctx, cancel := context.WithTimeout(context.Background(), indexOpsTimeout)
	defer cancel()
	task, err := index.AddDocumentsWithContext(ctx, []*HashtagSearchDocument{doc}, nil)
	if err != nil {
		return err
	}
	return finishTask(ctx, index, task)
}

// DeleteAccount 删除账户索引.
func (m *MeilisearchClient) DeleteAccount(id string) error {
	if !m.IsEnabled() {
		return nil
	}

	if id == "" {
		return errors.New("id is required")
	}

	index := m.client.Index(IndexAccounts)
	ctx, cancel := context.WithTimeout(context.Background(), indexOpsTimeout)
	defer cancel()
	_, err := index.DeleteDocumentWithContext(ctx, id, nil)

	return err
}

// DeleteStatus 删除嘟文索引.
func (m *MeilisearchClient) DeleteStatus(id string) error {
	if !m.IsEnabled() {
		return nil
	}

	if id == "" {
		return errors.New("id is required")
	}

	index := m.client.Index(IndexStatuses)
	ctx, cancel := context.WithTimeout(context.Background(), indexOpsTimeout)
	defer cancel()
	_, err := index.DeleteDocumentWithContext(ctx, id, nil)

	return err
}

// DeleteHashtag 删除标签索引.
func (m *MeilisearchClient) DeleteHashtag(id string) error {
	if !m.IsEnabled() {
		return nil
	}

	if id == "" {
		return errors.New("id is required")
	}

	index := m.client.Index(IndexHashtags)
	ctx, cancel := context.WithTimeout(context.Background(), indexOpsTimeout)
	defer cancel()
	_, err := index.DeleteDocumentWithContext(ctx, id, nil)

	return err
}

// sanitizeID 验证 ID 是否为纯数字，防止过滤器注入.
// Meilisearch 过滤器使用表达式 DSL（类似 SQL WHERE），
// 如果将未经验证的用户输入直接拼接到过滤器字符串中，
// 攻击者可以注入任意过滤表达式来绕过权限控制.
func sanitizeID(id string) bool {
	if id == "" {
		return false
	}
	for _, c := range id {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// sanitizeIDs 验证并过滤 ID 列表，仅保留合法的数字 ID.
func sanitizeIDs(ids []string) []string {
	cleaned := make([]string, 0, len(ids))
	for _, id := range ids {
		if sanitizeID(id) {
			cleaned = append(cleaned, id)
		}
	}
	return cleaned
}

// clampLimitOffset 将 limit 和 offset 限制在安全范围内.
func clampLimitOffset(limit, offset int64) (int64, int64) {
	if limit < 0 {
		limit = 0
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// SearchAccounts 搜索账户.
func (m *MeilisearchClient) SearchAccounts(query string, limit, offset int64, followingIDs []string) ([]string, error) {
	if !m.IsEnabled() {
		return nil, errors.New("meilisearch not enabled")
	}

	limit, offset = clampLimitOffset(limit, offset)

	// 安全：验证 followingIDs 为纯数字，防止过滤器注入
	var filter string
	if len(followingIDs) > 0 {
		safeIDs := sanitizeIDs(followingIDs)
		if len(safeIDs) > 0 {
			filter = fmt.Sprintf("id IN [%s]", strings.Join(safeIDs, ", "))
		} else {
			// 所有 ID 都不合法，返回空结果
			return []string{}, nil
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultSearchTimeout)
	defer cancel()
	result, err := m.client.Index(IndexAccounts).SearchWithContext(ctx, query, &meilisearch.SearchRequest{
		Limit:  limit,
		Offset: offset,
		Filter: filter,
	})
	if err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(result.Hits))

	for _, hit := range result.Hits {
		var doc struct {
			ID string `json:"id"`
		}
		err := hit.DecodeInto(&doc)

		if err == nil && doc.ID != "" {
			ids = append(ids, doc.ID)
		}
	}

	return ids, nil
}

// SearchStatuses 搜索嘟文.
func (m *MeilisearchClient) SearchStatuses(query string, limit, offset int64, accountID string, visibility []string) ([]string, error) {
	if !m.IsEnabled() {
		return nil, errors.New("meilisearch not enabled")
	}

	limit, offset = clampLimitOffset(limit, offset)

	var filters []string

	// 安全：验证 accountID 为纯数字，防止过滤器注入
	if accountID != "" {
		if !sanitizeID(accountID) {
			return nil, errors.New("invalid account_id")
		}
		filters = append(filters, "account_id = "+accountID)
	}

	// 安全：验证每个 visibility 值为纯数字，防止过滤器注入
	if len(visibility) > 0 {
		visFilter := make([]string, 0, len(visibility))
		for _, v := range visibility {
			if !sanitizeID(v) {
				continue // 跳过非法值
			}
			visFilter = append(visFilter, "visibility = "+v)
		}

		if len(visFilter) > 0 {
			filters = append(filters, fmt.Sprintf("(%s)", strings.Join(visFilter, " OR ")))
		}
	}

	var filter string
	if len(filters) > 0 {
		filter = strings.Join(filters, " AND ")
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultSearchTimeout)
	defer cancel()
	result, err := m.client.Index(IndexStatuses).SearchWithContext(ctx, query, &meilisearch.SearchRequest{
		Limit:  limit,
		Offset: offset,
		Filter: filter,
		Sort:   []string{"created_at:desc"},
	})
	if err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(result.Hits))

	for _, hit := range result.Hits {
		var doc struct {
			ID string `json:"id"`
		}
		err := hit.DecodeInto(&doc)

		if err == nil && doc.ID != "" {
			ids = append(ids, doc.ID)
		}
	}

	return ids, nil
}

// SearchHashtags 搜索标签.
func (m *MeilisearchClient) SearchHashtags(query string, limit, offset int64) ([]string, error) {
	if !m.IsEnabled() {
		return nil, errors.New("meilisearch not enabled")
	}

	limit, offset = clampLimitOffset(limit, offset)

	ctx, cancel := context.WithTimeout(context.Background(), defaultSearchTimeout)
	defer cancel()
	result, err := m.client.Index(IndexHashtags).SearchWithContext(ctx, query, &meilisearch.SearchRequest{
		Limit:  limit,
		Offset: offset,
		Sort:   []string{"status_count:desc"},
	})
	if err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(result.Hits))

	for _, hit := range result.Hits {
		var doc struct {
			Name string `json:"name"`
		}
		err := hit.DecodeInto(&doc)

		if err == nil && doc.Name != "" {
			ids = append(ids, doc.Name)
		}
	}

	return ids, nil
}

// BatchIndexAccounts 批量索引账户.
func (m *MeilisearchClient) BatchIndexAccounts(docs []*AccountSearchDocument) error {
	if !m.IsEnabled() || len(docs) == 0 {
		return nil
	}

	index := m.client.Index(IndexAccounts)
	ctx, cancel := context.WithTimeout(context.Background(), indexOpsTimeout)
	defer cancel()
	tasks, err := index.AddDocumentsInBatchesWithContext(ctx, docs, batchSize, nil)
	if err != nil {
		return err
	}
	return finishTasks(ctx, index, tasks)
}

// BatchIndexStatuses 批量索引嘟文.
func (m *MeilisearchClient) BatchIndexStatuses(docs []*StatusSearchDocument) error {
	if !m.IsEnabled() || len(docs) == 0 {
		return nil
	}

	index := m.client.Index(IndexStatuses)
	ctx, cancel := context.WithTimeout(context.Background(), indexOpsTimeout)
	defer cancel()
	tasks, err := index.AddDocumentsInBatchesWithContext(ctx, docs, batchSize, nil)
	if err != nil {
		return err
	}
	return finishTasks(ctx, index, tasks)
}

// BatchIndexHashtags 批量索引标签。文档 id 用标签名，搜索直接返回名字。
func (m *MeilisearchClient) BatchIndexHashtags(docs []*HashtagSearchDocument) error {
	if !m.IsEnabled() || len(docs) == 0 {
		return nil
	}

	index := m.client.Index(IndexHashtags)
	ctx, cancel := context.WithTimeout(context.Background(), indexOpsTimeout)
	defer cancel()
	tasks, err := index.AddDocumentsInBatchesWithContext(ctx, docs, batchSize, nil)
	if err != nil {
		return err
	}
	return finishTasks(ctx, index, tasks)
}

// ParseSearchQuery 解析高级搜索语法
// 支持的语法:
// - from:username - 搜索特定用户的嘟文
// - has:media - 搜索包含媒体的嘟文
// - is:reply - 搜索回复
// - is:reblog - 搜索转发
// - before:YYYY-MM-DD - 搜索特定日期之前的嘟文
// - after:YYYY-MM-DD - 搜索特定日期之后的嘟文
// - #tag - 搜索包含特定标签的嘟文.
func ParseSearchQuery(query string) (text string, filters map[string]string) {
	filters = make(map[string]string)
	words := strings.Fields(query)
	textParts := make([]string, 0)

	for _, word := range words {
		if after, ok := strings.CutPrefix(word, "from:"); ok {
			filters["from"] = after
		} else if after, ok := strings.CutPrefix(word, "has:"); ok {
			filters["has"] = after
		} else if after, ok := strings.CutPrefix(word, "is:"); ok {
			filters["is"] = after
		} else if after, ok := strings.CutPrefix(word, "before:"); ok {
			filters["before"] = after
		} else if after, ok := strings.CutPrefix(word, "after:"); ok {
			filters["after"] = after
		} else if after, ok := strings.CutPrefix(word, "#"); ok {
			filters["tag"] = after
		} else {
			textParts = append(textParts, word)
		}
	}

	text = strings.Join(textParts, " ")

	return text, filters
}

// GetStats 获取索引统计信息.
func (m *MeilisearchClient) GetStats() (map[string]any, error) {
	if !m.IsEnabled() {
		return nil, errors.New("meilisearch not enabled")
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultSearchTimeout)
	defer cancel()
	stats, err := m.client.GetStatsWithContext(ctx, nil)
	if err != nil {
		return nil, err
	}

	result := make(map[string]any)
	result["database_size"] = stats.DatabaseSize
	result["last_update"] = stats.LastUpdate

	indexes := make(map[string]any)

	for name, indexStats := range stats.Indexes {
		indexInfo := map[string]any{
			"number_of_documents": indexStats.NumberOfDocuments,
			"is_indexing":         indexStats.IsIndexing,
			"field_distribution":  indexStats.FieldDistribution,
		}
		indexes[name] = indexInfo
	}

	result["indexes"] = indexes

	return result, nil
}

// HealthCheck 健康检查.
func (m *MeilisearchClient) HealthCheck() bool {
	if !m.IsEnabled() {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), initCheckTimeout)
	defer cancel()
	_, err := m.client.HealthWithContext(ctx)

	return err == nil
}

// ToJSON 辅助函数：转换为 JSON.
func ToJSON(v any) string {
	b, _ := json.Marshal(v) //nolint:errchkjson // v is always a concrete type here

	return string(b)
}

// StringToUint64 辅助函数：字符串转 uint64.
func StringToUint64(s string) uint64 {
	n, _ := strconv.ParseUint(s, 10, 64)

	return n
}
