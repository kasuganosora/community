package account

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
	"gorm.io/gorm"
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

	authenticated := c.GetBool("authenticated")
	keyword := strings.TrimSpace(req.Keyword)
	conn := db.GetDB()
	accounts := make([]*models.Account, 0)
	var query *gorm.DB

	if activitypub.IsURLQuery(keyword) {
		// Only remote actor ids are matched exactly; a signed-in resolve fetches
		// unknown ones (profile URLs are followed to the canonical actor).
		var resolved *models.Account
		if req.Resolve && authenticated {
			account, err := activitypub.ResolveSearchAccount(c.Request.Context(), keyword)
			if err != nil {
				log.Warn("accounts/search: resolving %q failed: %v", keyword, err)
			}
			resolved = account
		}
		query = conn.Model(&models.Account{}).Where("accounts.origin_id = ? AND accounts.domain IS NOT NULL", keyword)
		if resolved != nil {
			query = conn.Model(&models.Account{}).Where("accounts.id = ?", resolved.Id)
		}
	} else if acct, ok := activitypub.ParseAcctQuery(keyword); ok {
		// user@domain matches username AND domain; our own domain means local.
		if req.Resolve && authenticated && acct.Domain != "" {
			if _, err := activitypub.ResolveSearchAccount(c.Request.Context(), keyword); err != nil {
				// Mastodon answers with the cached matches instead of an error.
				log.Warn("accounts/search: resolving %q failed: %v", keyword, err)
			}
		}
		query = models.OrderAcctExactFirst(models.WhereAcct(conn.Model(&models.Account{}), acct.Username, acct.Domain, true), acct.Username)
	} else {
		usernameKeyword := strings.Trim(keyword, " \t\n@")
		if usernameKeyword == "" || strings.Contains(usernameKeyword, "@") {
			c.AbortWithStatusJSON(422, gin.H{
				"error": "invalid keyword",
			})

			return
		}
		query = conn.Model(&models.Account{}).Where("accounts.username "+dialect.ILike(conn)+" ? "+dialect.LikeEscape(conn), escapeLike(usernameKeyword)+"%")
		query = models.OrderAcctExactFirst(query, usernameKeyword)
	}

	query = query.Preload("Emojis").Preload("Extra")

	// 只搜索关注的账号
	if req.Following && authenticated {
		selfAccount := c.MustGet("account").(*models.Account) //nolint:forcetypeassert
		query = query.Where("accounts.id IN (?)", db.GetDB().Model(&models.Follow{}).Scopes(models.Followed).Select("follow_id").Where("account_id = ?", selfAccount.Id))
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

// LookupRequest is the query for GET /api/v1/accounts/lookup.
type LookupRequest struct {
	Acct string `binding:"required,min=1,max=500" form:"acct"`
}

// Lookup handles GET /api/v1/accounts/lookup?acct=user[@domain]: an exact,
// case-insensitive match on username and domain without remote resolution
// (Mastodon semantics). A bare username, or our own domain, means a local
// account. It never falls back to a different domain.
func Lookup(c *gin.Context) {
	var req LookupRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": err.Error()})
		return
	}
	value := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(req.Acct), "acct:"), "@")
	username, domain := value, ""
	if acct, ok := activitypub.ParseAcctQuery(value); ok {
		username, domain = acct.Username, acct.Domain
	} else if value == "" || strings.ContainsAny(value, "@/ \t\r\n?#") {
		c.AbortWithStatusJSON(404, gin.H{"error": "Record not found"})
		return
	}

	var account models.Account
	err := models.WhereAcct(db.GetDB().Model(&models.Account{}).Preload("Emojis").Preload("Extra"), username, domain, false).
		Order("accounts.id ASC").First(&account).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatusJSON(404, gin.H{"error": "Record not found"})
			return
		}
		_ = c.Error(errors.Wrap(err, "lookup account"))
		return
	}
	var viewer *models.Account
	if c.GetBool("authenticated") {
		viewer = c.MustGet("account").(*models.Account) //nolint:forcetypeassert
	}
	kept, err := models.FilterAccountsByDomainPolicy(db.GetDB(), viewer, []*models.Account{&account})
	if err != nil {
		_ = c.Error(errors.Wrap(err, "filter blocked domains"))
		return
	}
	if len(kept) == 0 {
		c.AbortWithStatusJSON(404, gin.H{"error": "Record not found"})
		return
	}
	c.JSON(200, serializer.ToAccounts(kept)[0])
}
