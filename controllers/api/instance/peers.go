package instance

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
)

// Peers handles GET /api/v1/instance/peers
// Returns all known domains this instance is aware of
func Peers(c *gin.Context) {
	// Get distinct domains from accounts
	var domains []string
	db.GetDB().Model(&models.Account{}).
		Where("domain IS NOT NULL AND domain != ''").
		Distinct("domain").
		Pluck("domain", &domains)

	c.JSON(200, domains)
}
