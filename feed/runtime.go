package feed

import (
	"fmt"
	"sync/atomic"
)

var deliveryMode atomic.Int32
var databaseReads atomic.Bool

// ConfigureRuntime is called before serving traffic. Shadow/legacy reads use
// authoritative SQL while the v3 projection is evaluated or rolled back.
func ConfigureRuntime(mode string) error {
	switch mode {
	case "durable":
		deliveryMode.Store(1)
		databaseReads.Store(false)
	case "shadow":
		deliveryMode.Store(2)
		databaseReads.Store(true)
	case "legacy":
		deliveryMode.Store(0)
		databaseReads.Store(true)
	default:
		return fmt.Errorf("FEED_MODE must be durable, shadow or legacy")
	}
	return nil
}

func DurableFanoutEnabled() bool { return deliveryMode.Load() == 1 }
func ForceDatabaseReads() bool   { return databaseReads.Load() }
