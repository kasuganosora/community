package apierrors

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestHttpError_GetHttpCode(t *testing.T) {
	err := &HttpError{HttpCode: 404, error: errors.New("not found")}
	assert.Equal(t, 404, err.GetHttpCode())
}

func TestHttpError_Json(t *testing.T) {
	err := &HttpError{HttpCode: 400, error: errors.New("bad request")}
	result := err.Json()
	assert.Equal(t, "bad request", result["error"])
}

func TestHttpError_Error(t *testing.T) {
	err := &HttpError{HttpCode: 500, error: errors.New("internal error")}
	assert.Equal(t, "internal error", err.Error())
}

func TestNewHttpError(t *testing.T) {
	original := errors.New("something went wrong")
	err := NewHttpError(original)
	require.Error(t, err)

	var httpErr *HttpError

	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, 500, httpErr.HttpCode)
}

func TestNewHttpErrorWithCode(t *testing.T) {
	original := errors.New("forbidden")
	err := NewHttpErrorWithCode(original, 403)

	var httpErr *HttpError

	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, 403, httpErr.HttpCode)
}

func TestNewHttpErrorWithCodeAndMessage(t *testing.T) {
	original := errors.New("original")
	err := NewHttpErrorWithCodeAndMessage(original, 422, "custom message")

	var httpErr *HttpError

	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, 422, httpErr.HttpCode)
	assert.Equal(t, "custom message", httpErr.Error())
}

func TestWarpToHttpError(t *testing.T) {
	t.Run("nil error returns nil", func(t *testing.T) {
		result := WarpToHttpError(nil)
		assert.NoError(t, result)
	})

	t.Run("already HttpError returns same", func(t *testing.T) {
		original := &HttpError{HttpCode: 403, error: errors.New("forbidden")}
		result := WarpToHttpError(original)

		var httpErr *HttpError

		require.ErrorAs(t, result, &httpErr)
		assert.Equal(t, 403, httpErr.HttpCode)
	})

	t.Run("gorm ErrRecordNotFound returns 404", func(t *testing.T) {
		result := WarpToHttpError(gorm.ErrRecordNotFound)

		var httpErr *HttpError

		require.ErrorAs(t, result, &httpErr)
		assert.Equal(t, 404, httpErr.HttpCode)
		assert.Equal(t, "record not found", httpErr.Error())
	})

	t.Run("generic error returns 500", func(t *testing.T) {
		result := WarpToHttpError(errors.New("database error"))

		var httpErr *HttpError

		require.ErrorAs(t, result, &httpErr)
		assert.Equal(t, 500, httpErr.HttpCode)
	})

	t.Run("custom http code", func(t *testing.T) {
		result := WarpToHttpError(errors.New("conflict"), 409)

		var httpErr *HttpError

		require.ErrorAs(t, result, &httpErr)
		assert.Equal(t, 409, httpErr.HttpCode)
	})
}
