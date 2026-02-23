package status

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

type ReblogRequest struct {
	Visibility string `json:"visibility" binding:"omitempty,oneof=public unlisted private direct"`
}

func Reblog(c *gin.Context) {
	req := &ReblogRequest{}
	err := c.ShouldBindJSON(req)

	controllers.AbortIf(c, err != nil, 400, gin.H{"error": err})

	if req.Visibility == "" {
		req.Visibility = "public"
	}

	status := getRealStatus(c)

	dbConn := db.GetDB()
	currentAccount := controllers.MustGetAccount(c)

	// 判断当前用户有没有转发过这条 status
	isRelogged, err := status.IsReblogged(dbConn, currentAccount)
	controllers.AbortIfError(c, err)
	controllers.AbortIf(c, isRelogged, 400, gin.H{"error": "already reblogged"})

	// 判断当前用户有没权限转发这条 status, 只要能看到这条 status 就可以转发
	canView, err := status.CanViewEx(dbConn, currentAccount)
	controllers.AbortIfError(c, err)
	controllers.AbortIf(c, !canView, 403, gin.H{"error": "forbidden"})

	reblog := &models.Status{}
	reblog.ReblogId = &status.Id
	reblog.AccountId = currentAccount.Id

	// 可视范围，优先判断 reblog 的可视范围， 再判断原 status 的可视范围
	_ = reblog.SetVisibilityByString(req.Visibility)

	if app, exists := c.Get("application"); exists {
		reblog.ApplicationId = &app.(*models.Application).Id
	}

	// 生成转发的 status， 并把原来的 status 的转发数加一
	err = dbConn.Transaction(func(tx *gorm.DB) error {
		err = tx.Create(reblog).Error
		if err != nil {
			err = errors.Wrap(err, "create reblog")
			return err
		}
		err = tx.Model(status).Update("reblogs_count", gorm.Expr("reblogs_count + ?", 1)).Error
		if err != nil {
			err = errors.Wrap(err, "update reblogs_count")
			return err
		}
		return nil
	})

	controllers.AbortIfError(c, err)

	// 重新获取转发的 status
	returnFullSingleStatus(c, status, currentAccount)
}

func Unreblog(c *gin.Context) {
	status := getRealStatus(c)

	dbConn := db.GetDB()
	currentAccount := controllers.MustGetAccount(c)

	// 判断当前用户有没有转发过这条 status
	isRelogged, err := status.IsReblogged(dbConn, currentAccount)
	controllers.AbortIfError(c, err)
	controllers.AbortIf(c, !isRelogged, 400, gin.H{"error": "not reblogged"})

	// 删除转发的 status， 并把原来的 status 的转发数减一
	err = dbConn.Transaction(func(tx *gorm.DB) error {
		err = tx.Delete(&models.Status{}, "reblog_id = ? AND account_id = ?", status.Id, currentAccount.Id).Error
		if err != nil {
			err = errors.Wrap(err, "delete reblog")
			return err
		}

		err = tx.Model(status).Update("reblogs_count", gorm.Expr("reblogs_count - ?", 1)).Error
		if err != nil {
			err = errors.Wrap(err, "update reblogs_count")
			return err
		}

		return nil
	})

	controllers.AbortIfError(c, err)

	// 重新获取转发的 status
	returnFullSingleStatus(c, status, currentAccount)
}

type RebloggedByRequest struct {
	MaxId   uint64 `query:"max_id"`
	SinceId uint64 `query:"since_id"`
	Limit   uint64 `query:"limit"`
	Offset  uint64 `query:"offset"`
}

func RebloggedBy(c *gin.Context) {
	req := &RebloggedByRequest{}
	err := c.ShouldBindQuery(req)

	controllers.AbortIf(c, err != nil, 400, gin.H{"error": err})

	status := getRealStatus(c)

	var currentAccount *models.Account
	_currentAccount, exists := c.Get("account")

	if exists {
		currentAccount = _currentAccount.(*models.Account)
	}

	dbConn := db.GetDB()
	// 只有能看到这条 status 的人才能看到收藏列表
	canView, err := status.CanViewEx(dbConn, currentAccount)

	controllers.AbortIfError(c, err)
	controllers.AbortIf(c, !canView, 403, gin.H{"error": "forbidden"})

	accounts := make([]*models.Account, 0)

	query := dbConn.Model(&models.Account{}).Joins("JOIN statuses ON statuses.account_id = accounts.id").
		Where("statuses.reblog_id = ?", status.Id).
		Select("accounts.*").
		Order("statuses.created_at DESC")

	if req.MaxId > 0 {
		query = query.Where("accounts.id <= ?", req.MaxId)
	}

	if req.SinceId > 0 {
		query = query.Where("accounts.id >= ?", req.SinceId)
	}

	if req.Limit > 0 {
		query = query.Limit(int(req.Limit))
	}

	if req.Offset > 0 {
		query = query.Offset(int(req.Offset))
	}

	err = query.Find(&accounts).Error

	controllers.AbortIfError(c, err)

	ret := serializer.ToAccounts(accounts)

	c.JSON(200, ret)
}
