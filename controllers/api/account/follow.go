package account

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"strconv"
)

type FollowRequest struct {
	AccountId uint64 `json:"account_id"`
	Reblogs   bool   `json:"reblogs"`
}

func Follow(c *gin.Context) {
	var req FollowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(400, gin.H{
			"code": 400,                    //nolint:goconst
			"msg":  "invalid request body", //nolint:goconst
		})

		return
	}

	accid, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || accid == 0 {
		c.AbortWithStatusJSON(400, gin.H{
			"code": 400,
			"msg":  "invalid account id", //nolint:goconst
		})

		return
	}

	req.AccountId = accid

	// 获取当前登录用户
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	// 检查被关注的账户ID是否存在
	targetAccount := &models.Account{}

	err = db.GetDB().Model(&models.Account{}).Preload("User").Preload("User.Settings").Where("id = ?", req.AccountId).First(targetAccount).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatusJSON(400, gin.H{
				"code": 400,
				"msg":  "account not found", //nolint:goconst
			})

			return
		}

		c.AbortWithStatusJSON(500, gin.H{
			"code": 500,
			"msg":  "internal error", //nolint:goconst
		})

		err = errors.Wrap(err, "GetAccountBase error")
		_ = c.Error(err)

		return
	}

	follow := &models.Follow{}
	err = db.GetDB().Model(&models.Follow{}).Where("account_id = ? AND follow_id = ?", account.Id, targetAccount.Id).First(follow).Error

	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		c.AbortWithStatusJSON(500, gin.H{
			"code": 500,
			"msg":  "internal error",
		})

		err = errors.Wrap(err, "GetAccountBase error")
		_ = c.Error(err)

		return
	}

	follow.AccountId = account.Id
	follow.FollowId = targetAccount.Id
	// Initialize Properties map if nil
	if follow.Properties == nil {
		follow.Properties = make(map[string]any)
	}

	follow.Properties["reblogs"] = req.Reblogs

	if follow.Id > 0 {
		err = db.GetDB().Save(follow).Error
	} else {
		// 如果目标账户设置了关注保护，那么直接设置为请求状态
		if targetAccount.User.Settings.IsFollowProtect() {
			follow.Status = models.FollowStatusRequested
		}

		err = db.GetDB().Create(follow).Error
	}

	if err != nil {
		c.AbortWithStatusJSON(500, gin.H{
			"code": 500,
			"msg":  "internal error",
		})

		err = errors.Wrap(err, "GetAccountBase error")
		_ = c.Error(err)

		return
	}

	c.JSON(200, serializer.ToFollowStatus(targetAccount, follow))
}

type UnfollowRequest struct {
	AccountId uint64 `json:"account_id"`
}

func UnFollow(c *gin.Context) {
	var req UnfollowRequest

	accid, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || accid == 0 {
		c.AbortWithStatusJSON(400, gin.H{
			"code": 400,
			"msg":  "invalid account id",
		})

		return
	}

	req.AccountId = accid

	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	targetAccount := &models.Account{}

	err = db.GetDB().Model(&models.Account{}).Where("id = ?", req.AccountId).First(targetAccount).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatusJSON(400, gin.H{
				"code": 400,
				"msg":  "account not found",
			})

			return
		}

		err = errors.Wrap(err, "get follow target account error")
		_ = c.Error(err)

		c.AbortWithStatusJSON(500, gin.H{
			"code": 500,
			"msg":  "internal error",
		})

		return
	}

	follow := &models.Follow{}

	err = db.GetDB().Model(&models.Follow{}).Where("account_id = ? AND follow_id = ?", account.Id, targetAccount.Id).First(follow).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatusJSON(400, gin.H{
				"code": 400,
				"msg":  "not followed",
			})

			return
		}

		err = errors.Wrap(err, "get follow relation error")
		_ = c.Error(err)

		c.AbortWithStatusJSON(500, gin.H{
			"code": 500,
			"msg":  "internal error",
		})

		return
	}

	err = db.GetDB().Delete(follow).Error
	if err != nil {
		err = errors.Wrap(err, "delete follow relation error")
		_ = c.Error(err)

		c.AbortWithStatusJSON(500, gin.H{
			"code": 500,
			"msg":  "internal error",
		})

		return
	}

	c.JSON(200, serializer.ToFollowStatus(targetAccount, nil))
}
