# Phase 1 实施指南

本文档提供完成 Phase 1 剩余任务的详细指南和代码模板。

---

## 任务 3: Search API v2

### 创建搜索控制器

#### 文件: `controllers/api/search/search.go`

```go
package search

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"strconv"
	"strings"
)

// Search 通用搜索
func Search(c *gin.Context) {
	query := strings.TrimSpace(c.Query("q"))
	if query == "" {
		c.JSON(400, gin.H{"error": "Query parameter 'q' is required"})
		return
	}

	searchType := c.DefaultQuery("type", "accounts")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "40"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	var response interface{}
	var err error

	switch searchType {
	case "accounts":
		response, err = searchAccounts(query, limit, offset)
	case "statuses":
		response, err = searchStatuses(query, limit, offset)
	case "hashtags":
		response, err = searchHashtags(query, limit, offset)
	default:
		c.JSON(400, gin.H{"error": "Invalid search type"})
		return
	}

	if err != nil {
		err = errors.Wrap(err, "Search")
		_ = c.Error(err)
		return
	}

	c.JSON(200, response)
}

// searchAccounts 搜索账户
func searchAccounts(query string, limit, offset int) (*SearchResponse, error) {
	var accounts models.Accounts
	err := db.GetDB().
		Where("username LIKE ? OR display_name LIKE ?", "%"+query+"%", "%"+query+"%").
		Limit(limit).
		Offset(offset).
		Find(&accounts).Error

	if err != nil {
		return nil, err
	}

	serialized, err := serializer.ToAccountList(accounts)
	if err != nil {
		return nil, err
	}

	return &SearchResponse{
		Accounts: serialized,
		Statuses: []*serializer.StatusResponse{},
		Hashtags: []*serializer.TagResponse{},
	}, nil
}

// searchStatuses 搜索嘟文
func searchStatuses(query string, limit, offset int) (*SearchResponse, error) {
	var statuses models.Statuses
	err := db.GetDB().
		Where("content LIKE ? AND visibility = ?", "%"+query+"%", models.StatusVisibilityPublic).
		Limit(limit).
		Offset(offset).
		Find(&statuses).Error

	if err != nil {
		return nil, err
	}

	serialized, err := serializer.ToStatusList(statuses, nil)
	if err != nil {
		return nil, err
	}

	return &SearchResponse{
		Accounts: []*serializer.AccountResponse{},
		Statuses: serialized,
		Hashtags: []*serializer.TagResponse{},
	}, nil
}

// searchHashtags 搜索标签
func searchHashtags(query string, limit, offset int) (*SearchResponse, error) {
	// TODO: 实现标签搜索
	return &SearchResponse{
		Accounts: []*serializer.AccountResponse{},
		Statuses: []*serializer.StatusResponse{},
		Hashtags: []*serializer.TagResponse{},
	}, nil
}

// SearchResponse 搜索响应
type SearchResponse struct {
	Accounts []*serializer.AccountResponse `json:"accounts"`
	Statuses []*serializer.StatusResponse `json:"statuses"`
	Hashtags []*serializer.TagResponse     `json:"hashtags"`
}
```

#### 路由添加

在 `router.go` 中添加：

```go
// Search API
v1ApiGroup.GET("/search", search.Search)
v2ApiGroup.GET("/search", search.Search) // v2 版本
```

---

## 任务 4: OAuth 2.0 标准

### 创建 OAuth 模型

#### 文件: `models/oauth.go`

```go
package models

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// OAuthApplication OAuth 应用
type OAuthApplication struct {
	Id           uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	Name         string    `gorm:"size:255;not null" json:"name"`
	ClientId     string    `gorm:"size:100;unique;not null" json:"client_id"`
	ClientSecret string    `gorm:"size:100;not null" json:"client_secret"`
	RedirectUri  string    `gorm:"type:text;not null" json:"redirect_uri"`
	Scopes       string    `gorm:"type:text" json:"scopes"`
	Website      string    `gorm:"size:255" json:"website"`
	CreatedAt    time.Time `gorm:"autoCreateTime" json:"created_at"`
}

// OAuthToken OAuth 令牌
type OAuthToken struct {
	Id           uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	ApplicationId uint64   `gorm:"not null;index" json:"application_id"`
	AccountId    uint64    `gorm:"not null;index" json:"account_id"`
	AccessToken  string    `gorm:"size:100;unique;not null;index" json:"access_token"`
	TokenType    string    `gorm:"size:20;default:'Bearer'" json:"token_type"`
	Scope        string    `gorm:"type:text" json:"scope"`
	ExpiresAt    *time.Time `json:"expires_at"`
	CreatedAt    time.Time `gorm:"autoCreateTime" json:"created_at"`

	Application *OAuthApplication `gorm:"foreignKey:ApplicationId" json:"-"`
	Account     *Account          `gorm:"foreignKey:AccountId" json:"-"`
}

// GenerateClientID 生成客户端 ID
func GenerateClientID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// GenerateClientSecret 生成客户端密钥
func GenerateClientSecret() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// GenerateAccessToken 生成访问令牌
func GenerateAccessToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}
```

### 创建 OAuth 控制器

#### 文件: `controllers/oauth/apps.go`

```go
package oauth

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
)

// CreateApp 创建应用
func CreateApp(c *gin.Context) {
	var request struct {
		ClientName   string `json:"client_name" binding:"required"`
		RedirectUris string `json:"redirect_uris" binding:"required"`
		Scopes       string `json:"scopes"`
		Website      string `json:"website"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request"})
		return
	}

	app := &models.OAuthApplication{
		Name:         request.ClientName,
		ClientId:     models.GenerateClientID(),
		ClientSecret: models.GenerateClientSecret(),
		RedirectUri:  request.RedirectUris,
		Scopes:       request.Scopes,
		Website:      request.Website,
	}

	if err := db.GetDB().Create(app).Error; err != nil {
		err = errors.Wrap(err, "Create OAuth app")
		_ = c.Error(err)
		c.JSON(500, gin.H{"error": "Internal server error"})
		return
	}

	c.JSON(200, gin.H{
		"id":            app.Id,
		"name":          app.Name,
		"website":       app.Website,
		"client_id":     app.ClientId,
		"client_secret": app.ClientSecret,
		"redirect_uri":  app.RedirectUri,
	})
}
```

---

## 任务 5: Follow Requests API

### 创建 FollowRequest 模型

#### 文件: `models/follow_request.go`

```go
package models

import "time"

// FollowRequest 关注请求
type FollowRequest struct {
	Id        uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	AccountId uint64    `gorm:"not null;index" json:"account_id"` // 被请求的用户
	FollowerId uint64   `gorm:"not null;index" json:"follower_id"` // 请求关注的人
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`

	Account  *Account `gorm:"foreignKey:AccountId" json:"-"`
	Follower *Account `gorm:"foreignKey:FollowerId" json:"-"`
}
```

### 创建 FollowRequest 控制器

#### 文件: `controllers/api/account/follow_request.go`

```go
package account

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

// GetFollowRequests 获取关注请求列表
func GetFollowRequests(c *gin.Context) {
	account := c.MustGet("account").(*models.Account)

	var requests []models.FollowRequest
	err := db.GetDB().
		Preload("Follower").
		Where("account_id = ?", account.Id).
		Find(&requests).Error

	if err != nil {
		err = errors.Wrap(err, "Get follow requests")
		_ = c.Error(err)
		return
	}

	followers := make(models.Accounts, 0, len(requests))
	for _, req := range requests {
		followers = append(followers, req.Follower)
	}

	serialized, err := serializer.ToAccountList(followers)
	if err != nil {
		err = errors.Wrap(err, "Serialize accounts")
		_ = c.Error(err)
		return
	}

	c.JSON(200, serialized)
}

// AuthorizeFollowRequest 接受关注请求
func AuthorizeFollowRequest(c *gin.Context) {
	account := c.MustGet("account").(*models.Account)
	followerId := c.Param("id")

	var request models.FollowRequest
	err := db.GetDB().
		Where("account_id = ? AND follower_id = ?", account.Id, followerId).
		First(&request).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(404, gin.H{"error": "Not found"})
			return
		}
		err = errors.Wrap(err, "Get follow request")
		_ = c.Error(err)
		return
	}

	// 创建关注关系
	follow := &models.Follow{
		AccountId:  account.Id,
		FollowerId: request.FollowerId,
	}

	tx := db.GetDB().Begin()
	if err := tx.Create(follow).Error; err != nil {
		tx.Rollback()
		err = errors.Wrap(err, "Create follow")
		_ = c.Error(err)
		return
	}

	// 删除请求
	if err := tx.Delete(&request).Error; err != nil {
		tx.Rollback()
		err = errors.Wrap(err, "Delete request")
		_ = c.Error(err)
		return
	}

	tx.Commit()

	// 返回关系
	// TODO: 实现关系序列化
	c.JSON(200, gin.H{
		"id":         followerId,
		"following":  true,
		"followed_by": false,
	})
}

// RejectFollowRequest 拒绝关注请求
func RejectFollowRequest(c *gin.Context) {
	account := c.MustGet("account").(*models.Account)
	followerId := c.Param("id")

	result := db.GetDB().
		Where("account_id = ? AND follower_id = ?", account.Id, followerId).
		Delete(&models.FollowRequest{})

	if result.Error != nil {
		err := errors.Wrap(result.Error, "Delete follow request")
		_ = c.Error(err)
		return
	}

	if result.RowsAffected == 0 {
		c.JSON(404, gin.H{"error": "Not found"})
		return
	}

	c.JSON(200, gin.H{})
}
```

---

## 任务 6: Favourites & Bookmarks 列表

### 创建收藏和书签控制器

#### 文件: `controllers/api/status/favourites.go`

```go
package status

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
	"strconv"
)

// GetFavourites 获取收藏列表
func GetFavourites(c *gin.Context) {
	account := c.MustGet("account").(*models.Account)

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "40"))
	maxId := c.Query("max_id")
	sinceId := c.Query("since_id")

	var favourites []models.Favourite
	query := db.GetDB().
		Preload("Status").
		Where("account_id = ?", account.Id).
		Order("id DESC").
		Limit(limit)

	if maxId != "" {
		query = query.Where("id < ?", maxId)
	}
	if sinceId != "" {
		query = query.Where("id > ?", sinceId)
	}

	if err := query.Find(&favourites).Error; err != nil {
		err = errors.Wrap(err, "Get favourites")
		_ = c.Error(err)
		return
	}

	statuses := make(models.Statuses, 0, len(favourites))
	for _, fav := range favourites {
		statuses = append(statuses, fav.Status)
	}

	serialized, err := serializer.ToStatusList(statuses, account)
	if err != nil {
		err = errors.Wrap(err, "Serialize statuses")
		_ = c.Error(err)
		return
	}

	c.JSON(200, serialized)
}

// GetBookmarks 获取书签列表
func GetBookmarks(c *gin.Context) {
	account := c.MustGet("account").(*models.Account)

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "40"))
	maxId := c.Query("max_id")
	sinceId := c.Query("since_id")

	var bookmarks []models.Bookmark
	query := db.GetDB().
		Preload("Status").
		Where("account_id = ?", account.Id).
		Order("id DESC").
		Limit(limit)

	if maxId != "" {
		query = query.Where("id < ?", maxId)
	}
	if sinceId != "" {
		query = query.Where("id > ?", sinceId)
	}

	if err := query.Find(&bookmarks).Error; err != nil {
		err = errors.Wrap(err, "Get bookmarks")
		_ = c.Error(err)
		return
	}

	statuses := make(models.Statuses, 0, len(bookmarks))
	for _, bm := range bookmarks {
		statuses = append(statuses, bm.Status)
	}

	serialized, err := serializer.ToStatusList(statuses, account)
	if err != nil {
		err = errors.Wrap(err, "Serialize statuses")
		_ = c.Error(err)
		return
	}

	c.JSON(200, serialized)
}
```

---

## 任务 7: 完善账户接口

### 添加账户相关接口

#### 文件: `controllers/api/account/credentials.go`

```go
package account

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
)

// VerifyCredentials 验证凭证
func VerifyCredentials(c *gin.Context) {
	account := c.MustGet("account").(*models.Account)

	// 加载完整信息
	if err := db.GetDB().Model(account).Association("Emojis").Find(&account.Emojis); err != nil {
		err = errors.Wrap(err, "Load emojis")
		_ = c.Error(err)
		return
	}

	serialized, err := serializer.ToAccount(account)
	if err != nil {
		err = errors.Wrap(err, "Serialize account")
		_ = c.Error(err)
		return
	}

	// 添加 source 字段（用于客户端显示账户设置）
	serialized["source"] = gin.H{
		"privacy":   account.Privacy,
		"sensitive": account.Sensitive,
		"language":  account.Language,
		"note":      account.Note,
		"fields":    []interface{}{},
	}

	c.JSON(200, serialized)
}

// GetAccountStatuses 获取用户嘟文
func GetAccountStatuses(c *gin.Context) {
	accountId := c.Param("id")
	limit := c.DefaultQuery("limit", "20")
	onlyMedia := c.Query("only_media") == "true"
	excludeReplies := c.Query("exclude_replies") == "true"
	pinned := c.Query("pinned") == "true"

	query := db.GetDB().Model(&models.Status{}).
		Where("account_id = ?", accountId).
		Order("created_at DESC")

	if onlyMedia {
		query = query.Where("media_attachments_count > 0")
	}
	if excludeReplies {
		query = query.Where("in_reply_to_id IS NULL")
	}
	if pinned {
		query = query.Where("pinned = ?", true)
	}

	limitInt, _ := strconv.Atoi(limit)
	query = query.Limit(limitInt)

	var statuses models.Statuses
	if err := query.Find(&statuses).Error; err != nil {
		err = errors.Wrap(err, "Get account statuses")
		_ = c.Error(err)
		return
	}

	account, _ := c.Get("account")
	serialized, err := serializer.ToStatusList(statuses, account.(*models.Account))
	if err != nil {
		err = errors.Wrap(err, "Serialize statuses")
		_ = c.Error(err)
		return
	}

	c.JSON(200, serialized)
}

// GetAccountFollowers 获取粉丝列表
func GetAccountFollowers(c *gin.Context) {
	accountId := c.Param("id")
	limit := c.DefaultQuery("limit", "40")

	var follows []models.Follow
	query := db.GetDB().
		Preload("Follower").
		Where("account_id = ?", accountId).
		Order("id DESC")

	limitInt, _ := strconv.Atoi(limit)
	query = query.Limit(limitInt)

	if err := query.Find(&follows).Error; err != nil {
		err = errors.Wrap(err, "Get followers")
		_ = c.Error(err)
		return
	}

	followers := make(models.Accounts, 0, len(follows))
	for _, f := range follows {
		followers = append(followers, f.Follower)
	}

	serialized, err := serializer.ToAccountList(followers)
	if err != nil {
		err = errors.Wrap(err, "Serialize accounts")
		_ = c.Error(err)
		return
	}

	c.JSON(200, serialized)
}

// GetAccountFollowing 获取关注列表
func GetAccountFollowing(c *gin.Context) {
	accountId := c.Param("id")
	limit := c.DefaultQuery("limit", "40")

	var follows []models.Follow
	query := db.GetDB().
		Preload("Account").
		Where("follower_id = ?", accountId).
		Order("id DESC")

	limitInt, _ := strconv.Atoi(limit)
	query = query.Limit(limitInt)

	if err := query.Find(&follows).Error; err != nil {
		err = errors.Wrap(err, "Get following")
		_ = c.Error(err)
		return
	}

	following := make(models.Accounts, 0, len(follows))
	for _, f := range follows {
		following = append(following, f.Account)
	}

	serialized, err := serializer.ToAccountList(following)
	if err != nil {
		err = errors.Wrap(err, "Serialize accounts")
		_ = c.Error(err)
		return
	}

	c.JSON(200, serialized)
}
```

---

## 路由配置总结

在 `router.go` 中添加以下路由：

```go
// Search API
v1ApiGroup.GET("/search", search.Search)
v2ApiGroup.GET("/search", search.Search)

// OAuth API
v1ApiGroup.POST("/apps", oauth.CreateApp)

// Follow Requests API
authGroupV1.Use(middleware.CheckScopeMiddleware(...)).
	GET("/follow_requests", account.GetFollowRequests).
	POST("/follow_requests/:id/authorize", account.AuthorizeFollowRequest).
	POST("/follow_requests/:id/reject", account.RejectFollowRequest)

// Favourites & Bookmarks
authGroupV1.Use(middleware.CheckScopeMiddleware(...)).
	GET("/favourites", status.GetFavourites).
	GET("/bookmarks", status.GetBookmarks)

// Account APIs
v1ApiGroup.GET("/accounts/verify_credentials", account.VerifyCredentials)
v1ApiGroup.GET("/accounts/:id/statuses", account.GetAccountStatuses)
v1ApiGroup.GET("/accounts/:id/followers", account.GetAccountFollowers)
v1ApiGroup.GET("/accounts/:id/following", account.GetAccountFollowing)
```

---

## 数据库迁移

需要创建以下数据库迁移脚本：

### 文件: `migrate/scripts/add_oauth_tables.go`

```go
package scripts

import "gorm.io/gorm"

func AddOAuthTables(db *gorm.DB) error {
	return db.AutoMigrate(
		&models.OAuthApplication{},
		&models.OAuthToken{},
		&models.FollowRequest{},
	)
}
```

---

## 测试要点

### 单元测试
- [ ] Media 上传测试
- [ ] Search 查询测试
- [ ] OAuth 流程测试
- [ ] Follow Request 逻辑测试
- [ ] Favourites/Bookmarks 分页测试

### 集成测试
- [ ] API 端点完整性测试
- [ ] 权限验证测试
- [ ] 分页功能测试
- [ ] 客户端兼容性测试

---

## 性能优化建议

1. **搜索优化**：
   - 添加数据库索引
   - 考虑全文搜索引擎
   - 缓存热门搜索结果

2. **列表查询优化**：
   - 使用游标分页
   - 添加 Redis 缓存
   - 数据库查询优化

3. **媒体处理优化**：
   - 异步处理大文件
   - CDN 分发
   - 缩略图缓存

---

**更新时间**: 2025-02-17  
**文档版本**: v1.0
