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
// 必须在 gin.Recovery() **之后**注册（即成为更内层的中间件），使其 defer
// 先于 gin.Recovery 的 recover 执行，从而静默吞掉哨兵 panic。
// 如果不注册此中间件，gin.Recovery() 会将每次 AbortPanic() 都当作真正的
// panic 处理，在日志中打印冗余的堆栈跟踪。
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
