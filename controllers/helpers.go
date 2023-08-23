package controllers

import (
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"strconv"
)

func GetIdFromContext[T any](t *T, c *gin.Context, db *gorm.DB) (ret *T, abort bool) {
	ret = t
	abort = true
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid id"})
		return
	}

	err = db.First(ret, id).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatusJSON(404, gin.H{"error": "not found"})
			return
		}
	}

	abort = false

	return
}
