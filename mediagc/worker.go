package mediagc

import (
	"context"
	"time"

	"github.com/kasuganosora/community/log"
	"gorm.io/gorm"
)

// Start purges expired detached media once, then once an hour, until ctx
// is cancelled. Storage failures are logged and left for the next pass.
func Start(ctx context.Context, conn *gorm.DB, opts Options) {
	if opts.MinAge <= 0 {
		opts.MinAge = DefaultMinAge
	}

	if opts.Limit <= 0 {
		opts.Limit = DefaultSweepLimit
	}

	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		run := func() {
			sweepCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()
			stats, err := Purge(sweepCtx, conn, opts)
			if err != nil || stats.Failed > 0 {
				log.Warn("unattached media purge: removed %d, failed %d: %v", stats.Removed, stats.Failed, err)
				return
			}
			if stats.Removed > 0 || stats.SharedKept > 0 {
				log.Info("unattached media purge: removed %d attachment(s), kept objects for %d shared attachment(s), left %d profile attachment(s)", stats.Removed, stats.SharedKept, stats.ProfileKept)
			}
		}
		run()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()
}
