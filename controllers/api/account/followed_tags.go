package account

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/controllers/api/common"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// FollowedTag represents a tag follow relationship.
type FollowedTag struct {
	Id        uint64     `gorm:"primary_key;autoIncrement" json:"id"`
	AccountId uint64     `gorm:"not null;index"             json:"account_id"`
	TagId     uint64     `gorm:"not null;index"             json:"tag_id"`
	CreatedAt *time.Time `gorm:"not null;autoCreateTime"    json:"created_at"`
}

func (f *FollowedTag) TableName() string {
	return "followed_tags"
}

// ListFollowedTags handles GET /api/v1/followed_tags.
func ListFollowedTags(c *gin.Context) {
	limit := 100

	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}

	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	// Query followed tags
	conn := db.GetDB().WithContext(c.Request.Context())
	query := conn.Table("followed_tags").
		Select("followed_tags.id, followed_tags.tag_id, tags.name").
		Joins("JOIN tags ON tags.id = followed_tags.tag_id").
		Where("followed_tags.account_id = ?", account.Id).
		Order("followed_tags.id DESC")

	// Pagination
	if maxId := c.Query("max_id"); maxId != "" {
		if id, err := strconv.ParseUint(maxId, 10, 64); err == nil {
			query = query.Where("followed_tags.id < ?", id)
		}
	}

	if sinceId := c.Query("since_id"); sinceId != "" {
		if id, err := strconv.ParseUint(sinceId, 10, 64); err == nil {
			query = query.Where("followed_tags.id > ?", id)
		}
	}

	type FollowedTagResult struct {
		Id    uint64
		TagId uint64
		Name  string
	}

	followedTags := make([]FollowedTagResult, 0)
	err := query.Limit(limit).Find(&followedTags).Error
	if err != nil {
		_ = c.Error(errors.Wrap(err, "get followed tags error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"}) //nolint:goconst

		return
	}

	tags := make([]*models.Tag, 0, len(followedTags))
	for _, ft := range followedTags {
		tags = append(tags, &models.Tag{Id: ft.TagId, Name: ft.Name})
	}
	result, err := serializer.ToDetailedTags(conn, tags, account)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if len(followedTags) > 0 {
		common.SetPageLinksUint(c, followedTags[0].Id, followedTags[len(followedTags)-1].Id, true)
	}

	c.JSON(200, result)
}

// GetTag handles GET /api/v1/tags/:id.
func GetTag(c *gin.Context) {
	conn := db.GetDB().WithContext(c.Request.Context())
	tag, err := tagForRead(conn, c.Param("id"))
	if err != nil {
		writeTagError(c, err)
		return
	}
	renderTag(c, conn, tag, controllers.GetAccount(c))
}

// FollowTag handles POST /api/v1/tags/:id/follow.
func FollowTag(c *gin.Context) {
	name := models.NormalizeTagName(c.Param("id"))
	if !models.ValidTagName(name) {
		writeTagError(c, gorm.ErrRecordNotFound)
		return
	}
	conn := db.GetDB().WithContext(c.Request.Context())
	account := controllers.MustGetAccount(c)
	tag, err := models.FindOrCreateTag(conn, name)
	if err == nil {
		err = conn.Clauses(clause.OnConflict{DoNothing: true}).
			Create(&FollowedTag{AccountId: account.Id, TagId: tag.Id}).Error
	}
	if err != nil {
		writeTagError(c, err)
		return
	}
	renderTag(c, conn, tag, account)
}

// UnfollowTag handles POST /api/v1/tags/:id/unfollow.
func UnfollowTag(c *gin.Context) {
	conn := db.GetDB().WithContext(c.Request.Context())
	account := controllers.MustGetAccount(c)
	tag, err := tagForRead(conn, c.Param("id"))
	if err == nil && tag.Id != 0 {
		err = conn.Where("account_id = ? AND tag_id = ?", account.Id, tag.Id).Delete(&FollowedTag{}).Error
	}
	if err != nil {
		writeTagError(c, err)
		return
	}
	renderTag(c, conn, tag, account)
}

func tagForRead(conn *gorm.DB, raw string) (*models.Tag, error) {
	name := models.NormalizeTagName(raw)
	if !models.ValidTagName(name) {
		return nil, gorm.ErrRecordNotFound
	}
	tag, err := models.FindTagByName(conn, name)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &models.Tag{Name: name}, nil
	}
	return tag, err
}

func renderTag(c *gin.Context, conn *gorm.DB, tag *models.Tag, viewer *models.Account) {
	tags, err := serializer.ToDetailedTags(conn, []*models.Tag{tag}, viewer)
	if err != nil {
		writeTagError(c, err)
		return
	}
	c.JSON(200, tags[0])
}

func writeTagError(c *gin.Context, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(404, gin.H{"error": "Record not found"})
		return
	}
	_ = c.Error(err)
}
