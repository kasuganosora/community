package search

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

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
	AccountID   string `json:"account_id"`
	Username    string `json:"username"`
	Visibility  string `json:"visibility"`
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
	client   meilisearch.ServiceManager
	host     string
	apiKey   string
	enabled  bool
	initOnce sync.Once
}

var (
	meiliClient *MeilisearchClient
	clientOnce  sync.Once
)

// GetMeilisearchClient 获取 Meilisearch 客户端单例.
func GetMeilisearchClient() *MeilisearchClient {
	clientOnce.Do(func() {
		meiliClient = &MeilisearchClient{
			host:    getMeilisearchHost(),
			apiKey:  getMeilisearchAPIKey(),
			enabled: isMeilisearchEnabled(),
		}
		if meiliClient.enabled {
			meiliClient.init()
		}
	})

	return meiliClient
}

func getMeilisearchHost() string {
	if host, ok := config.C.Other["MEILISEARCH_HOST"]; ok {
		return host
	}

	return "http://localhost:7700" //nolint:goconst
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

// init 初始化 Meilisearch 客户端.
func (m *MeilisearchClient) init() {
	m.initOnce.Do(func() {
		m.client = meilisearch.New(m.host, meilisearch.WithAPIKey(m.apiKey))

		// 检查连接
		_, err := m.client.Version()
		if err != nil {
			log.Error("Failed to connect to Meilisearch: %v", err)

			m.enabled = false

			return
		}

		log.Info("Meilisearch connected successfully")

		// 初始化索引
		m.initIndexes()
	})
}

// initIndexes 初始化索引设置.
func (m *MeilisearchClient) initIndexes() {
	// 账户索引
	m.createIndexWithSettings(IndexAccounts, &meilisearch.Settings{
		SearchableAttributes: []string{"username", "display_name", "bio", "domain"},
		FilterableAttributes: []string{"created_at"}, //nolint:goconst
		SortableAttributes:   []string{"created_at"},
		RankingRules:         []string{"words", "typo", "proximity", "attribute", "sort", "exactness"}, //nolint:goconst
	})

	// 嘟文索引
	m.createIndexWithSettings(IndexStatuses, &meilisearch.Settings{
		SearchableAttributes: []string{"content", "spoiler_text", "username"},
		FilterableAttributes: []string{"account_id", "visibility", "created_at"},
		SortableAttributes:   []string{"created_at"},
		RankingRules:         []string{"words", "typo", "proximity", "attribute", "sort", "exactness"},
	})

	// 标签索引
	m.createIndexWithSettings(IndexHashtags, &meilisearch.Settings{
		SearchableAttributes: []string{"name"},
		FilterableAttributes: []string{"status_count", "created_at"},
		SortableAttributes:   []string{"status_count", "created_at"},
		RankingRules:         []string{"words", "typo", "proximity", "attribute", "sort", "exactness"},
	})
}

// createIndexWithSettings 创建带设置的索引.
func (m *MeilisearchClient) createIndexWithSettings(indexUID string, settings *meilisearch.Settings) {
	index := m.client.Index(indexUID)

	// 更新设置
	_, err := index.UpdateSettings(settings)
	if err != nil {
		log.Error("Failed to update settings for index %s: %v", indexUID, err)
	}
}

// IsEnabled 检查是否启用.
func (m *MeilisearchClient) IsEnabled() bool {
	return m.enabled && m.client != nil
}

// IndexAccount 索引账户.
func (m *MeilisearchClient) IndexAccount(doc *AccountSearchDocument) error {
	if !m.IsEnabled() {
		return nil
	}

	index := m.client.Index(IndexAccounts)
	_, err := index.AddDocuments(doc, nil)

	return err
}

// IndexStatus 索引嘟文.
func (m *MeilisearchClient) IndexStatus(doc *StatusSearchDocument) error {
	if !m.IsEnabled() {
		return nil
	}

	index := m.client.Index(IndexStatuses)
	_, err := index.AddDocuments(doc, nil)

	return err
}

// IndexHashtag 索引标签.
func (m *MeilisearchClient) IndexHashtag(doc *HashtagSearchDocument) error {
	if !m.IsEnabled() {
		return nil
	}

	index := m.client.Index(IndexHashtags)
	_, err := index.AddDocuments(doc, nil)

	return err
}

// DeleteAccount 删除账户索引.
func (m *MeilisearchClient) DeleteAccount(id string) error {
	if !m.IsEnabled() {
		return nil
	}

	index := m.client.Index(IndexAccounts)
	_, err := index.DeleteDocument(id, nil)

	return err
}

// DeleteStatus 删除嘟文索引.
func (m *MeilisearchClient) DeleteStatus(id string) error {
	if !m.IsEnabled() {
		return nil
	}

	index := m.client.Index(IndexStatuses)
	_, err := index.DeleteDocument(id, nil)

	return err
}

// DeleteHashtag 删除标签索引.
func (m *MeilisearchClient) DeleteHashtag(id string) error {
	if !m.IsEnabled() {
		return nil
	}

	index := m.client.Index(IndexHashtags)
	_, err := index.DeleteDocument(id, nil)

	return err
}

// SearchAccounts 搜索账户.
func (m *MeilisearchClient) SearchAccounts(query string, limit, offset int64, followingIDs []string) ([]string, error) {
	if !m.IsEnabled() {
		return nil, errors.New("meilisearch not enabled")
	}

	var filter string
	if len(followingIDs) > 0 {
		filter = fmt.Sprintf("id IN [%s]", strings.Join(followingIDs, ", "))
	}

	result, err := m.client.Index(IndexAccounts).Search(query, &meilisearch.SearchRequest{
		Limit:  limit,
		Offset: offset,
		Filter: filter,
	})
	if err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(result.Hits))

	for _, hit := range result.Hits {
		// Hit is map[string]json.RawMessage, use Decode method
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

	var filters []string
	if accountID != "" {
		filters = append(filters, "account_id = "+accountID)
	}

	if len(visibility) > 0 {
		visFilter := make([]string, len(visibility))
		for i, v := range visibility {
			visFilter[i] = "visibility = " + v
		}

		filters = append(filters, fmt.Sprintf("(%s)", strings.Join(visFilter, " OR ")))
	}

	var filter string
	if len(filters) > 0 {
		filter = strings.Join(filters, " AND ")
	}

	result, err := m.client.Index(IndexStatuses).Search(query, &meilisearch.SearchRequest{
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

	result, err := m.client.Index(IndexHashtags).Search(query, &meilisearch.SearchRequest{
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
	_, err := index.AddDocumentsInBatches(docs, 1000, nil)

	return err
}

// BatchIndexStatuses 批量索引嘟文.
func (m *MeilisearchClient) BatchIndexStatuses(docs []*StatusSearchDocument) error {
	if !m.IsEnabled() || len(docs) == 0 {
		return nil
	}

	index := m.client.Index(IndexStatuses)
	_, err := index.AddDocumentsInBatches(docs, 1000, nil)

	return err
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

	stats, err := m.client.GetStats()
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

	_, err := m.client.Health()

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
