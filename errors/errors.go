// Package errors 提供统一的错误处理体系
package errors

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/log"
)

// ErrorCode 错误码类型
type ErrorCode string

// 预定义错误码
const (
	// 通用错误 (1xxx)
	ErrCodeInvalidRequest ErrorCode = "INVALID_REQUEST"    // 400
	ErrCodeUnauthorized   ErrorCode = "UNAUTHORIZED"       // 401
	ErrCodeForbidden      ErrorCode = "FORBIDDEN"          // 403
	ErrCodeNotFound       ErrorCode = "NOT_FOUND"          // 404
	ErrCodeMethodNotAllow ErrorCode = "METHOD_NOT_ALLOWED" // 405
	ErrCodeConflict       ErrorCode = "CONFLICT"           // 409
	ErrCodeTooManyRequest ErrorCode = "RATE_LIMITED"       // 429
	ErrCodeInternal       ErrorCode = "INTERNAL_ERROR"     // 500
	ErrCodeServiceUnavail ErrorCode = "SERVICE_UNAVAILABLE" // 503

	// 认证错误 (2xxx)
	ErrCodeInvalidToken      ErrorCode = "INVALID_TOKEN"
	ErrCodeTokenExpired      ErrorCode = "TOKEN_EXPIRED"
	ErrCodeInvalidCredential ErrorCode = "INVALID_CREDENTIAL"
	ErrCodeAccountDisabled   ErrorCode = "ACCOUNT_DISABLED"
	ErrCodeAccountLocked     ErrorCode = "ACCOUNT_LOCKED"

	// 业务错误 (3xxx)
	ErrCodeValidation      ErrorCode = "VALIDATION_ERROR"
	ErrCodeResourceExhaust ErrorCode = "RESOURCE_EXHAUSTED"
	ErrCodeDuplicateEntry  ErrorCode = "DUPLICATE_ENTRY"
	ErrCodeNotImplemented  ErrorCode = "NOT_IMPLEMENTED"
	ErrCodeDependencyFail  ErrorCode = "DEPENDENCY_FAILURE"
)

// AppError 应用错误结构
type AppError struct {
	Code       ErrorCode `json:"code"`
	Message    string    `json:"message"`
	Detail     string    `json:"detail,omitempty"`
	Status     int       `json:"-"`
	RequestID  string    `json:"request_id,omitempty"`
	InternalID string    `json:"error_id,omitempty"` // 内部错误 ID，用于追踪
}

// Error 实现 error 接口
func (e *AppError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("[%s] %s: %s", e.Code, e.Message, e.Detail)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

// WithDetail 添加详细错误信息
func (e *AppError) WithDetail(detail string) *AppError {
	return &AppError{
		Code:    e.Code,
		Message: e.Message,
		Detail:  detail,
		Status:  e.Status,
	}
}

// WithRequestID 添加请求 ID
func (e *AppError) WithRequestID(requestID string) *AppError {
	return &AppError{
		Code:      e.Code,
		Message:   e.Message,
		Detail:    e.Detail,
		Status:    e.Status,
		RequestID: requestID,
	}
}

// JSON 返回 JSON 格式的错误响应
func (e *AppError) JSON() gin.H {
	response := gin.H{
		"error": gin.H{
			"code":    e.Code,
			"message": e.Message,
		},
	}

	// 开发模式显示详细信息
	if config.C.DevMode == "true" && e.Detail != "" {
		response["error"].(gin.H)["detail"] = e.Detail
	}

	if e.RequestID != "" {
		response["error"].(gin.H)["request_id"] = e.RequestID
	}

	return response
}

// 预定义错误实例
var (
	// 通用错误
	ErrInvalidRequest = NewAppError(ErrCodeInvalidRequest, "Invalid request", http.StatusBadRequest)
	ErrUnauthorized   = NewAppError(ErrCodeUnauthorized, "Unauthorized", http.StatusUnauthorized)
	ErrForbidden      = NewAppError(ErrCodeForbidden, "Forbidden", http.StatusForbidden)
	ErrNotFound       = NewAppError(ErrCodeNotFound, "Resource not found", http.StatusNotFound)
	ErrMethodNotAllow = NewAppError(ErrCodeMethodNotAllow, "Method not allowed", http.StatusMethodNotAllowed)
	ErrConflict       = NewAppError(ErrCodeConflict, "Resource conflict", http.StatusConflict)
	ErrRateLimit      = NewAppError(ErrCodeTooManyRequest, "Rate limit exceeded", http.StatusTooManyRequests)
	ErrInternal       = NewAppError(ErrCodeInternal, "Internal server error", http.StatusInternalServerError)
	ErrServiceUnavail = NewAppError(ErrCodeServiceUnavail, "Service unavailable", http.StatusServiceUnavailable)

	// 认证错误
	ErrInvalidToken      = NewAppError(ErrCodeInvalidToken, "Invalid token", http.StatusUnauthorized)
	ErrTokenExpired      = NewAppError(ErrCodeTokenExpired, "Token expired", http.StatusUnauthorized)
	ErrInvalidCredential = NewAppError(ErrCodeInvalidCredential, "Invalid credentials", http.StatusUnauthorized)
	ErrAccountDisabled   = NewAppError(ErrCodeAccountDisabled, "Account disabled", http.StatusForbidden)
	ErrAccountLocked     = NewAppError(ErrCodeAccountLocked, "Account locked", http.StatusForbidden)

	// 业务错误
	ErrValidation      = NewAppError(ErrCodeValidation, "Validation error", http.StatusBadRequest)
	ErrResourceExhaust = NewAppError(ErrCodeResourceExhaust, "Resource exhausted", http.StatusTooManyRequests)
	ErrDuplicateEntry  = NewAppError(ErrCodeDuplicateEntry, "Duplicate entry", http.StatusConflict)
	ErrNotImplemented  = NewAppError(ErrCodeNotImplemented, "Not implemented", http.StatusNotImplemented)
	ErrDependencyFail  = NewAppError(ErrCodeDependencyFail, "Dependency failure", http.StatusServiceUnavailable)
)

// NewAppError 创建新的应用错误
func NewAppError(code ErrorCode, message string, status int) *AppError {
	return &AppError{
		Code:    code,
		Message: message,
		Status:  status,
	}
}

// NewValidationError 创建验证错误
func NewValidationError(field, message string) *AppError {
	return ErrValidation.WithDetail(fmt.Sprintf("%s: %s", field, message))
}

// NewNotFoundError 创建资源未找到错误
func NewNotFoundError(resource string) *AppError {
	return ErrNotFound.WithDetail(fmt.Sprintf("%s not found", resource))
}

// ErrorHandler 统一错误处理中间件
func ErrorHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		if len(c.Errors) == 0 {
			return
		}

		// 获取最后一个错误
		err := c.Errors.Last().Err

		// 如果是 AppError，使用它
		var appErr *AppError
		if AsAppError(err, &appErr) {
			// 添加请求 ID
			if requestID, exists := c.Get("request_id"); exists {
				appErr = appErr.WithRequestID(requestID.(string))
			}

			// 记录错误日志
			if appErr.Status >= 500 {
				log.WithFields(map[string]interface{}{
					"code":       appErr.Code,
					"status":     appErr.Status,
					"path":       c.Request.URL.Path,
					"method":     c.Request.Method,
					"request_id": appErr.RequestID,
				}).Error("Server error: " + appErr.Detail)
			}

			c.JSON(appErr.Status, appErr.JSON())
			return
		}

		// 处理其他类型的错误
		response := ErrInternal.WithDetail(err.Error())
		if config.C.DevMode != "true" {
			response.Detail = "" // 生产环境不暴露内部错误
		}

		// 添加请求 ID
		if requestID, exists := c.Get("request_id"); exists {
			response = response.WithRequestID(requestID.(string))
		}

		// 记录错误
		log.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"path":       c.Request.URL.Path,
			"method":     c.Request.Method,
			"request_id": response.RequestID,
		}).Error("Unhandled error: " + err.Error())

		c.JSON(http.StatusInternalServerError, response.JSON())
	}
}

// AsAppError 将错误转换为 AppError
func AsAppError(err error, target **AppError) bool {
	if err == nil {
		return false
	}

	// 检查是否是 AppError
	if appErr, ok := err.(*AppError); ok {
		*target = appErr
		return true
	}

	// 检查是否是 gin.Error
	if ginErr, ok := err.(*gin.Error); ok {
		return AsAppError(ginErr.Err, target)
	}

	return false
}

// Response 统一响应结构
type Response struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   *AppError   `json:"error,omitempty"`
	Meta    *Meta       `json:"meta,omitempty"`
}

// Meta 分页元数据
type Meta struct {
	Page       int   `json:"page"`
	PageSize   int   `json:"page_size"`
	Total      int64 `json:"total"`
	TotalPages int   `json:"total_pages"`
}

// Success 成功响应
func Success(data interface{}) *Response {
	return &Response{
		Success: true,
		Data:    data,
	}
}

// SuccessWithMeta 带分页的成功响应
func SuccessWithMeta(data interface{}, meta *Meta) *Response {
	return &Response{
		Success: true,
		Data:    data,
		Meta:    meta,
	}
}

// Fail 失败响应
func Fail(err *AppError) *Response {
	return &Response{
		Success: false,
		Error:   err,
	}
}

// JSON 序列化为 JSON
func (r *Response) JSON() ([]byte, error) {
	return json.Marshal(r)
}

// Send 发送响应
func (r *Response) Send(c *gin.Context, status int) {
	c.JSON(status, r)
}

// SendSuccess 发送成功响应
func SendSuccess(c *gin.Context, data interface{}) {
	Success(data).Send(c, http.StatusOK)
}

// SendCreated 发送创建成功响应
func SendCreated(c *gin.Context, data interface{}) {
	Success(data).Send(c, http.StatusCreated)
}

// SendNoContent 发送无内容响应
func SendNoContent(c *gin.Context) {
	c.Status(http.StatusNoContent)
}

// SendError 发送错误响应
func SendError(c *gin.Context, err *AppError) {
	Fail(err).Send(c, err.Status)
}

// SendValidationError 发送验证错误
func SendValidationError(c *gin.Context, field, message string) {
	SendError(c, NewValidationError(field, message))
}

// SendNotFound 发送资源未找到错误
func SendNotFound(c *gin.Context, resource string) {
	SendError(c, NewNotFoundError(resource))
}

// ErrorWrapper 错误包装器，用于统一处理错误
type ErrorWrapper struct {
	RequestID string
}

// Wrap 包装错误
func (w *ErrorWrapper) Wrap(err error, defaultErr *AppError) *AppError {
	if err == nil {
		return nil
	}

	var appErr *AppError
	if AsAppError(err, &appErr) {
		if w.RequestID != "" {
			appErr = appErr.WithRequestID(w.RequestID)
		}
		return appErr
	}

	result := defaultErr.WithDetail(err.Error())
	if w.RequestID != "" {
		result = result.WithRequestID(w.RequestID)
	}
	return result
}

// WrapWithLog 包装错误并记录日志
func (w *ErrorWrapper) WrapWithLog(err error, defaultErr *AppError, msg string) *AppError {
	appErr := w.Wrap(err, defaultErr)
	if appErr != nil {
		log.WithFields(map[string]interface{}{
			"error": appErr.Code,
			"msg":   msg,
		}).Error(appErr.Detail)
	}
	return appErr
}
