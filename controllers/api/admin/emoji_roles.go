package admin

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
)

// EmojiRoles exposes only the role labels needed for emoji reaction restrictions.
// The router requires a current administrator and the admin read scope.
func EmojiRoles(c *gin.Context) {
	roles := make([]models.Role, 0)
	if err := db.GetDB().Select("id", "name").Order("id ASC").Find(&roles).Error; err != nil {
		_ = c.Error(err)
		c.AbortWithStatusJSON(500, gin.H{"error": "could not load emoji roles"})
		return
	}
	result := make([]gin.H, 0, len(roles))
	for _, role := range roles {
		result = append(result, gin.H{"id": strconv.FormatInt(role.ID, 10), "name": role.Name})
	}
	c.JSON(200, result)
}
