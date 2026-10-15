//go:build !linux

package avatarcache

import (
	"os"
	"time"
)

// accessTime falls back to mtime where atime is not portable; eviction then
// approximates LRU by last refresh.
func accessTime(info os.FileInfo) time.Time { return info.ModTime() }
