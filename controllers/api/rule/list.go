package rule

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
)

func List(c *gin.Context) {
	rules := make([]*models.Rule, 0)

	err := db.GetDB().Model(&models.Rule{}).Order("priority desc, created_at asc").Find(&rules).Error
	if err != nil {
		_ = c.Error(err)
		return
	}

	ret := serializer.ToRules(rules)

	c.JSON(200, ret)
}
