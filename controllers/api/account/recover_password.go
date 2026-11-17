package account

import (
	"crypto/subtle"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/email"
	"github.com/kasuganosora/community/i18nz"
	"github.com/kasuganosora/community/middleware"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

// errRecoveryTokenRejected means the recovery token was missing, mismatched,
// or expired. It must not change the password or revoke credentials.
var errRecoveryTokenRejected = errors.New("recovery token rejected")

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

	toUrl := systemsettings.GetBaseURL() + "/api/v2/recover_password?token=" + token.Token
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
	pageUrl := systemsettings.GetBaseURL() + "/recover_password"
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

	// 使用常量时间比较，防止时序攻击
	if subtle.ConstantTimeCompare([]byte(token.Token), []byte(req.Token)) != 1 {
		c.JSON(http.StatusNotFound, gin.H{"error": "token error"})

		return
	}

	if token.ExpireAt < time.Now().Unix() {
		c.JSON(http.StatusNotFound, gin.H{"error": "token error"})

		return
	}

	if user.UserSecret == nil {
		err = errors.New("RecoverPassword: user secret missing")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		_ = c.Error(err)

		return
	}

	// Hash before taking the user-row lock. Bcrypt must not run while refresh
	// and recovery are serialized on that lock.
	newPassword, err := models.EncryptPassword(req.Password, user.UserSecret.Salt)
	if err != nil {
		err = errors.Wrap(err, "RecoverPassword")

		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		_ = c.Error(err)

		return
	}

	var revoked []string
	err = db.GetDB().WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		hashes, err := commitPasswordRecovery(tx, user.Id, req.Token, newPassword, time.Now())
		if err != nil {
			return err
		}
		revoked = hashes

		return nil
	})
	if err != nil {
		if errors.Is(err, errRecoveryTokenRejected) {
			c.JSON(http.StatusNotFound, gin.H{"error": "token error"})

			return
		}

		err = errors.Wrap(err, "RecoverPassword")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		_ = c.Error(err)

		return
	}

	for _, hash := range revoked {
		middleware.InvalidateTokenCacheHash(hash)
	}

	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

// commitPasswordRecovery changes the password and revokes previously issued
// web login sessions and persistent credentials, or changes none of them.
// The user-row lock is the same one refresh and device revocation hold until
// commit, so a concurrent refresh cannot insert a credential this recovery
// misses. OAuth tokens are not login sessions and stay valid. Revoke reason
// stays empty so a recovered credential is not treated as a rotation replay.
func commitPasswordRecovery(tx *gorm.DB, userID uint64, rawToken string, passwordHash string, now time.Time) ([]string, error) {
	if err := lockPersistentCredentialFamilies(tx, userID); err != nil {
		return nil, err
	}

	current := &RecoverPasswordVerifyEmailToken{}
	if err := models.GetUserMeta(tx, userID, "recover_password_verify_email_token", current); err != nil {
		if strings.Contains(err.Error(), "user meta not found") {
			return nil, errRecoveryTokenRejected
		}

		return nil, err
	}
	if subtle.ConstantTimeCompare([]byte(current.Token), []byte(rawToken)) != 1 || current.ExpireAt < now.Unix() {
		return nil, errRecoveryTokenRejected
	}

	// Snapshot only after the lock. []uint8 would bind as a single blob.
	var hashes []string
	loginTypes := []int{int(models.AuthTokenTypeWeb), int(models.AuthTokenTypePersistent)}
	if err := tx.Model(&models.AuthToken{}).
		Where("user_id = ? AND revoke IS NULL AND type IN ?", userID, loginTypes).
		Pluck("token", &hashes).Error; err != nil {
		return nil, err
	}
	if len(hashes) > 0 {
		if err := tx.Model(&models.AuthToken{}).
			Where("token IN ? AND revoke IS NULL", hashes).
			Update("revoke", now).Error; err != nil {
			return nil, err
		}
	}

	// Save cannot target this row: the gorm tag "primary_key, column:user_id"
	// is not parsed as a primary key, so Save returns "WHERE conditions required".
	// The explicit user_id update matches change-password.
	res := tx.Model(&models.UserSecretData{}).Where("user_id = ?", userID).
		Updates(map[string]any{
			"password":        passwordHash,
			"password_scheme": "",
		})
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected != 1 {
		return nil, errors.New("password update did not apply")
	}

	if err := models.SaveUserMeta(tx, userID, "recover_password_verify_email_token", nil); err != nil {
		return nil, err
	}

	return hashes, nil
}
