package errors

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// ============================================================================
// ErrorCode constants
// ============================================================================

func TestErrorCodeConstants(t *testing.T) {
	// General error codes
	assert.Equal(t, ErrCodeInvalidRequest, ErrorCode("INVALID_REQUEST"))
	assert.Equal(t, ErrCodeUnauthorized, ErrorCode("UNAUTHORIZED"))
	assert.Equal(t, ErrCodeForbidden, ErrorCode("FORBIDDEN"))
	assert.Equal(t, ErrCodeNotFound, ErrorCode("NOT_FOUND"))
	assert.Equal(t, ErrCodeMethodNotAllow, ErrorCode("METHOD_NOT_ALLOWED"))
	assert.Equal(t, ErrCodeConflict, ErrorCode("CONFLICT"))
	assert.Equal(t, ErrCodeTooManyRequest, ErrorCode("RATE_LIMITED"))
	assert.Equal(t, ErrCodeInternal, ErrorCode("INTERNAL_ERROR"))
	assert.Equal(t, ErrCodeServiceUnavail, ErrorCode("SERVICE_UNAVAILABLE"))

	// Auth error codes
	assert.Equal(t, ErrCodeInvalidToken, ErrorCode("INVALID_TOKEN"))
	assert.Equal(t, ErrCodeTokenExpired, ErrorCode("TOKEN_EXPIRED"))
	assert.Equal(t, ErrCodeInvalidCredential, ErrorCode("INVALID_CREDENTIAL"))
	assert.Equal(t, ErrCodeAccountDisabled, ErrorCode("ACCOUNT_DISABLED"))
	assert.Equal(t, ErrCodeAccountLocked, ErrorCode("ACCOUNT_LOCKED"))

	// Business error codes
	assert.Equal(t, ErrCodeValidation, ErrorCode("VALIDATION_ERROR"))
	assert.Equal(t, ErrCodeResourceExhaust, ErrorCode("RESOURCE_EXHAUSTED"))
	assert.Equal(t, ErrCodeDuplicateEntry, ErrorCode("DUPLICATE_ENTRY"))
	assert.Equal(t, ErrCodeNotImplemented, ErrorCode("NOT_IMPLEMENTED"))
	assert.Equal(t, ErrCodeDependencyFail, ErrorCode("DEPENDENCY_FAILURE"))
}

// ============================================================================
// NewAppError
// ============================================================================

func TestNewAppError(t *testing.T) {
	err := NewAppError(ErrCodeInternal, "something broke", http.StatusInternalServerError)
	assert.NotNil(t, err)
	assert.Equal(t, ErrCodeInternal, err.Code)
	assert.Equal(t, "something broke", err.Message)
	assert.Equal(t, http.StatusInternalServerError, err.Status)
	assert.Empty(t, err.Detail)
	assert.Empty(t, err.RequestID)
	assert.Empty(t, err.InternalID)
}

func TestNewAppError_DifferentCodes(t *testing.T) {
	tests := []struct {
		name    string
		code    ErrorCode
		message string
		status  int
	}{
		{"bad request", ErrCodeInvalidRequest, "bad input", http.StatusBadRequest},
		{"unauthorized", ErrCodeUnauthorized, "not logged in", http.StatusUnauthorized},
		{"forbidden", ErrCodeForbidden, "no access", http.StatusForbidden},
		{"not found", ErrCodeNotFound, "gone", http.StatusNotFound},
		{"method not allowed", ErrCodeMethodNotAllow, "wrong verb", http.StatusMethodNotAllowed},
		{"conflict", ErrCodeConflict, "already exists", http.StatusConflict},
		{"rate limited", ErrCodeTooManyRequest, "slow down", http.StatusTooManyRequests},
		{"internal", ErrCodeInternal, "oops", http.StatusInternalServerError},
		{"service unavailable", ErrCodeServiceUnavail, "maintenance", http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewAppError(tt.code, tt.message, tt.status)
			assert.Equal(t, tt.code, err.Code)
			assert.Equal(t, tt.message, err.Message)
			assert.Equal(t, tt.status, err.Status)
		})
	}
}

// ============================================================================
// AppError.Error()
// ============================================================================

func TestAppError_Error_WithoutDetail(t *testing.T) {
	err := NewAppError(ErrCodeInternal, "server error", http.StatusInternalServerError)
	assert.Equal(t, "[INTERNAL_ERROR] server error", err.Error())
}

func TestAppError_Error_WithDetail(t *testing.T) {
	err := NewAppError(ErrCodeInternal, "server error", http.StatusInternalServerError)
	err.Detail = "database connection refused"
	assert.Equal(t, "[INTERNAL_ERROR] server error: database connection refused", err.Error())
}

func TestAppError_Error_EmptyMessage(t *testing.T) {
	err := NewAppError(ErrCodeInternal, "", http.StatusInternalServerError)
	assert.Equal(t, "[INTERNAL_ERROR] ", err.Error())
}

func TestAppError_Error_EmptyCode(t *testing.T) {
	err := NewAppError("", "some message", http.StatusInternalServerError)
	assert.Equal(t, "[] some message", err.Error())
}

// ============================================================================
// AppError.WithDetail()
// ============================================================================

func TestAppError_WithDetail(t *testing.T) {
	original := NewAppError(ErrCodeValidation, "validation failed", http.StatusBadRequest)
	withDetail := original.WithDetail("email is required")

	assert.Equal(t, original.Code, withDetail.Code)
	assert.Equal(t, original.Message, withDetail.Message)
	assert.Equal(t, original.Status, withDetail.Status)
	assert.Equal(t, "email is required", withDetail.Detail)
	// Original should be unchanged
	assert.Empty(t, original.Detail)
}

func TestAppError_WithDetail_EmptyString(t *testing.T) {
	original := NewAppError(ErrCodeValidation, "validation failed", http.StatusBadRequest)
	withDetail := original.WithDetail("")
	assert.Empty(t, withDetail.Detail)
}

func TestAppError_WithDetail_PreservesOriginal(t *testing.T) {
	original := NewAppError(ErrCodeInternal, "internal error", http.StatusInternalServerError)
	original.Detail = "original detail"

	withDetail := original.WithDetail("new detail")
	// WithDetail should NOT copy original detail (as per implementation)
	assert.Equal(t, "new detail", withDetail.Detail)
	assert.Equal(t, "original detail", original.Detail)
}

// ============================================================================
// AppError.WithRequestID()
// ============================================================================

func TestAppError_WithRequestID(t *testing.T) {
	original := NewAppError(ErrCodeInternal, "internal error", http.StatusInternalServerError)
	withID := original.WithRequestID("req-123")

	assert.Equal(t, original.Code, withID.Code)
	assert.Equal(t, original.Message, withID.Message)
	assert.Equal(t, original.Status, withID.Status)
	assert.Equal(t, "req-123", withID.RequestID)
	assert.Empty(t, original.RequestID)
}

func TestAppError_WithRequestID_PreservesDetail(t *testing.T) {
	original := NewAppError(ErrCodeInternal, "internal error", http.StatusInternalServerError)
	original.Detail = "some detail"
	withID := original.WithRequestID("req-456")

	assert.Equal(t, "some detail", withID.Detail)
	assert.Equal(t, "req-456", withID.RequestID)
}

func TestAppError_WithRequestID_EmptyString(t *testing.T) {
	original := NewAppError(ErrCodeInternal, "internal error", http.StatusInternalServerError)
	withID := original.WithRequestID("")
	assert.Empty(t, withID.RequestID)
}

// ============================================================================
// AppError.JSON()
// ============================================================================

func TestAppError_JSON_Basic(t *testing.T) {
	err := NewAppError(ErrCodeInternal, "server error", http.StatusInternalServerError)
	result := err.JSON()

	errMap, ok := result["error"].(gin.H)
	assert.True(t, ok)
	assert.Equal(t, ErrCodeInternal, errMap["code"])
	assert.Equal(t, "server error", errMap["message"])
	assert.Nil(t, errMap["detail"])
	assert.Nil(t, errMap["request_id"])
}

func TestAppError_JSON_WithRequestID(t *testing.T) {
	err := NewAppError(ErrCodeInternal, "server error", http.StatusInternalServerError).WithRequestID("req-abc")
	result := err.JSON()

	errMap, ok := result["error"].(gin.H)
	assert.True(t, ok)
	assert.Equal(t, "req-abc", errMap["request_id"])
}

func TestAppError_JSON_DevModeWithDetail(t *testing.T) {
	// Save and restore config
	origDevMode := config.C.DevMode

	config.C.DevMode = "true" //nolint:goconst

	defer func() { config.C.DevMode = origDevMode }()

	err := NewAppError(ErrCodeInternal, "server error", http.StatusInternalServerError).WithDetail("db failed")
	result := err.JSON()

	errMap, ok := result["error"].(gin.H)
	assert.True(t, ok)
	assert.Equal(t, "db failed", errMap["detail"])
}

func TestAppError_JSON_ProductionModeHidesDetail(t *testing.T) {
	origDevMode := config.C.DevMode

	config.C.DevMode = "false"

	defer func() { config.C.DevMode = origDevMode }()

	err := NewAppError(ErrCodeInternal, "server error", http.StatusInternalServerError).WithDetail("db failed")
	result := err.JSON()

	errMap, ok := result["error"].(gin.H)
	assert.True(t, ok)
	// In production mode, detail should NOT be in the JSON
	_, hasDetail := errMap["detail"]
	assert.False(t, hasDetail)
}

func TestAppError_JSON_DevModeWithoutDetail(t *testing.T) {
	origDevMode := config.C.DevMode

	config.C.DevMode = "true"

	defer func() { config.C.DevMode = origDevMode }()

	err := NewAppError(ErrCodeInternal, "server error", http.StatusInternalServerError)
	result := err.JSON()

	errMap, ok := result["error"].(gin.H)
	assert.True(t, ok)

	_, hasDetail := errMap["detail"]
	assert.False(t, hasDetail)
}

// ============================================================================
// Predefined error instances
// ============================================================================

func TestPredefinedErrors(t *testing.T) {
	tests := []struct {
		name    string
		err     *AppError
		code    ErrorCode
		message string
		status  int
	}{
		{"ErrInvalidRequest", ErrInvalidRequest, ErrCodeInvalidRequest, "Invalid request", http.StatusBadRequest},
		{"ErrUnauthorized", ErrUnauthorized, ErrCodeUnauthorized, "Unauthorized", http.StatusUnauthorized},
		{"ErrForbidden", ErrForbidden, ErrCodeForbidden, "Forbidden", http.StatusForbidden},
		{"ErrNotFound", ErrNotFound, ErrCodeNotFound, "Resource not found", http.StatusNotFound},
		{"ErrMethodNotAllow", ErrMethodNotAllow, ErrCodeMethodNotAllow, "Method not allowed", http.StatusMethodNotAllowed},
		{"ErrConflict", ErrConflict, ErrCodeConflict, "Resource conflict", http.StatusConflict},
		{"ErrRateLimit", ErrRateLimit, ErrCodeTooManyRequest, "Rate limit exceeded", http.StatusTooManyRequests},
		{"ErrInternal", ErrInternal, ErrCodeInternal, "Internal server error", http.StatusInternalServerError},
		{"ErrServiceUnavail", ErrServiceUnavail, ErrCodeServiceUnavail, "Service unavailable", http.StatusServiceUnavailable},
		{"ErrInvalidToken", ErrInvalidToken, ErrCodeInvalidToken, "Invalid token", http.StatusUnauthorized},
		{"ErrTokenExpired", ErrTokenExpired, ErrCodeTokenExpired, "Token expired", http.StatusUnauthorized},
		{"ErrInvalidCredential", ErrInvalidCredential, ErrCodeInvalidCredential, "Invalid credentials", http.StatusUnauthorized},
		{"ErrAccountDisabled", ErrAccountDisabled, ErrCodeAccountDisabled, "Account disabled", http.StatusForbidden},
		{"ErrAccountLocked", ErrAccountLocked, ErrCodeAccountLocked, "Account locked", http.StatusForbidden},
		{"ErrValidation", ErrValidation, ErrCodeValidation, "Validation error", http.StatusBadRequest},
		{"ErrResourceExhaust", ErrResourceExhaust, ErrCodeResourceExhaust, "Resource exhausted", http.StatusTooManyRequests},
		{"ErrDuplicateEntry", ErrDuplicateEntry, ErrCodeDuplicateEntry, "Duplicate entry", http.StatusConflict},
		{"ErrNotImplemented", ErrNotImplemented, ErrCodeNotImplemented, "Not implemented", http.StatusNotImplemented},
		{"ErrDependencyFail", ErrDependencyFail, ErrCodeDependencyFail, "Dependency failure", http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.NotNil(t, tt.err)
			assert.Equal(t, tt.code, tt.err.Code)
			assert.Equal(t, tt.message, tt.err.Message)
			assert.Equal(t, tt.status, tt.err.Status)
		})
	}
}

// ============================================================================
// NewValidationError
// ============================================================================

func TestNewValidationError(t *testing.T) {
	err := NewValidationError("email", "is required")
	assert.Equal(t, ErrCodeValidation, err.Code)
	assert.Equal(t, "Validation error", err.Message)
	assert.Equal(t, http.StatusBadRequest, err.Status)
	assert.Equal(t, "email: is required", err.Detail)
}

func TestNewValidationError_EmptyField(t *testing.T) {
	err := NewValidationError("", "is required")
	assert.Equal(t, ": is required", err.Detail)
}

func TestNewValidationError_EmptyMessage(t *testing.T) {
	err := NewValidationError("email", "")
	assert.Equal(t, "email: ", err.Detail)
}

// ============================================================================
// NewNotFoundError
// ============================================================================

func TestNewNotFoundError(t *testing.T) {
	err := NewNotFoundError("user")
	assert.Equal(t, ErrCodeNotFound, err.Code)
	assert.Equal(t, "Resource not found", err.Message)
	assert.Equal(t, http.StatusNotFound, err.Status)
	assert.Equal(t, "user not found", err.Detail)
}

func TestNewNotFoundError_EmptyResource(t *testing.T) {
	err := NewNotFoundError("")
	assert.Equal(t, " not found", err.Detail)
}

// ============================================================================
// AsAppError
// ============================================================================

func TestAsAppError_Nil(t *testing.T) {
	var appErr *AppError

	result := AsAppError(nil, &appErr)
	assert.False(t, result)
	assert.Nil(t, appErr)
}

func TestAsAppError_AppError(t *testing.T) {
	original := NewAppError(ErrCodeInternal, "server error", http.StatusInternalServerError)

	var appErr *AppError

	result := AsAppError(original, &appErr)
	assert.True(t, result)
	assert.Equal(t, original, appErr)
}

func TestAsAppError_RegularError(t *testing.T) {
	var appErr *AppError

	result := AsAppError(assert.AnError, &appErr)
	assert.False(t, result)
	assert.Nil(t, appErr)
}

func TestAsAppError_GinError(t *testing.T) {
	appErrInner := NewAppError(ErrCodeInternal, "server error", http.StatusInternalServerError)
	ginErr := &gin.Error{Err: appErrInner}

	var appErr *AppError

	result := AsAppError(ginErr, &appErr)
	assert.True(t, result)
	assert.Equal(t, appErrInner, appErr)
}

func TestAsAppError_GinErrorWithRegularError(t *testing.T) {
	ginErr := &gin.Error{Err: assert.AnError}

	var appErr *AppError

	result := AsAppError(ginErr, &appErr)
	assert.False(t, result)
	assert.Nil(t, appErr)
}

func TestAsAppError_ImplementsError(t *testing.T) {
	err := NewAppError(ErrCodeNotFound, "not found", http.StatusNotFound)

	var _ error = err // Compile-time check

	assert.Implements(t, (*error)(nil), err)
}

// ============================================================================
// ErrorHandler middleware
// ============================================================================

func TestErrorHandler_NoErrors(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/test", nil) //nolint:noctx

	handler := ErrorHandler()
	handler(c)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestErrorHandler_AppError_Below500(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/test", nil) //nolint:noctx

	handler := ErrorHandler()
	_ = c.Error(ErrNotFound) // 404
	handler(c)

	assert.Equal(t, http.StatusNotFound, w.Code)

	var response map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	errObj, ok := response["error"].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "NOT_FOUND", errObj["code"])
	assert.Equal(t, "Resource not found", errObj["message"])
}

func TestErrorHandler_AppError_WithRequestID(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/test", nil) //nolint:noctx
	c.Set("request_id", "req-xyz-123")

	handler := ErrorHandler()
	_ = c.Error(ErrNotFound)
	handler(c)

	assert.Equal(t, http.StatusNotFound, w.Code)

	var response map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	errObj, ok := response["error"].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "req-xyz-123", errObj["request_id"])
}

func TestErrorHandler_AppError_500(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/test", nil) //nolint:noctx

	handler := ErrorHandler()
	_ = c.Error(ErrInternal) // 500
	handler(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)

	var response map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	errObj, ok := response["error"].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "INTERNAL_ERROR", errObj["code"])
}

func TestErrorHandler_NonAppError_DevMode(t *testing.T) {
	origDevMode := config.C.DevMode

	config.C.DevMode = "true"

	defer func() { config.C.DevMode = origDevMode }()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/test", nil) //nolint:noctx

	handler := ErrorHandler()
	_ = c.Error(assert.AnError)
	handler(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)

	var response map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	errObj, ok := response["error"].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "INTERNAL_ERROR", errObj["code"])
	// In dev mode, detail should be visible
	assert.Equal(t, assert.AnError.Error(), errObj["detail"])
}

func TestErrorHandler_NonAppError_ProductionMode(t *testing.T) {
	origDevMode := config.C.DevMode

	config.C.DevMode = "false"

	defer func() { config.C.DevMode = origDevMode }()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/test", nil) //nolint:noctx

	handler := ErrorHandler()
	_ = c.Error(assert.AnError)
	handler(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)

	var response map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	errObj, ok := response["error"].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "INTERNAL_ERROR", errObj["code"])
	// In production mode, detail should NOT be present
	_, hasDetail := errObj["detail"]
	assert.False(t, hasDetail)
}

func TestErrorHandler_NonAppError_WithRequestID(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/test", nil) //nolint:noctx
	c.Set("request_id", "req-abc")

	handler := ErrorHandler()
	_ = c.Error(assert.AnError)
	handler(c)

	var response map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	errObj, ok := response["error"].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "req-abc", errObj["request_id"])
}

func TestErrorHandler_MultipleErrors_UsesLast(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/test", nil) //nolint:noctx

	handler := ErrorHandler()
	_ = c.Error(ErrInvalidRequest) // 400
	_ = c.Error(ErrNotFound)       // 404 - last one
	handler(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ============================================================================
// Response struct
// ============================================================================

func TestSuccess(t *testing.T) {
	data := map[string]string{"key": "value"}
	resp := Success(data)

	assert.True(t, resp.Success)
	assert.Equal(t, data, resp.Data)
	assert.Nil(t, resp.Error)
	assert.Nil(t, resp.Meta)
}

func TestSuccess_NilData(t *testing.T) {
	resp := Success(nil)
	assert.True(t, resp.Success)
	assert.Nil(t, resp.Data)
}

func TestSuccessWithMeta(t *testing.T) {
	data := []string{"a", "b"}
	meta := &Meta{Page: 1, PageSize: 10, Total: 100, TotalPages: 10}
	resp := SuccessWithMeta(data, meta)

	assert.True(t, resp.Success)
	assert.Equal(t, data, resp.Data)
	assert.Equal(t, meta, resp.Meta)
	assert.Nil(t, resp.Error)
}

func TestSuccessWithMeta_NilMeta(t *testing.T) {
	resp := SuccessWithMeta("data", nil)
	assert.True(t, resp.Success)
	assert.Nil(t, resp.Meta)
}

func TestFail(t *testing.T) {
	err := NewAppError(ErrCodeInternal, "failed", http.StatusInternalServerError)
	resp := Fail(err)

	assert.False(t, resp.Success)
	assert.Equal(t, err, resp.Error)
	assert.Nil(t, resp.Data)
}

func TestResponse_JSON(t *testing.T) {
	data := map[string]string{"name": "test"}
	resp := Success(data)
	b, err := resp.JSON()
	require.NoError(t, err)
	assert.Contains(t, string(b), `"success":true`)
	assert.Contains(t, string(b), `"name":"test"`)
}

func TestResponse_JSON_Error(t *testing.T) {
	appErr := NewAppError(ErrCodeValidation, "bad input", http.StatusBadRequest).WithDetail("field required")
	resp := Fail(appErr)
	b, jsonErr := resp.JSON()
	require.NoError(t, jsonErr)
	assert.Contains(t, string(b), `"success":false`)
	assert.Contains(t, string(b), `"VALIDATION_ERROR"`)
}

func TestResponse_JSON_WithMeta(t *testing.T) {
	meta := &Meta{Page: 2, PageSize: 20, Total: 50, TotalPages: 3}
	resp := SuccessWithMeta([]int{1, 2}, meta)
	b, err := resp.JSON()
	require.NoError(t, err)
	assert.Contains(t, string(b), `"page":2`)
	assert.Contains(t, string(b), `"page_size":20`)
	assert.Contains(t, string(b), `"total":50`)
	assert.Contains(t, string(b), `"total_pages":3`)
}

// ============================================================================
// Response.Send
// ============================================================================

func TestResponse_Send(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/test", nil) //nolint:noctx

	resp := Success(map[string]string{"msg": "hello"})
	resp.Send(c, http.StatusOK)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any

	_ = json.Unmarshal(w.Body.Bytes(), &body)
	assert.Equal(t, true, body["success"])
}

// ============================================================================
// Send helpers
// ============================================================================

func TestSendSuccess(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/test", nil) //nolint:noctx

	SendSuccess(c, map[string]string{"result": "ok"})

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any

	_ = json.Unmarshal(w.Body.Bytes(), &body)
	assert.Equal(t, true, body["success"])
}

func TestSendCreated(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/test", nil) //nolint:noctx

	SendCreated(c, map[string]string{"id": "123"})

	assert.Equal(t, http.StatusCreated, w.Code)

	var body map[string]any

	_ = json.Unmarshal(w.Body.Bytes(), &body)
	assert.Equal(t, true, body["success"])
}

func TestSendNoContent(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodDelete, "/test", nil) //nolint:noctx

	SendNoContent(c)

	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestSendError(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/test", nil) //nolint:noctx

	err := NewAppError(ErrCodeForbidden, "access denied", http.StatusForbidden)
	SendError(c, err)

	assert.Equal(t, http.StatusForbidden, w.Code)

	var body map[string]any

	_ = json.Unmarshal(w.Body.Bytes(), &body)
	assert.Equal(t, false, body["success"])
}

func TestSendValidationError(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/test", nil) //nolint:noctx

	SendValidationError(c, "email", "is invalid")

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var body map[string]any

	_ = json.Unmarshal(w.Body.Bytes(), &body)
	assert.Equal(t, false, body["success"])

	errObj := body["error"].(map[string]any) //nolint:forcetypeassert
	assert.Equal(t, "VALIDATION_ERROR", errObj["code"])
}

func TestSendNotFound(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/test", nil) //nolint:noctx

	SendNotFound(c, "user")

	assert.Equal(t, http.StatusNotFound, w.Code)

	var body map[string]any

	_ = json.Unmarshal(w.Body.Bytes(), &body)
	assert.Equal(t, false, body["success"])

	errObj := body["error"].(map[string]any) //nolint:forcetypeassert
	assert.Equal(t, "NOT_FOUND", errObj["code"])
}

// ============================================================================
// ErrorWrapper
// ============================================================================

func TestErrorWrapper_Wrap_NilError(t *testing.T) {
	w := &ErrorWrapper{RequestID: "req-1"}
	result := w.Wrap(nil, ErrInternal)
	assert.Nil(t, result)
}

func TestErrorWrapper_Wrap_AppError(t *testing.T) {
	w := &ErrorWrapper{RequestID: "req-1"}
	appErr := NewAppError(ErrCodeNotFound, "not found", http.StatusNotFound)
	result := w.Wrap(appErr, ErrInternal)

	assert.NotNil(t, result)
	assert.Equal(t, ErrCodeNotFound, result.Code)
	assert.Equal(t, "req-1", result.RequestID)
}

func TestErrorWrapper_Wrap_AppError_NoRequestID(t *testing.T) {
	w := &ErrorWrapper{}
	appErr := NewAppError(ErrCodeNotFound, "not found", http.StatusNotFound)
	result := w.Wrap(appErr, ErrInternal)

	assert.NotNil(t, result)
	assert.Empty(t, result.RequestID)
}

func TestErrorWrapper_Wrap_RegularError(t *testing.T) {
	w := &ErrorWrapper{RequestID: "req-2"}
	result := w.Wrap(assert.AnError, ErrInternal)

	assert.NotNil(t, result)
	assert.Equal(t, ErrCodeInternal, result.Code)
	assert.Equal(t, "Internal server error", result.Message)
	assert.Equal(t, assert.AnError.Error(), result.Detail)
	assert.Equal(t, "req-2", result.RequestID)
}

func TestErrorWrapper_Wrap_RegularError_NoRequestID(t *testing.T) {
	w := &ErrorWrapper{}
	result := w.Wrap(assert.AnError, ErrInternal)

	assert.NotNil(t, result)
	assert.Empty(t, result.RequestID)
}

func TestErrorWrapper_WrapWithLog_NilError(t *testing.T) {
	w := &ErrorWrapper{}
	result := w.WrapWithLog(nil, ErrInternal, "test log")
	assert.Nil(t, result)
}

func TestErrorWrapper_WrapWithLog_AppError(t *testing.T) {
	w := &ErrorWrapper{RequestID: "req-3"}
	appErr := NewAppError(ErrCodeValidation, "validation error", http.StatusBadRequest)
	result := w.WrapWithLog(appErr, ErrInternal, "something failed")

	assert.NotNil(t, result)
	assert.Equal(t, ErrCodeValidation, result.Code)
	assert.Equal(t, "req-3", result.RequestID)
}

func TestErrorWrapper_WrapWithLog_RegularError(t *testing.T) {
	w := &ErrorWrapper{RequestID: "req-4"}
	result := w.WrapWithLog(assert.AnError, ErrInternal, "operation failed")

	assert.NotNil(t, result)
	assert.Equal(t, ErrCodeInternal, result.Code)
	assert.Equal(t, "req-4", result.RequestID)
}

// ============================================================================
// Meta struct
// ============================================================================

func TestMeta_Fields(t *testing.T) {
	meta := &Meta{
		Page:       3,
		PageSize:   25,
		Total:      150,
		TotalPages: 6,
	}
	assert.Equal(t, 3, meta.Page)
	assert.Equal(t, 25, meta.PageSize)
	assert.Equal(t, int64(150), meta.Total)
	assert.Equal(t, 6, meta.TotalPages)
}

func TestMeta_JSONTags(t *testing.T) {
	meta := &Meta{Page: 1, PageSize: 10, Total: 5, TotalPages: 1}
	b, err := json.Marshal(meta)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"page":1`)
	assert.Contains(t, string(b), `"page_size":10`)
	assert.Contains(t, string(b), `"total":5`)
	assert.Contains(t, string(b), `"total_pages":1`)
}

// ============================================================================
// AppError JSON tags
// ============================================================================

func TestAppError_JSONSerialization(t *testing.T) {
	err := &AppError{
		Code:       ErrCodeInternal,
		Message:    "something failed",
		Detail:     "db connection refused",
		Status:     http.StatusInternalServerError,
		RequestID:  "req-abc",
		InternalID: "int-123",
	}

	b, marshalErr := json.Marshal(err)
	require.NoError(t, marshalErr)

	var result map[string]any

	_ = json.Unmarshal(b, &result)

	assert.Equal(t, "INTERNAL_ERROR", result["code"])
	assert.Equal(t, "something failed", result["message"])
	assert.Equal(t, "db connection refused", result["detail"])
	assert.Equal(t, "req-abc", result["request_id"])
	assert.Equal(t, "int-123", result["error_id"])
	// Status has "-" tag, should not be serialized
	_, hasStatus := result["Status"]
	assert.False(t, hasStatus)
}

func TestAppError_JSONSerialization_OmitEmpty(t *testing.T) {
	err := &AppError{
		Code:    ErrCodeNotFound,
		Message: "not found",
		Status:  http.StatusNotFound,
	}

	b, marshalErr := json.Marshal(err)
	require.NoError(t, marshalErr)

	var result map[string]any

	_ = json.Unmarshal(b, &result)

	assert.Equal(t, "NOT_FOUND", result["code"])
	assert.Equal(t, "not found", result["message"])
	// Detail, RequestID, InternalID should be omitted
	assert.NotContains(t, string(b), "detail")
	assert.NotContains(t, string(b), "request_id")
	assert.NotContains(t, string(b), "error_id")
}

// ============================================================================
// Chained operations
// ============================================================================

func TestAppError_ChainedOperations(t *testing.T) {
	err := NewAppError(ErrCodeValidation, "validation failed", http.StatusBadRequest).
		WithDetail("email is invalid").
		WithRequestID("chain-req-123")

	assert.Equal(t, ErrCodeValidation, err.Code)
	assert.Equal(t, "validation failed", err.Message)
	assert.Equal(t, "email is invalid", err.Detail)
	assert.Equal(t, http.StatusBadRequest, err.Status)
	assert.Equal(t, "chain-req-123", err.RequestID)

	// Verify Error() output
	expected := "[VALIDATION_ERROR] validation failed: email is invalid"
	assert.Equal(t, expected, err.Error())
}

func TestAppError_ChainedWithRequestIDThenDetail(t *testing.T) {
	// WithRequestID preserves Detail, and WithDetail also preserves RequestID
	// (both methods preserve all other fields — consistent builder semantics).
	err := NewAppError(ErrCodeInternal, "error", http.StatusInternalServerError).
		WithRequestID("req-1").
		WithDetail("detail")

	assert.Equal(t, "detail", err.Detail)
	// RequestID is preserved because WithDetail now copies all existing fields
	assert.Equal(t, "req-1", err.RequestID)
}

// ============================================================================
// Edge cases
// ============================================================================

func TestNewAppError_ZeroStatus(t *testing.T) {
	err := NewAppError(ErrCodeInternal, "error", 0)
	assert.Equal(t, 0, err.Status)
}

func TestErrorHandler_UnwrappedErrorInGinError(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/test", nil) //nolint:noctx

	handler := ErrorHandler()
	// Pass a regular error wrapped in gin.Error
	ginErr := &gin.Error{Err: assert.AnError}
	c.Errors = append(c.Errors, ginErr)
	handler(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestErrorHandler_AppErrorWithDetail(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/test", nil) //nolint:noctx

	handler := ErrorHandler()
	errWithDetail := NewNotFoundError("user")
	_ = c.Error(errWithDetail)
	handler(c)

	assert.Equal(t, http.StatusNotFound, w.Code)

	var response map[string]any

	_ = json.Unmarshal(w.Body.Bytes(), &response)
	errObj := response["error"].(map[string]any) //nolint:forcetypeassert
	assert.Equal(t, "NOT_FOUND", errObj["code"])
	// Detail should not be exposed in production
	_, hasDetail := errObj["detail"]
	assert.False(t, hasDetail)
}

func TestErrorHandler_AppErrorWithDetail_DevMode(t *testing.T) {
	origDevMode := config.C.DevMode

	config.C.DevMode = "true"

	defer func() { config.C.DevMode = origDevMode }()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/test", nil) //nolint:noctx

	handler := ErrorHandler()
	errWithDetail := NewNotFoundError("user")
	_ = c.Error(errWithDetail)
	handler(c)

	assert.Equal(t, http.StatusNotFound, w.Code)

	var response map[string]any

	_ = json.Unmarshal(w.Body.Bytes(), &response)
	errObj := response["error"].(map[string]any) //nolint:forcetypeassert
	assert.Equal(t, "NOT_FOUND", errObj["code"])
	// In dev mode, detail should be exposed
	assert.Equal(t, "user not found", errObj["detail"])
}

func TestResponse_JSON_EmptySuccess(t *testing.T) {
	resp := &Response{Success: true}
	b, err := resp.JSON()
	require.NoError(t, err)
	assert.Contains(t, string(b), `"success":true`)
}

func TestResponse_JSON_SerializeData(t *testing.T) {
	type Item struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}

	data := []Item{{ID: 1, Name: "test"}}
	resp := Success(data)
	b, err := resp.JSON()
	require.NoError(t, err)
	assert.Contains(t, string(b), `"id":1`)
	assert.Contains(t, string(b), `"name":"test"`)
}

func TestErrorHandler_WithGinErrorWrappingAppError(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/test", nil) //nolint:noctx

	handler := ErrorHandler()
	// Wrapping AppError inside gin.Error
	appErr := NewAppError(ErrCodeConflict, "conflict", http.StatusConflict)
	ginErr := &gin.Error{Err: appErr, Type: gin.ErrorTypePrivate}
	c.Errors = append(c.Errors, ginErr)
	handler(c)

	assert.Equal(t, http.StatusConflict, w.Code)

	var response map[string]any

	_ = json.Unmarshal(w.Body.Bytes(), &response)
	errObj := response["error"].(map[string]any) //nolint:forcetypeassert
	assert.Equal(t, "CONFLICT", errObj["code"])
}

func TestAsAppError_NestedGinError(t *testing.T) {
	// Double-wrapped: gin.Error containing gin.Error containing AppError
	inner := NewAppError(ErrCodeInternal, "inner", http.StatusInternalServerError)
	ginInner := &gin.Error{Err: inner}
	ginOuter := &gin.Error{Err: ginInner}

	var appErr *AppError
	// AsAppError only unwraps one level of gin.Error
	result := AsAppError(ginOuter, &appErr)
	// The outer gin.Error contains an inner gin.Error, not an AppError directly
	// So AsAppError will try to unwrap one level and find ginInner, then recurse
	// Actually looking at the code: AsAppError checks if err is *gin.Error, then recurses on ginErr.Err
	// So it should handle one level of wrapping
	// But ginOuter.Err is ginInner which is *gin.Error, so it recurses again and finds inner which is *AppError
	assert.True(t, result)
	assert.Equal(t, inner, appErr)
}

func TestErrorWrapper_WrapWithLog_RegularErrorWithDefaultErr(t *testing.T) {
	w := &ErrorWrapper{RequestID: "req-5"}
	defaultErr := NewAppError(ErrCodeServiceUnavail, "service down", http.StatusServiceUnavailable)
	result := w.WrapWithLog(assert.AnError, defaultErr, "service call failed")

	assert.NotNil(t, result)
	assert.Equal(t, ErrCodeServiceUnavail, result.Code)
	assert.Equal(t, "service down", result.Message)
	assert.Equal(t, assert.AnError.Error(), result.Detail)
	assert.Equal(t, "req-5", result.RequestID)
	assert.Equal(t, http.StatusServiceUnavailable, result.Status)
}

func TestErrorHandler_ResponseAlreadyWritten(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(ErrorHandler())
	r.GET("/test", func(c *gin.Context) {
		_ = c.Error(ErrInternal)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create status"})
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/test", nil)) //nolint:noctx

	assert.Equal(t, http.StatusInternalServerError, w.Code)

	// The body must be exactly the handler's JSON, not two concatenated objects.
	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response), "body: %s", w.Body.String())
	assert.Equal(t, "failed to create status", response["error"])
}
