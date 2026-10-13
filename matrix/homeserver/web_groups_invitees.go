package homeserver

import (
	"strings"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
)

type webGroupInviteeOption struct {
	UserID      string `json:"user_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url"`
}

// Suggestions are only active Community identities on this server. Resolving
// remote Matrix IDs remains an explicit invitation, never a keystroke effect.
func handleWebGroupInvitees(c *gin.Context) {
	var req struct {
		Q     string `form:"q" binding:"max=255"`
		Limit int    `form:"limit" binding:"omitempty,min=1,max=20"`
	}
	if err := c.ShouldBindQuery(&req); err != nil || strings.ContainsFunc(req.Q, unicode.IsControl) {
		webGroupError(c, ErrInvalidParam)
		return
	}
	if req.Limit == 0 {
		req.Limit = 8
	}
	hs := GetHomeServer()
	query := strings.TrimPrefix(strings.TrimSpace(req.Q), "@")
	users := []webGroupInviteeOption{}
	// A fully qualified local Matrix ID can still be completed. An AP handle,
	// external Matrix ID, or pasted URL must not fall back to a namesake here.
	if local, server, qualified := strings.Cut(query, ":"); qualified {
		if !strings.EqualFold(server, hs.Config.ServerName) {
			c.JSON(200, gin.H{"users": users})
			return
		}
		query = local
	}
	if query == "" || strings.ContainsAny(query, "@/\\") {
		c.JSON(200, gin.H{"users": users})
		return
	}
	conn := db.GetDB().WithContext(c.Request.Context())
	local, _, _ := ParseMatrixUserID(getUserID(c))
	pattern := models.EscapeLike(query)
	var accounts []*models.Account
	search := conn.Model(&models.Account{}).
		Select("accounts.id", "accounts.username", "accounts.display_name", "accounts.avatar", "accounts.avatar_static").
		Joins("JOIN users AS invitee_users ON invitee_users.id = accounts.user_id AND invitee_users.deleted_at IS NULL").
		Where("accounts.domain IS NULL AND accounts.user_id > 0 AND accounts.is_ban = ? AND invitee_users.is_ban = ? AND invitee_users.status = ?", false, false, models.UserStatusNormal).
		Where("LOWER(accounts.username) <> ?", strings.ToLower(local)).
		Where("(accounts.username "+dialect.ILike(conn)+" ? "+dialect.LikeEscape(conn)+" OR accounts.display_name "+dialect.ILike(conn)+" ? "+dialect.LikeEscape(conn)+")", pattern+"%", "%"+pattern+"%")
	if err := models.OrderAcctExactFirst(search, query).Limit(req.Limit).Find(&accounts).Error; err != nil {
		webGroupInternalError(c, "search local invitees", err)
		return
	}
	for _, account := range accounts {
		profile := serializer.ToAccount(account)
		name := account.DisplayName
		if name == "" {
			name = account.Username
		}
		users = append(users, webGroupInviteeOption{
			UserID: FormatMatrixUserID(account.Username, hs.Config.ServerName), Username: account.Username,
			DisplayName: name, AvatarURL: profile.Avatar,
		})
	}
	c.JSON(200, gin.H{"users": users})
}
