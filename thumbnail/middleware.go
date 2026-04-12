package thumbnail

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/thumbnail/service"
)

var (
	// 匹配图片URL的正则
	imageURLRegex = regexp.MustCompile(`https?://[^\s"<>]+\.(jpg|jpeg|png|gif|webp)(\?[^\s"<>]*)?`)
)

// responseWriter 响应写入器包装器
type responseWriter struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

// Write 捕获写入的数据
func (w *responseWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

// WriteString 捕获写入的字符串
func (w *responseWriter) WriteString(s string) (int, error) {
	w.body.WriteString(s)
	return w.ResponseWriter.WriteString(s)
}

// ThumbnailMiddleware 缩略图中间件
// 自动处理响应中的远程图片URL
func ThumbnailMiddleware(host string) gin.HandlerFunc {
	return func(c *gin.Context) {
		svc := GetService()
		if svc == nil || !svc.IsEnabled() {
			c.Next()
			return
		}

		// 只处理 GET 请求的成功响应
		if c.Request.Method != "GET" {
			c.Next()
			return
		}

		// 检查是否需要处理（根据路径判断）
		if !shouldProcessPath(c.Request.URL.Path) {
			c.Next()
			return
		}

		// 创建响应捕获器
		writer := &responseWriter{
			ResponseWriter: c.Writer,
			body:           &bytes.Buffer{},
		}
		c.Writer = writer

		// 继续处理请求
		c.Next()

		// 只处理成功的 JSON 响应
		if c.Writer.Status() != http.StatusOK {
			return
		}

		contentType := c.Writer.Header().Get("Content-Type")
		if !strings.Contains(contentType, "application/json") {
			return
		}

		// 处理响应体中的 URL
		body := writer.body.Bytes()
		if len(body) == 0 {
			return
		}

		processed := processJSONResponse(body, svc, host)
		if processed != nil {
			// 修改响应体
			c.Writer.Header().Set("Content-Length", strconv.Itoa(len(processed)))
			_, _ = c.Writer.Write(processed)
		}
	}
}

// shouldProcessPath 检查是否应该处理该路径
func shouldProcessPath(path string) bool {
	// 处理时间线相关接口
	timelinePaths := []string{
		"/api/v1/timelines/home",
		"/api/v1/timelines/public",
		"/api/v1/timelines/list",
		"/api/v1/timelines/tag",
		"/api/v1/accounts/",
		"/api/v1/statuses/",
		"/api/v1/favourites",
		"/api/v1/bookmarks",
	}

	for _, p := range timelinePaths {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// isRemoteURL 检查是否是远程URL（非本站点URL）
func isRemoteURL(url string, host string) bool {
	if url == "" {
		return false
	}

	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return false
	}

	if host != "" && (strings.Contains(url, "://"+host) || strings.Contains(url, "://www."+host)) {
		return false
	}

	return true
}

// extractRemoteImageURLs 从内容中提取远程图片URL
func extractRemoteImageURLs(content string, host string) []string {
	matches := imageURLRegex.FindAllString(content, -1)
	var urls []string
	seen := make(map[string]bool)

	for _, url := range matches {
		url = strings.TrimSpace(url)
		if isRemoteURL(url, host) && !seen[url] {
			seen[url] = true
			urls = append(urls, url)
		}
	}

	return urls
}

// processJSONResponse 处理 JSON 响应体
func processJSONResponse(data []byte, svc *service.Service, host string) []byte {
	if svc == nil || !svc.IsEnabled() {
		return nil
	}

	// 解析 JSON
	var result interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		// 不是有效的 JSON，返回 nil 表示不处理
		return nil
	}

	// 递归处理所有字符串字段
	processed := processValue(result, svc, host)

	// 重新编码
	output, err := json.Marshal(processed)
	if err != nil {
		log.Warn("Failed to marshal processed response: %v", err)
		return nil
	}

	return output
}

// processValue 递归处理 JSON 值
func processValue(v interface{}, svc *service.Service, host string) interface{} {
	switch val := v.(type) {
	case string:
		// 处理字符串，替换其中的 URL
		return processString(val, svc, host)
	case map[string]interface{}:
		// 处理对象
		result := make(map[string]interface{})
		for k, v := range val {
			result[k] = processValue(v, svc, host)
		}
		return result
	case []interface{}:
		// 处理数组
		result := make([]interface{}, len(val))
		for i, v := range val {
			result[i] = processValue(v, svc, host)
		}
		return result
	default:
		return v
	}
}

// processString 处理字符串，替换图片 URL
func processString(s string, svc *service.Service, host string) string {
	// 检查是否包含远程图片 URL
	urls := extractRemoteImageURLs(s, host)
	if len(urls) == 0 {
		return s
	}

	// 批量获取缩略图 URL
	results := svc.BatchGetThumbnailURLs(context.TODO(), urls)

	// 替换 URL
	result := s
	for originalURL, thumbResult := range results {
		if thumbResult.Status == models.ThumbnailStatusCompleted && thumbResult.ThumbnailURL != "" {
			result = strings.ReplaceAll(result, originalURL, thumbResult.ThumbnailURL)
		}
	}

	return result
}

// ApplyThumbnailsToStatuses 对状态列表应用缩略图
// 这个函数可以在序列化层调用
func ApplyThumbnailsToStatuses(statuses []*models.Status, host string) {
	svc := GetService()
	if svc == nil || !svc.IsEnabled() || !svc.GetConfig().RewriteEnabled {
		return
	}

	// 收集所有需要处理的 URL
	urlMap := make(map[string]bool)
	for _, status := range statuses {
		if status.Content.Content != "" {
			urls := extractRemoteImageURLs(status.Content.Content, host)
			for _, url := range urls {
				urlMap[url] = true
			}
		}
	}

	if len(urlMap) == 0 {
		return
	}

	// 转换为切片
	var urls []string
	for url := range urlMap {
		urls = append(urls, url)
	}

	// 批量获取缩略图
	results := svc.BatchGetThumbnailURLs(context.TODO(), urls)

	// 应用到每个状态
	for _, status := range statuses {
		applyThumbnailsToStatus(status, results)
	}
}

// applyThumbnailsToStatus 应用缩略图到帖子
func applyThumbnailsToStatus(status *models.Status, results map[string]*service.ThumbnailResult) {
	if status.Content.Content == "" {
		return
	}

	content := status.Content.Content
	for originalURL, result := range results {
		if result.Status == models.ThumbnailStatusCompleted && result.ThumbnailURL != "" {
			content = strings.ReplaceAll(content, originalURL, result.ThumbnailURL)
		}
	}
	status.Content.Content = content
}

// ProcessContent 处理内容中的图片URL（供外部调用）
func ProcessContent(content string, host string) string {
	svc := GetService()
	if svc == nil {
		log.Debug("Thumbnail service not available")
		return content
	}

	urls := extractRemoteImageURLs(content, host)
	if len(urls) == 0 {
		return content
	}

	results := svc.BatchGetThumbnailURLs(context.TODO(), urls)

	result := content
	for originalURL, thumbResult := range results {
		if thumbResult.Status == models.ThumbnailStatusCompleted && thumbResult.ThumbnailURL != "" {
			result = strings.ReplaceAll(result, originalURL, thumbResult.ThumbnailURL)
		}
	}

	return result
}

// ThumbnailResult 结果类型别名（用于兼容）
type ThumbnailResult struct {
	OriginalURL  string `json:"original_url"`
	ThumbnailURL string `json:"thumbnail_url,omitempty"`
	Status       string `json:"status"`
	IsThumbnail  bool   `json:"is_thumbnail"`
	Reason       string `json:"reason,omitempty"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
}
