package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAccountAllowsRecommendationUsesFreshCheckOrStoredActorFlag(t *testing.T) {
	now := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	yes, no := true, false
	domain := "remote.test"
	old, future := now.Add(-24*time.Hour-time.Second), now.Add(time.Second)
	ancient := now.Add(-31 * 24 * time.Hour)
	cases := []struct {
		name    string
		account *Account
		want    bool
	}{
		{"nil", nil, false},
		{"local unknown", &Account{}, false},
		{"local opt-in", &Account{Discoverable: true}, true},
		{"local noindex", &Account{Discoverable: true, NoIndex: true}, false},
		{"remote unchecked stored actor flag", &Account{Domain: &domain, Discoverable: true}, true},
		{"remote unchecked unknown", &Account{Domain: &domain}, false},
		{"remote unchecked stored nullable flag", &Account{Domain: &domain, RemoteDiscoverable: &yes}, true},
		{"remote unchecked stored refusal", &Account{Domain: &domain, RemoteDiscoverable: &no, Discoverable: true}, false},
		{"remote unchecked noindex", &Account{Domain: &domain, Discoverable: true, NoIndex: true}, false},
		{"remote invalid document", &Account{Domain: &domain, Discoverable: true, RemoteDiscoverySource: "actor"}, false},
		{"remote fresh check overrides legacy flag", &Account{Domain: &domain, Discoverable: true, RemoteDiscoverable: &no, RemoteDiscoveryCheckedAt: &now}, false},
		{"remote very stale", &Account{Domain: &domain, RemoteDiscoverable: &yes, RemoteDiscoveryCheckedAt: &ancient}, false},
		{"remote unknown discoverable", &Account{Domain: &domain, RemoteIndexable: &yes, RemoteDiscoveryCheckedAt: &now}, false},
		{"remote opt-in indexable omitted", &Account{Domain: &domain, RemoteDiscoverable: &yes, RemoteDiscoveryCheckedAt: &now}, true},
		{"remote legacy noindex", &Account{Domain: &domain, NoIndex: true, RemoteDiscoverable: &yes, RemoteDiscoveryCheckedAt: &now}, false},
		{"remote refuses index", &Account{Domain: &domain, RemoteDiscoverable: &yes, RemoteIndexable: &no, RemoteDiscoveryCheckedAt: &now}, false},
		{"remote refuses discovery", &Account{Domain: &domain, RemoteDiscoverable: &no, RemoteIndexable: &yes, RemoteDiscoveryCheckedAt: &now}, false},
		{"remote stale falls back to stored opt-in", &Account{Domain: &domain, RemoteDiscoverable: &yes, RemoteDiscoveryCheckedAt: &old}, true},
		{"remote stale refusal", &Account{Domain: &domain, RemoteDiscoverable: &no, RemoteDiscoveryCheckedAt: &old}, false},
		{"remote future", &Account{Domain: &domain, RemoteDiscoverable: &yes, RemoteDiscoveryCheckedAt: &future}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { require.Equal(t, tc.want, tc.account.AllowsRecommendation(now)) })
	}
}
