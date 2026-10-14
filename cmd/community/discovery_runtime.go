package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"time"

	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/discovery"
	"github.com/kasuganosora/community/systemsettings"
)

func startDiscoveryRuntime(ctx context.Context) {
	// Derive a purpose-specific key from the mandatory deployment secret. All
	// replicas share it; restarting one process never invalidates every cursor.
	mac := hmac.New(sha256.New, []byte(config.C.EncryptedKey))
	_, _ = mac.Write([]byte("community/discovery/cursor/v1"))
	readConfig := func() systemsettings.DiscoveryConfiguration {
		readCtx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		settings, err := systemsettings.ReadDiscoveryConfiguration(readCtx, db.GetDB())
		if err != nil {
			return systemsettings.DiscoveryConfiguration{Mode: "rules"}
		}
		return settings
	}
	discovery.Configure(discovery.Config{
		Policy: func(readCtx context.Context) (string, error) {
			return systemsettings.DiscoveryPolicyVersion(readCtx, db.GetDB())
		},
		Enabled: func() bool { return readConfig().Enabled },
		CheckEnabled: func(readCtx context.Context) (bool, error) {
			settings, err := systemsettings.ReadDiscoveryConfiguration(readCtx, db.GetDB())
			return settings.Enabled, err
		},
		TrendsEnabled:      func() bool { return readConfig().TrendsEnabled },
		SigningKeys:        [][]byte{mac.Sum(nil)},
		MaxActiveSnapshots: 512,
		MaxCacheBytes:      64 << 20,
		RefreshRemotePreferences: func(refreshCtx context.Context) error {
			_, err := activitypub.RefreshRemoteDiscoveryPreferences(refreshCtx, db.GetDB(), activitypub.NewFetchClient(), 8)
			return err
		},
	})
	provider := discovery.NewMeiliProvider()
	discovery.SetContentProvider(provider)
	provider.Start(ctx)
	discovery.StartBackground(ctx)
}
