package account

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
	"strings"
)

type SearchRequest struct {
	Keyword   string `binding:"required,min=1,max=100" form:"q"`
	Limit     int    `binding:"omitempty,min=1,max=100" form:"limit"`
	Offset    int    `binding:"min=0"                  form:"offset"`
	Resolve   bool   `form:"resolve"`   // 是否使用 WebFinger 协议 查找， q必须为精确的账号地址 TODO ...
	Following bool   `form:"following"` // 是否只查找关注的账号
}

func Search(c *gin.Context) {
	var req SearchRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.AbortWithStatusJSON(400, gin.H{
			"error": err.Error(), //nolint:goconst
		})

		return
	}

	if req.Limit == 0 {
		req.Limit = 4
	}

	accounts := make([]*models.Account, 0)
	req.Keyword = strings.Trim(req.Keyword, " \t\n@")
	keywordParts := strings.Split(req.Keyword, "@")

	var usernameKeyword, domain string

	if len(keywordParts) == 1 {
		usernameKeyword = keywordParts[0]
	} else {
		usernameKeyword = keywordParts[0]
		domain = keywordParts[1]
	}

	if usernameKeyword == "" {
		c.AbortWithStatusJSON(422, gin.H{
			"error": "invalid keyword",
		})

		return
	}

	query := db.GetDB().Model(&models.Account{}).Where("username LIKE ?", usernameKeyword+"%")

	query = query.Preload("Emojis").Preload("Extra")

	if domain != "" {
		query = query.Where("domain = ?", domain)
	}

	// 只搜索关注的账号
	if req.Following && c.GetBool("authenticated") {
		selfAccount := c.MustGet("account").(*models.Account) //nolint:forcetypeassert
		query = query.Where("id IN (?)", db.GetDB().Model(&models.Follow{}).Scopes(models.Followed).Select("follow_id").Where("account_id = ?", selfAccount.Id))
	}

	err := query.Limit(req.Limit).Offset(req.Offset).Find(&accounts).Error
	if err != nil {
		err = errors.Wrap(err, "search accounts error")
		_ = c.Error(err)

		return
	}

	c.JSON(200, serializer.ToAccounts(accounts))
}
