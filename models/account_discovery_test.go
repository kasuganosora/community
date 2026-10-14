package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAccountAllowsRecommendationRequiresFreshRemoteConsent(t *testing.T) {
	now := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	yes, no := true, false
	domain := "remote.test"
	old, future := now.Add(-24*time.Hour-time.Second), now.Add(time.Second)
	cases := []struct {
		name    string
		account *Account
		want    bool
	}{
		{"nil", nil, false},
		{"local unknown", &Account{}, false},
		{"local opt-in", &Account{Discoverable: true}, true},
		{"local noindex", &Account{Discoverable: true, NoIndex: true}, false},
		{"remote legacy default", &Account{Domain: &domain, Discoverable: true}, false},
		{"remote unknown discoverable", &Account{Domain: &domain, RemoteIndexable: &yes, RemoteDiscoveryCheckedAt: &now}, false},
		{"remote opt-in indexable omitted", &Account{Domain: &domain, RemoteDiscoverable: &yes, RemoteDiscoveryCheckedAt: &now}, true},
		{"remote legacy noindex", &Account{Domain: &domain, NoIndex: true, RemoteDiscoverable: &yes, RemoteDiscoveryCheckedAt: &now}, false},
		{"remote refuses index", &Account{Domain: &domain, RemoteDiscoverable: &yes, RemoteIndexable: &no, RemoteDiscoveryCheckedAt: &now}, false},
		{"remote refuses discovery", &Account{Domain: &domain, RemoteDiscoverable: &no, RemoteIndexable: &yes, RemoteDiscoveryCheckedAt: &now}, false},
		{"remote stale", &Account{Domain: &domain, RemoteDiscoverable: &yes, RemoteDiscoveryCheckedAt: &old}, false},
		{"remote future", &Account{Domain: &domain, RemoteDiscoverable: &yes, RemoteDiscoveryCheckedAt: &future}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { require.Equal(t, tc.want, tc.account.AllowsRecommendation(now)) })
	}
}
