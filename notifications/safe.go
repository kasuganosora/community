package notifications

import (
	"fmt"
	"runtime/debug"

	"github.com/kasuganosora/community/log"
)

// safeGo 在新 goroutine 中执行 fn，并捕获 panic 以防止整个进程崩溃。
// 通知系统的所有 goroutine 都应通过 safeGo 启动，确保单个通知失败不会影响主进程。
func safeGo(name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Error(fmt.Sprintf("[safeGo:%s] recovered from panic: %v\n%s", name, r, debug.Stack()))
			}
		}()
		fn()
	}()
}
