//go:build windows

package datatransfer

func syscallUmask(m int) int { return m }
