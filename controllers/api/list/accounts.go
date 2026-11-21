package list

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/controllers/api/common"
	"github.com/kasuganosora/community/db"
	listfeed "github.com/kasuganosora/community/feed/list"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type GetListAccountsRequest struct {
	MaxId   *uint64 `form:"max_id"`
	MinId   *uint64 `form:"min_id"`
	Limit   *uint64 `form:"limit"`
	SinceId *uint64 `form:"since_id"`
}

func GetListAccounts(c *gin.Context) {
	req := GetListAccountsRequest{}

	if err := c.ShouldBindQuery(&req); err != nil {
		c.AbortWithStatusJSON(400, gin.H{
			"error": err.Error(), //nolint:goconst
		})

		return
	}

	var currentAccount *models.Account
	if _currentAccount, ok := c.Get("account"); ok {
		currentAccount = _currentAccount.(*models.Account) //nolint:forcetypeassert
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
			"error": "forbidden", //nolint:goconst
		})

		return
	}

	if req.SinceId != nil {
		req.MinId = req.SinceId
	}

	accounts := make([]*models.Account, 0)
	query := db.GetDB().Model(&models.Account{}).
		Joins("JOIN list_accounts ON list_accounts.account_id = accounts.id").
		Where("list_accounts.list_id = ?", list.Id).
		Order("list_accounts.id DESC").
		Select("accounts.*")

	if req.MaxId != nil {
		query = query.Where("list_accounts.id < ?", *req.MaxId)
	}

	if req.MinId != nil {
		query = query.Where("list_accounts.id > ?", *req.MinId)
	}

	if req.Limit != nil {
		query = query.Limit(int(*req.Limit)) //nolint:gosec // G115: reviewed and safe
	}

	query = query.Preload("Emojis").Preload("Extra")

	err = query.Find(&accounts).Error
	if err != nil {
		err = errors.Wrap(err, "GetListAccounts error")
		_ = c.Error(err)

		return
	}

	setListAccountLinks(c, list.Id, accounts)

	c.JSON(200, serializer.ToAccounts(accounts))
}

// setListAccountLinks paginates by the list_accounts row id, which is not the
// account id, so look the cursors up for the returned page.
func setListAccountLinks(c *gin.Context, listID uint64, accounts []*models.Account) {
	if len(accounts) == 0 {
		return
	}

	ids := make([]uint64, 0, len(accounts))
	for _, a := range accounts {
		ids = append(ids, a.Id)
	}

	var rows []uint64

	err := db.GetDB().Table("list_accounts").Where("list_id = ? AND account_id IN ?", listID, ids).
		Order("id DESC").Pluck("id", &rows).Error
	if err != nil || len(rows) == 0 {
		return
	}

	common.SetPageLinksUint(c, rows[0], rows[len(rows)-1], true)
}

type AddAccountsToListRequest struct {
	AccountIds []string `binding:"required" json:"account_ids"`
}

func AddAccountToList(c *gin.Context) {
	req := AddAccountsToListRequest{}
	if err := controllers.BindBody(c, &req); err != nil {
		c.AbortWithStatusJSON(400, gin.H{
			"error": err.Error(),
		})

		return
	}

	list, abort := GetListByParam(c)

	if abort {
		return
	}

	selfAccount := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	if list.AccountId != selfAccount.Id {
		c.AbortWithStatusJSON(403, gin.H{
			"apierrors": "you are not the owner of the list", //nolint:goconst
		})

		return
	}

	ids := make([]uint64, 0, len(req.AccountIds))
	for _, raw := range req.AccountIds {
		if id, err := strconv.ParseUint(raw, 10, 64); err == nil && id != 0 {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid account ids"})
		return
	}

	var added []uint64
	err := db.GetDB().WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		// Read existing memberships after locking their list, including on
		// older installations without a unique (list_id, account_id) index.
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").First(&models.List{}, list.Id).Error; err != nil {
			return err
		}
		var existing []uint64
		if err := tx.Model(&models.ListAccount{}).Where("list_id = ? AND account_id IN ?", list.Id, ids).Pluck("account_id", &existing).Error; err != nil {
			return err
		}
		seen := make(map[uint64]bool, len(ids))
		for _, id := range existing {
			seen[id] = true
		}
		var rows []models.ListAccount
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				rows = append(rows, models.ListAccount{ListId: list.Id, AccountId: id})
				added = append(added, id)
			}
		}
		if len(rows) == 0 {
			return nil
		}
		return tx.Create(&rows).Error
	})
	if err != nil {
		_ = c.Error(errors.Wrap(err, "add accounts to list"))
		return
	}
	if len(added) > 0 {
		listfeed.DropCache(list.Id)
		go ensureProxyFollowsForList(added, list.Id)
	}

	c.JSON(200, gin.H{})
}

// ensureProxyFollowsForList checks each account and triggers proxy follow if needed.
func ensureProxyFollowsForList(accountIDs []uint64, listID uint64) {
	proxyService := activitypub.NewProxyService()

	for _, accountID := range accountIDs {
		var acc models.Account
		err := db.GetDB().First(&acc, accountID).Error
		if err != nil {
			continue
		}

		if acc.Domain != nil { // Remote account
			err := proxyService.EnsureProxyFollow(acc.Id, models.ProxyFollowReasonList, listID)
			if err != nil {
				log.Error("Failed to ensure proxy follow for account %d in list %d: %v", acc.Id, listID, err)
			}
		}
	}
}

func RemoveAccountFromList(c *gin.Context) {
	list, abort := GetListByParam(c)

	if abort {
		return
	}

	selfAccount := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	if list.AccountId != selfAccount.Id {
		c.AbortWithStatusJSON(403, gin.H{
			"apierrors": "you are not the owner of the list",
		})

		return
	}

	rawIDs := removeAccountIDs(c)
	if len(rawIDs) == 0 {
		c.AbortWithStatusJSON(400, gin.H{"error": "account ids required"})
		return
	}
	ids := make([]uint64, 0, len(rawIDs))
	for _, rawID := range rawIDs {
		id, err := strconv.ParseUint(rawID, 10, 64)
		if err != nil || id == 0 {
			c.AbortWithStatusJSON(400, gin.H{"error": "invalid account id"})
			return
		}
		ids = append(ids, id)
	}

	var removed []models.ListAccount
	var remoteIDs []uint64
	err := db.GetDB().WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("list_id = ? AND account_id IN ?", list.Id, ids).Find(&removed).Error; err != nil || len(removed) == 0 {
			return err
		}
		removedIDs := make([]uint64, 0, len(removed))
		for _, row := range removed {
			removedIDs = append(removedIDs, row.AccountId)
		}
		if err := tx.Model(&models.Account{}).Where("id IN ? AND domain IS NOT NULL", removedIDs).Pluck("id", &remoteIDs).Error; err != nil {
			return err
		}
		return tx.Delete(&removed).Error
	})
	if err != nil {
		_ = c.Error(errors.Wrap(err, "remove accounts from list"))
		return
	}
	if len(removed) > 0 {
		listfeed.DropCache(list.Id)
	}
	if len(remoteIDs) > 0 {
		go cleanupListProxyFollows(remoteIDs, list.Id)
	}
	c.JSON(200, gin.H{})
}

func cleanupListProxyFollows(remoteIDs []uint64, listID uint64) {
	proxy := activitypub.NewProxyService()
	for _, id := range remoteIDs {
		if err := proxy.CleanupProxyFollow(id, models.ProxyFollowReasonList, listID); err != nil {
			log.Error("Failed to cleanup proxy follow for account %d in list %d: %v", id, listID, err)
		}
	}
}

func removeAccountIDs(c *gin.Context) []string {
	if id := c.Param("account_id"); id != "" {
		return []string{id}
	}
	for _, key := range []string{"account_ids[]", "account_ids"} {
		if ids := c.QueryArray(key); len(ids) > 0 {
			return ids
		}
	}
	var body AddAccountsToListRequest
	if err := controllers.BindBody(c, &body); err != nil {
		return nil
	}
	return body.AccountIds
}
