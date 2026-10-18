//go:build !windows

package datatransfer

import "syscall"

func syscallUmask(m int) int { return syscall.Umask(m) }
