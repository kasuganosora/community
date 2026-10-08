package account

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
	"strings"
)

type SearchRequest struct {
	Keyword   string `binding:"required,min=1,max=500" form:"q"`
	Limit     int    `binding:"omitempty,min=1,max=100" form:"limit"`
	Offset    int    `binding:"min=0"                  form:"offset"`
	Resolve   bool   `form:"resolve"`   // 是否解析精确的远端账号地址
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

	if req.Resolve && c.GetBool("authenticated") {
		resolved, err := activitypub.ResolveSearchAccount(c.Request.Context(), req.Keyword)
		if err != nil {
			c.AbortWithStatusJSON(422, gin.H{"error": "remote account could not be resolved"})
			return
		}
		if resolved != nil && resolved.Domain != nil {
			req.Keyword = resolved.Username + "@" + *resolved.Domain
		}
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

	conn := db.GetDB()
	query := conn.Model(&models.Account{}).Where("username "+dialect.ILike(conn)+" ? "+dialect.LikeEscape(conn), escapeLike(usernameKeyword)+"%")

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

	var viewer *models.Account
	if c.GetBool("authenticated") {
		viewer = c.MustGet("account").(*models.Account) //nolint:forcetypeassert
	}
	accounts, err = models.FilterAccountsByDomainPolicy(db.GetDB(), viewer, accounts)
	if err != nil {
		_ = c.Error(errors.Wrap(err, "filter blocked domains"))

		return
	}

	c.JSON(200, serializer.ToAccounts(accounts))
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "%", `\%`)
	s = strings.ReplaceAll(s, "_", `\_`)
	return s
}
