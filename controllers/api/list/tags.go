package list

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	listfeed "github.com/kasuganosora/community/feed/list"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"gorm.io/gorm"
)

// Extension endpoints (not in the Mastodon API): hashtags a list follows in
// addition to its members.
//
//	GET    /api/v1/lists/:id/tags                       -> [Tag]
//	POST   /api/v1/lists/:id/tags   {"names": ["godot"]} -> [Tag] (all tags of the list)
//	DELETE /api/v1/lists/:id/tags   {"names": ["godot"]} or ?names[]=godot -> [Tag]

type listTagsRequest struct {
	Names []string `form:"names[]" json:"names"`
}

func listTags(conn *gorm.DB, listID uint64) ([]*models.Tag, error) {
	var ids []uint64
	if err := conn.Model(&models.ListHashtag{}).Where("list_id = ?", listID).Order("id ASC").Pluck("tag_id", &ids).Error; err != nil {
		return nil, err
	}
	tags := make([]*models.Tag, 0, len(ids))
	if len(ids) == 0 {
		return tags, nil
	}
	var rows []*models.Tag
	if err := conn.Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	byID := make(map[uint64]*models.Tag, len(rows))
	for _, t := range rows {
		byID[t.Id] = t
	}
	for _, id := range ids { // in the order they were added
		if t := byID[id]; t != nil {
			tags = append(tags, t)
		}
	}
	return tags, nil
}

func GetListTags(c *gin.Context) {
	list, abort := GetListByParam(c)
	if abort {
		return
	}
	var viewer *models.Account
	if v, ok := c.Get("account"); ok {
		viewer, _ = v.(*models.Account)
	}
	canView, err := models.ListCanView(list, viewer)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if !canView {
		c.AbortWithStatusJSON(403, gin.H{"error": "forbidden"})
		return
	}
	tags, err := listTags(db.GetDB(), list.Id)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(200, serializer.ToTags(tags))
}

func ownedList(c *gin.Context) (*models.List, bool) {
	list, abort := GetListByParam(c)
	if abort {
		return nil, false
	}
	self, _ := c.MustGet("account").(*models.Account)
	if self == nil || list.AccountId != self.Id {
		c.AbortWithStatusJSON(403, gin.H{"error": "you are not the owner of the list"})
		return nil, false
	}
	return list, true
}

func requestedTagNames(c *gin.Context) ([]string, bool) {
	req := listTagsRequest{}
	if c.Request.ContentLength > 0 {
		if err := controllers.BindBody(c, &req); err != nil {
			c.AbortWithStatusJSON(400, gin.H{"error": err.Error()})
			return nil, false
		}
	}
	if len(req.Names) == 0 {
		req.Names = append(c.QueryArray("names[]"), c.QueryArray("names")...)
	}
	cleaned := make([]string, 0, len(req.Names))
	for _, name := range req.Names {
		cleaned = append(cleaned, strings.TrimPrefix(strings.TrimSpace(name), "#"))
	}
	names := models.CleanTagNames(cleaned)
	if len(names) == 0 {
		c.AbortWithStatusJSON(422, gin.H{"error": "no valid hashtag names"})
		return nil, false
	}
	return names, true
}

func AddListTags(c *gin.Context) {
	list, ok := ownedList(c)
	if !ok {
		return
	}
	names, ok := requestedTagNames(c)
	if !ok {
		return
	}
	conn := db.GetDB()
	err := conn.Transaction(func(tx *gorm.DB) error {
		// Lock the list row so concurrent adds cannot exceed the limit.
		if err := tx.Model(&models.List{}).Where("id = ?", list.Id).Update("updated_at", gorm.Expr("updated_at")).Error; err != nil {
			return err
		}
		tags, err := models.FindOrCreateTags(tx, names)
		if err != nil {
			return err
		}
		var existing []uint64
		if err := tx.Model(&models.ListHashtag{}).Where("list_id = ?", list.Id).Pluck("tag_id", &existing).Error; err != nil {
			return err
		}
		have := make(map[uint64]bool, len(existing))
		for _, id := range existing {
			have[id] = true
		}
		rows := make([]*models.ListHashtag, 0, len(tags))
		for _, name := range names {
			t := tags[models.TagKey(name)]
			if t == nil || t.Id == 0 || have[t.Id] {
				continue
			}
			have[t.Id] = true
			rows = append(rows, &models.ListHashtag{ListId: list.Id, TagId: t.Id})
		}
		if len(existing)+len(rows) > models.MaxListHashtags {
			return errTooManyTags
		}
		if len(rows) == 0 {
			return nil
		}
		return tx.Create(&rows).Error
	})
	if err == errTooManyTags {
		c.AbortWithStatusJSON(422, gin.H{"error": "a list can follow at most 20 hashtags"})
		return
	}
	if err != nil {
		_ = c.Error(err)
		return
	}
	listfeed.DropCache(list.Id)
	respondListTags(c, list.Id)
}

func RemoveListTags(c *gin.Context) {
	list, ok := ownedList(c)
	if !ok {
		return
	}
	names, ok := requestedTagNames(c)
	if !ok {
		return
	}
	conn := db.GetDB()
	tags, err := models.FindTagsByNames(conn, names)
	if err != nil {
		_ = c.Error(err)
		return
	}
	ids := make([]uint64, 0, len(tags))
	for _, t := range tags {
		ids = append(ids, t.Id)
	}
	if len(ids) > 0 {
		if err := conn.Where("list_id = ? AND tag_id IN ?", list.Id, ids).Delete(&models.ListHashtag{}).Error; err != nil {
			_ = c.Error(err)
			return
		}
		listfeed.DropCache(list.Id)
	}
	respondListTags(c, list.Id)
}

func respondListTags(c *gin.Context, listID uint64) {
	tags, err := listTags(db.GetDB(), listID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(200, serializer.ToTags(tags))
}

type listTagError string

func (e listTagError) Error() string { return string(e) }

const errTooManyTags = listTagError("too many list hashtags")
