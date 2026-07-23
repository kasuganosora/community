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
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid id"}) //nolint:goconst
		AbortPanic()
	}

	query := db.Model(t).Where("id = ?", id)

	for _, q := range appendQuery {
		query = q(query)
	}

	err = query.First(t).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatusJSON(404, gin.H{"error": "not found"})
			AbortPanic()
		}

		_ = c.Error(err)

		AbortPanic()
	}

	return
}

func GetAccount(c *gin.Context) *models.Account {
	if account, ok := c.Get("account"); ok {
		return account.(*models.Account) //nolint:forcetypeassert
	}

	return nil
}

func MustGetAccount(c *gin.Context) *models.Account {
	account := GetAccount(c)
	if account == nil {
		c.AbortWithStatusJSON(500, gin.H{"error": "must login"})
		AbortPanic()
	}

	return account
}

func AbortIfError(c *gin.Context, err error) {
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatusJSON(404, gin.H{"error": "not found"})
			AbortPanic()
		}

		_ = c.Error(err)
		c.Abort()
		AbortPanic()
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

	AbortPanic()
}

func AbortIfNotFoundAndOtherError(c *gin.Context, err error, notFoundMessage string) {
	if err == nil {
		return
	}

	if errors.As(err, &apierrors.HttpError{}) {
		c.AbortWithStatusJSON(func() *apierrors.HttpError {
			target := &apierrors.HttpError{}
			_ = errors.As(err, &target)
			return target
		}().HttpCode, func() *apierrors.HttpError {
			target := &apierrors.HttpError{}
			_ = errors.As(err, &target)
			return target
		}().Json())
		AbortPanic()
	}

	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.AbortWithStatusJSON(404, gin.H{"error": notFoundMessage})
	} else {
		_ = c.Error(err)
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})
	}

	AbortPanic()
}
