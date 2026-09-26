package list

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
	"gorm.io/gorm"
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
		query = query.Where("list_accounts.id <= ?", *req.MaxId)
	}

	if req.MinId != nil {
		query = query.Where("list_accounts.id >= ?", *req.MinId)
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

	c.JSON(200, serializer.ToAccounts(accounts))
}

type AddAccountsToListRequest struct {
	AccountIds []string `binding:"required" json:"account_ids"`
}

func AddAccountToList(c *gin.Context) {
	req := AddAccountsToListRequest{}
	if err := c.ShouldBindJSON(&req); err != nil {
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

	uint64AccountIds := make([]uint64, 0, len(req.AccountIds))

	for _, accountId := range req.AccountIds {
		uint64AccountId, err := strconv.ParseUint(accountId, 10, 64)
		if err != nil {
			continue
		}

		uint64AccountIds = append(uint64AccountIds, uint64AccountId)
	}

	if len(uint64AccountIds) == 0 {
		c.AbortWithStatusJSON(400, gin.H{
			"apierrors": "invalid account ids",
		})

		return
	}

	// 取出已经存在的账号，并且在插入的时候忽略
	existRecords := make([]*models.ListAccount, 0)

	err := db.GetDB().Model(&models.ListAccount{}).
		Where("list_id = ?", list.Id).
		Where("account_id IN ?", uint64AccountIds).
		Find(&existRecords).Error
	if err != nil {
		err = errors.Wrap(err, "AddAccountsToList error")
		_ = c.Error(err)

		return
	}

	existAccountIdsMap := make(map[uint64]bool)
	for _, existRecord := range existRecords {
		existAccountIdsMap[existRecord.AccountId] = true
	}

	unExistAccountIds := make([]uint64, 0, len(uint64AccountIds))

	for _, uint64AccountId := range uint64AccountIds {
		if _, ok := existAccountIdsMap[uint64AccountId]; !ok {
			unExistAccountIds = append(unExistAccountIds, uint64AccountId)
		}
	}

	if len(unExistAccountIds) == 0 {
		c.JSON(200, gin.H{
			"error": "all accounts already exist",
		})

		return
	}

	// 插入新的账号
	listAccounts := make([]*models.ListAccount, 0, len(unExistAccountIds))

	for _, unExistAccountId := range unExistAccountIds {
		listAccount := &models.ListAccount{
			ListId:    list.Id,
			AccountId: unExistAccountId,
		}
		listAccounts = append(listAccounts, listAccount)
	}

	err = db.GetDB().Create(&listAccounts).Error
	if err != nil {
		err = errors.Wrap(err, "AddAccountsToList error")
		_ = c.Error(err)

		return
	}

	// Trigger proxy follow for remote accounts that have no local followers
	go ensureProxyFollowsForList(unExistAccountIds, list.Id)

	c.Data(200, "application/json", nil)
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

	// The route is DELETE /lists/:id/accounts (Mastodon passes account_ids[]
	// in the query or body); there is no :account_id path param, so reading
	// only c.Param("account_id") made every removal fail with 400.
	accountId, err := strconv.ParseUint(removeAccountIdParam(c), 10, 64)
	if err != nil {
		c.AbortWithStatusJSON(400, gin.H{
			"apierrors": "invalid account id",
		})

		return
	}

	listAccount := &models.ListAccount{}

	err = db.GetDB().Where("list_id = ?", list.Id).
		Where("account_id = ?", accountId).
		First(listAccount).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatusJSON(404, gin.H{
				"apierrors": "account not found in list",
			})

			return
		}

		err = errors.Wrap(err, "RemoveAccountIdFromList error")
		_ = c.Error(err)

		return
	}

	err = db.GetDB().Delete(listAccount).Error
	if err != nil {
		err = errors.Wrap(err, "RemoveAccountIdFromList error")
		_ = c.Error(err)

		return
	}

	// Trigger proxy follow cleanup for remote accounts
	go func() {
		var acc models.Account
		err := db.GetDB().First(&acc, accountId).Error
		if err != nil {
			return
		}

		if acc.Domain != nil {
			proxyService := activitypub.NewProxyService()
			err := proxyService.CleanupProxyFollow(acc.Id, models.ProxyFollowReasonList, list.Id)
			if err != nil {
				log.Error("Failed to cleanup proxy follow for account %d in list %d: %v", acc.Id, list.Id, err)
			}
		}
	}()

	c.Data(200, "application/json", nil)
}

// removeAccountIdParam returns the first account id from account_ids[] /
// account_ids (query string; net/http does not parse DELETE form bodies), a JSON body {"account_ids": [...]}, or the
// legacy :account_id path param.
func removeAccountIdParam(c *gin.Context) string {
	if id := c.Param("account_id"); id != "" {
		return id
	}

	for _, key := range []string{"account_ids[]", "account_ids"} {
		if ids := c.QueryArray(key); len(ids) > 0 {
			return ids[0]
		}
	}

	var body AddAccountsToListRequest
	if err := c.ShouldBindJSON(&body); err == nil && len(body.AccountIds) > 0 {
		return body.AccountIds[0]
	}

	return ""
}
