package list

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
)

type GetListFollowersRequest struct {
	MaxId   *uint64 `query:"max_id"`
	MinId   *uint64 `query:"min_id"`
	Limit   *uint64 `query:"limit"`
	SinceId *uint64 `query:"since_id"`
}

func GetListFollowers(c *gin.Context) {
	req := GetListAccountsRequest{}

	if err := c.ShouldBindQuery(&req); err != nil {
		c.AbortWithStatusJSON(400, gin.H{
			"error": err.Error(),
		})
		return
	}

	var currentAccount *models.Account
	if _currentAccount, ok := c.Get("account"); ok {
		currentAccount = _currentAccount.(*models.Account)
	}

	list, abort := GetListByParam(c)

	if abort {
		return
	}

	canView, err := models.ListCanView(list, currentAccount)
	if err != nil {
		_ = c.Error(err)
		return
	}

	if !canView {
		c.AbortWithStatusJSON(403, gin.H{
			"error": "forbidden",
		})
		return
	}

	if req.SinceId != nil {
		req.MinId = req.SinceId
	}

	accounts := make([]*models.Account, 0)
	query := db.GetDB().Model(&models.Account{}).
		Joins("list_follow_accounts ON list_follow_accounts.account_id = accounts.id").
		Order("list_follow_accounts.id DESC").
		Select("accounts.*").
		Where("list_follow_accounts.list_id = ?", list.Id)

	if req.MaxId != nil {
		query = query.Where("list_accounts.id <= ?", *req.MaxId)
	}

	if req.MinId != nil {
		query = query.Where("list_accounts.id >= ?", *req.MinId)
	}

	if req.Limit != nil {
		query = query.Limit(int(*req.Limit))
	}

	query = query.Preload("Emojis").Preload("Extra")

	err = query.Find(&accounts).Error

	if err != nil {
		err = errors.Wrap(err, "GetListFollowers error")
		_ = c.Error(err)
		return
	}

	c.JSON(200, serializer.ToAccounts(accounts))
}

func FollowList(c *gin.Context) {
	account := c.MustGet("account").(*models.Account)
	list, abort := GetListByParam(c)

	if abort {
		return
	}

	// 如果列表是私有的，判断是否有权限，则不能关注
	if list.Visibility == models.ListVisibilityPrivate {
		c.AbortWithStatusJSON(403, gin.H{"error": "forbidden"})
		return
	}

	// 判断是否已经关注
	var count int64
	err := db.GetDB().Model(&models.ListFollowAccount{}).Where("list_id = ? AND account_id = ?", list.Id, account.Id).Count(&count).Error

	if err != nil {
		err = errors.Wrap(err, "FollowList error")
		_ = c.Error(err)
		return
	}

	if count > 0 {
		c.JSON(200, gin.H{
			"error": "already followed",
		})
		return
	}

	// 关注
	record := &models.ListFollowAccount{
		ListId:    list.Id,
		AccountId: account.Id,
	}

	err = db.GetDB().Create(record).Error

	if err != nil {
		err = errors.Wrap(err, "FollowList error")
		_ = c.Error(err)
		return
	}

	c.Data(200, "application/json", nil)
}

func UnfollowList(c *gin.Context) {
	accountId := c.MustGet("accountId").(uint64)
	list, abort := GetListByParam(c)

	if abort {
		return
	}

	// 判断是否已经关注
	var count int64
	err := db.GetDB().Model(&models.ListFollowAccount{}).Where("list_id = ? AND account_id = ?", list.Id, accountId).Count(&count).Error

	if err != nil {
		err = errors.Wrap(err, "UnfollowList error")
		_ = c.Error(err)
		return
	}

	if count == 0 {
		c.JSON(200, gin.H{
			"error": "not followed",
		})
		return
	}

	// 取消关注
	err = db.GetDB().Where("list_id = ? AND account_id = ?", list.Id, accountId).Delete(&models.ListFollowAccount{}).Error

	if err != nil {
		err = errors.Wrap(err, "UnfollowList error")
		_ = c.Error(err)
		return
	}

	c.Data(200, "application/json", nil)
}
