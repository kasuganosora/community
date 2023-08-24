package controllers

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers/apierrors"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"strconv"
)

type model interface {
	TableName() string
}

type query func(db *gorm.DB) *gorm.DB

func GetIdFromContext[T model](t *T, c *gin.Context, db *gorm.DB, appendQuery ...query) (ret *T) {
	ret = t
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid id"})
		panic("abort")
		return
	}

	query := db.Model(t).Where("id = ?", id)

	for _, q := range appendQuery {
		query = q(query)
	}

	err = query.First(t).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatusJSON(404, gin.H{"error": "not found"})
			panic("abort")
			return
		}

		_ = c.Error(err)
		panic("abort")
		return
	}

	return
}

func GetAccount(c *gin.Context) *models.Account {
	if account, ok := c.Get("account"); ok {
		return account.(*models.Account)
	}

	return nil
}

func MustGetAccount(c *gin.Context) *models.Account {
	account := GetAccount(c)
	if account == nil {
		c.AbortWithStatusJSON(500, gin.H{"error": "must login"})
		panic("abort")
	}

	return account
}

func AbortIfError(c *gin.Context, err error) {
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatusJSON(404, gin.H{"error": "not found"})
			panic("abort")
		}

		_ = c.Error(err)
		c.Abort()
		panic("abort")
	}
}

func AbortIf(c *gin.Context, condition bool, statusCode int, data any) {
	if !condition {
		return
	}

	if data != nil {
		c.AbortWithStatusJSON(statusCode, data)
	} else {
		c.AbortWithStatus(statusCode)
	}
	panic("abort")
}

func AbortIfNotFoundAndOtherError(c *gin.Context, err error, notFoundMessage string) {
	if err == nil {
		return
	}

	if errors.As(err, &apierrors.HttpError{}) {
		c.AbortWithStatusJSON(err.(*apierrors.HttpError).HttpCode, err.(*apierrors.HttpError).Json())
		panic("abort")
		return
	}

	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.AbortWithStatusJSON(404, gin.H{"error": notFoundMessage})
	} else {
		_ = c.Error(err)
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})
	}

	panic("abort")
}
