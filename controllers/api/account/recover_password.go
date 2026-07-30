package account

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/email"
	"github.com/kasuganosora/community/i18nz"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type RecoverPasswordVerifyEmailRequest struct {
	Username string `binding:"required" json:"username"`
	Email    string `binding:"required" json:"email"`
}

type RecoverPasswordVerifyEmailToken struct {
	Token    string `json:"token"`
	ExpireAt int64  `json:"expire_at"`
}

func RecoverPasswordVerifyEmail(c *gin.Context) {
	req := &RecoverPasswordVerifyEmailRequest{}
	if err := c.ShouldBind(req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()}) //nolint:goconst

		return
	}

	// 找出这个用户
	user := &models.User{}
	emailHash := models.GetEmailHash(req.Email)

	err := db.GetDB().Where("username = ?", req.Username).First(user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "user name or email error"})

			return
		}

		err = errors.Wrap(err, "RecoverPasswordVerifyEmail")
		_ = c.Error(err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"}) //nolint:goconst

		return
	}

	if user.EmailHash != emailHash {
		c.JSON(http.StatusNotFound, gin.H{"error": "user name or email error"})

		return
	}

	// 生成一个恢复密码用的 token
	token := RecoverPasswordVerifyEmailToken{
		Token:    utils.GetRandomString(32) + "-" + strconv.FormatUint(user.Id, 10) + "-" + uuid.New().String(),
		ExpireAt: time.Now().Unix() + 5*60, // 5 分钟后过期
	}

	// 保存到数据库
	err = models.SaveUserMeta(db.GetDB(), user.Id, "recover_password_verify_email_token", token)
	if err != nil {
		err = errors.Wrap(err, "RecoverPasswordVerifyEmail")

		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		_ = c.Error(err)

		return
	}

	// 发送邮件

	var acceptLanguage string
	if langStr, ok := user.Properties["language"].(string); ok && langStr != "" {
		acceptLanguage = langStr
	} else {
		acceptLanguage = c.Request.Header.Get("Accept-Language")
	}

	toUrl := config.C.AppBaseURL + "api/v2/recover_password?token=" + token.Token
	mailData := map[string]any{
		"UserName": user.UserName, //nolint:goconst
		"Token":    token.Token,
		"SiteName": systemsettings.GetString(systemsettings.KeyTitle, "Community"), //nolint:goconst
		"URL":      toUrl,
	}

	mailTemplate := i18nz.GetMailTemplateName(acceptLanguage, "recover_password_verify_email")
	subject := i18nz.GetLocalizer("", acceptLanguage).T("recover_password_verify_email_subject", map[string]any{
		"UserName": user.UserName,
		"SiteName": systemsettings.GetString(systemsettings.KeyTitle, "Community"),
	})

	err = email.SendHtmlTemplate(req.Email, "", "", subject, "", mailTemplate, mailData)
	if err != nil {
		err = errors.Wrap(err, "RecoverPasswordVerifyEmail")

		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		_ = c.Error(err)

		return
	}

	// 重定向到前端页面
	pageUrl := config.C.AppBaseURL + "recover_password"
	c.Redirect(http.StatusFound, pageUrl)
}

type RecoverPasswordRequest struct {
	Token    string `binding:"required" json:"Token"`
	Password string `binding:"required" json:"password"`
}

func RecoverPassword(c *gin.Context) {
	req := &RecoverPasswordRequest{}

	if err := c.ShouldBindQuery(req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})

		return
	}

	// 从token中解析出用户id
	tokenParts := strings.Split(req.Token, "-")
	if len(tokenParts) < 3 {
		c.JSON(http.StatusNotFound, gin.H{"error": "token error"})

		return
	}

	userId, err := strconv.ParseInt(tokenParts[1], 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "token error"})

		return
	}

	// 找出这个用户
	user := &models.User{}

	err = db.GetDB().Preload("UserSecret").Where("id = ?", userId).First(user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "token error"})

			return
		}

		err = errors.Wrap(err, "RecoverPassword")

		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		_ = c.Error(err)

		return
	}

	// 检查token是否正确
	token := &RecoverPasswordVerifyEmailToken{}

	err = models.GetUserMeta(db.GetDB(), user.Id, "recover_password_verify_email_token", token)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "token error"})

		return
	}

	if token.Token != req.Token {
		c.JSON(http.StatusNotFound, gin.H{"error": "token error"})

		return
	}

	if token.ExpireAt < time.Now().Unix() {
		c.JSON(http.StatusNotFound, gin.H{"error": "token error"})

		return
	}

	// 设置新密码
	user.UserSecret.Password, err = models.EncryptPassword(req.Password, user.UserSecret.Salt)
	if err != nil {
		err = errors.Wrap(err, "RecoverPassword")

		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		_ = c.Error(err)

		return
	}

	err = db.GetDB().Save(user.UserSecret).Error
	if err != nil {
		err = errors.Wrap(err, "RecoverPassword")

		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		_ = c.Error(err)

		return
	}

	// 删除token
	err = models.SaveUserMeta(db.GetDB(), user.Id, "recover_password_verify_email_token", nil)
	if err != nil {
		err = errors.Wrap(err, "RecoverPassword")
		_ = c.Error(err)

		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}
