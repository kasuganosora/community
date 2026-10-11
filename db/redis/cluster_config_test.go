package redis

import (
	"os"
	"reflect"
	"testing"

	"github.com/kasuganosora/community/config"
)

func TestFeedClusterAddressesConfigAndEnvironment(t *testing.T) {
	const key = "REDIS_FEED_CLUSTER_ADDRS"
	prior, exists := os.LookupEnv(key)
	old := config.C.Other
	t.Cleanup(func() {
		config.C.Other = old
		if exists {
			_ = os.Setenv(key, prior)
		} else {
			_ = os.Unsetenv(key)
		}
	})
	config.C.Other = map[string]string{key: " localhost:17100, , localhost:17101 ,"}
	_ = os.Unsetenv(key)
	if got := feedClusterAddresses(); !reflect.DeepEqual(got, []string{"localhost:17100", "localhost:17101"}) {
		t.Fatalf("parsed .env addresses: %v", got)
	}
	_ = os.Setenv(key, " localhost:17200, , localhost:17201 ")
	if got := feedClusterAddresses(); !reflect.DeepEqual(got, []string{"localhost:17200", "localhost:17201"}) {
		t.Fatalf("environment override: %v", got)
	}
	_ = os.Setenv(key, " , ")
	if got := feedClusterAddresses(); len(got) != 0 {
		t.Fatalf("explicit empty override: %v", got)
	}
	_ = os.Unsetenv(key)
	config.C.Other = nil
	if got := feedClusterAddresses(); len(got) != 0 {
		t.Fatalf("unconfigured: %v", got)
	}
}
