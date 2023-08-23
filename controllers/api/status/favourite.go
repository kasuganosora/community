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

func Favourite(c *gin.Context) {
	status := getRealStatus(c)

	currentAccount := controllers.MustGetAccount(c)
	dbConn := db.GetDB()

	isFavourited, err := status.IsFavourited(dbConn, currentAccount)
	if err != nil {
		_ = c.Error(err)
		return
	}

	if isFavourited {
		c.AbortWithStatusJSON(400, gin.H{"error": "already favourited"})
		return
	}

	// 判断用户是否有权限收藏这条 status， 能看到都能收藏
	canView, err := status.CanViewEx(dbConn, currentAccount)
	if err != nil {
		_ = c.Error(err)
		return
	}

	if !canView {
		c.AbortWithStatusJSON(403, gin.H{"error": "forbidden"})
		return
	}

	favourite := &models.FavoriteStatus{}
	favourite.AccountId = currentAccount.Id
	favourite.StatusId = status.Id

	// 生成收藏记录， 并把原来的 status 的收藏数加一
	err = dbConn.Transaction(func(tx *gorm.DB) error {
		err = tx.Create(favourite).Error
		if err != nil {
			err = errors.Wrap(err, "create favourite")
			return err
		}

		err = tx.Model(status).Update("favourites_count", gorm.Expr("favourites_count + ?", 1)).Error
		if err != nil {
			err = errors.Wrap(err, "update status favourites_count")
			return err
		}

		return nil
	})

	if err != nil {
		_ = c.Error(err)
		return
	}

	// 重新获取 status
	returnFullSingleStatus(c, status, currentAccount)
}

func Unfavourite(c *gin.Context) {
	status := getRealStatus(c)

	currentAccount := c.MustGet("account").(*models.Account)
	dbConn := db.GetDB()

	isFavourited, err := status.IsFavourited(dbConn, currentAccount)
	if err != nil {
		_ = c.Error(err)
		return
	}

	if !isFavourited {
		c.JSON(400, gin.H{"error": "not favourited"})
		return
	}

	// 删除收藏记录， 并把原来的 status 的收藏数减一
	err = dbConn.Transaction(func(tx *gorm.DB) error {
		err = tx.Where("account_id = ? AND status_id = ?", currentAccount.Id, status.Id).Delete(&models.FavoriteStatus{}).Error
		if err != nil {
			err = errors.Wrap(err, "delete favourite")
			return err
		}

		err = tx.Model(status).Update("favourites_count", gorm.Expr("favourites_count - ?", 1)).Error
		if err != nil {
			err = errors.Wrap(err, "update status favourites_count")
			return err
		}

		return nil
	})

	if err != nil {
		_ = c.Error(err)
		return
	}
}

type FavouritedByRequest struct {
	MaxId   uint64 `query:"max_id"`
	SinceId uint64 `query:"since_id"`
	Limit   uint64 `query:"limit"`
	Offset  uint64 `query:"offset"`
}

func FavouritedBy(c *gin.Context) {
	req := &FavouritedByRequest{}
	err := c.ShouldBindQuery(req)
	if err != nil {
		c.JSON(422, gin.H{"error": err.Error()})
		return
	}

	status := getRealStatus(c)

	var currentAccount *models.Account
	_currentAccount, exists := c.Get("account")

	if exists {
		currentAccount = _currentAccount.(*models.Account)
	}

	dbConn := db.GetDB()
	// 只有能看到这条 status 的人才能看到收藏列表
	canView, err := status.CanViewEx(dbConn, currentAccount)

	if err != nil {
		_ = c.Error(err)
		return
	}

	if !canView {
		c.JSON(403, gin.H{"error": "forbidden"})
		return
	}

	var accounts []*models.Account

	query := dbConn.Model(&models.Account{}).
		Joins("favorite_statuses ON favorite_statuses.account_id = accounts.id").
		Select("accounts.*").
		Order("favorite_statuses.created_at DESC")

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

	if err != nil {
		err = errors.Wrap(err, "get reblogged by")
		_ = c.Error(err)
	}

	ret := serializer.ToAccounts(accounts)

	c.JSON(200, ret)

}
