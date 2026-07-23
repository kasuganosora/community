package apierrors

import (
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

type HttpError struct {
	HttpCode int
	error
}

func (e *HttpError) GetHttpCode() int {
	return e.HttpCode
}

func (e *HttpError) Json() map[string]any {
	return map[string]any{
		"error": e.Error(),
	}
}

func (e *HttpError) WriteResponse(c *gin.Context) {
	c.AbortWithStatusJSON(e.HttpCode, e.Json())
}

func NewHttpError(err error) error {
	err = &HttpError{
		HttpCode: 500,
		error:    err,
	}

	err = errors.WithStack(err)

	return err
}

func NewHttpErrorWithCode(err error, code int) error {
	err = &HttpError{
		HttpCode: code,
		error:    err,
	}

	return err
}

func NewHttpErrorWithCodeAndMessage(_ error, code int, message string) error {
	err := &HttpError{
		HttpCode: code,
		error:    errors.New(message),
	}

	return err
}

func WarpToHttpError(err error, httpCode ...int) error {
	if err == nil {
		return nil
	}

	var httpError *HttpError
	if errors.As(err, &httpError) {
		return httpError
	}

	ht := 500

	if len(httpCode) > 0 {
		ht = httpCode[0]
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		err = NewHttpErrorWithCodeAndMessage(err, 404, "record not found")

		return err
	}

	return NewHttpErrorWithCode(err, ht)
}
