package account

import (
	"crypto/rand"
	"math/big"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
)

// InviteResponse represents the invite response.
type InviteResponse struct {
	InviteCode     string `json:"invite_code"`
	InstanceApiUrl string `json:"instance_api_url"`
}

// InviteInfo represents detailed invite information.
type InviteInfo struct {
	Id        string `json:"id"`
	Code      string `json:"code"`
	Uses      int    `json:"uses"`
	MaxUses   int    `json:"max_uses"`
	ExpiresAt string `json:"expires_at,omitempty"`
	CreatedAt string `json:"created_at"`
}

// isInviteValid checks if an invite is still valid.
func isInviteValid(invite *models.InviteCode) bool {
	if invite.Disabled {
		return false
	}

	if invite.MaxUseCount > 0 && invite.UseCount >= invite.MaxUseCount {
		return false
	}

	if invite.ExpireAt != nil && invite.ExpireAt.Before(time.Now()) {
		return false
	}

	return true
}

// GetInvite handles GET /api/v1/invites/:code
// Returns information about an invite code.
func GetInvite(c *gin.Context) {
	code := c.Param("code")
	if code == "" {
		c.JSON(400, gin.H{"error": "invite code required"}) //nolint:goconst

		return
	}

	// Look up the invite
	var invite models.InviteCode

	err := db.GetDB().Where("code = ?", code).First(&invite).Error
	if err != nil {
		c.JSON(404, gin.H{"error": "invite not found"})

		return
	}

	// Check if invite is still valid
	if !isInviteValid(&invite) {
		c.JSON(401, gin.H{"error": "invite is no longer valid"})

		return
	}

	c.JSON(200, InviteResponse{
		InviteCode:     code,
		InstanceApiUrl: systemsettings.GetBaseURL() + "/api/v2/instance",
	})
}

// ListInvites handles GET /api/v1/invites
// Returns all invites created by the current user.
func ListInvites(c *gin.Context) {
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	var invites []models.InviteCode

	db.GetDB().Where("user_id = ?", account.UserId).Find(&invites)

	result := make([]InviteInfo, 0, len(invites))

	for _, invite := range invites {
		info := InviteInfo{
			Id:      strconv.FormatInt(invite.Id, 10),
			Code:    invite.Code,
			Uses:    invite.UseCount,
			MaxUses: invite.MaxUseCount,
		}
		if invite.CreatedAt != nil {
			info.CreatedAt = invite.CreatedAt.Format("2006-01-02T15:04:05.000Z")
		}

		if invite.ExpireAt != nil {
			info.ExpiresAt = invite.ExpireAt.Format("2006-01-02T15:04:05.000Z")
		}

		result = append(result, info)
	}

	c.JSON(200, result)
}

// CreateInvite handles POST /api/v1/invites
// Creates a new invite code.
func CreateInvite(c *gin.Context) {
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	var req struct {
		MaxUses   int `json:"max_uses"`
		ExpiresIn int `json:"expires_in"` // in seconds
	}

	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})

		return
	}

	// Generate invite code
	code := generateInviteCode()

	now := time.Now()
	invite := models.InviteCode{
		Code:        code,
		UserId:      account.UserId,
		AccountId:   strconv.FormatUint(account.Id, 10),
		MaxUseCount: req.MaxUses,
		UseCount:    0,
		CreatedAt:   &now,
	}

	if req.ExpiresIn > 0 {
		expiresAt := now.Add(time.Duration(req.ExpiresIn) * time.Second)
		invite.ExpireAt = &expiresAt
	}

	err = db.GetDB().Create(&invite).Error
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to create invite"})

		return
	}

	c.JSON(200, InviteInfo{
		Id:        strconv.FormatInt(invite.Id, 10),
		Code:      invite.Code,
		Uses:      invite.UseCount,
		MaxUses:   invite.MaxUseCount,
		CreatedAt: invite.CreatedAt.Format("2006-01-02T15:04:05.000Z"),
	})
}

// generateInviteCode generates a random invite code.
func generateInviteCode() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

	b := make([]byte, 16)
	for i := range b {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			b[i] = charset[0]
			continue
		}

		b[i] = charset[idx.Int64()]
	}

	return string(b)
}
