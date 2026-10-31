package main

import (
	"context"
	"os"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/mediagc"
)

// startUnattachedMediaPurge expires media whose status_id is NULL and whose
// updated_at is older than the retention window. MEDIA_UNATTACHED_PURGE=off
// disables it. MEDIA_UNATTACHED_TTL overrides the default of 168h (minimum 1h).
func startUnattachedMediaPurge(ctx context.Context) {
	if !mediagc.Enabled(os.Getenv("MEDIA_UNATTACHED_PURGE")) {
		log.Info("Unattached media purge disabled")

		return
	}

	age, err := mediagc.ParseMinAge(os.Getenv("MEDIA_UNATTACHED_TTL"))
	if err != nil {
		log.Warn("MEDIA_UNATTACHED_TTL %q is invalid (%v); using %s", os.Getenv("MEDIA_UNATTACHED_TTL"), err, mediagc.DefaultMinAge)
		age = mediagc.DefaultMinAge
	}

	log.Info("Unattached media purge enabled (retention %s)", age)
	mediagc.Start(ctx, db.GetDB(), mediagc.Options{MinAge: age, Limit: mediagc.DefaultSweepLimit})
}
