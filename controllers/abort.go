package controllers

import (
	"github.com/gin-gonic/gin"
)

// AbortSentinel 是用于控制流的 panic 值，区别于真正的 panic
// 通过专门的 recovery handler 捕获，不影响真正的 panic 处理.
type AbortSentinel struct{}

// abortSentinel 全局哨兵实例.
var abortSentinel = AbortSentinel{}

// AbortPanic 发送一个控制流 panic，由 RecoveryMiddleware 捕获.
func AbortPanic() {
	panic(abortSentinel)
}

// IsAbortSentinel 判断 recover 到的值是否是 AbortSentinel.
func IsAbortSentinel(r any) bool {
	_, ok := r.(AbortSentinel)

	return ok
}

// AbortRecoveryMiddleware 专门捕获 AbortSentinel panic 的中间件
// 应该在 gin.Recovery() 之前注册，确保只有真正的 panic 被 gin.Recovery() 处理.
func AbortRecoveryMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				if IsAbortSentinel(r) {
					// 这是控制流 panic，已经被 AbortWithStatusJSON 处理过响应
					// 不需要额外操作
					return
				}
				// 真正的 panic，重新 panic 让 gin.Recovery() 处理
				panic(r)
			}
		}()

		c.Next()
	}
}
