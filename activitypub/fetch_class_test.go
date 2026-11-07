package activitypub

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/moderation"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
)

func TestClassifyRemoteFetchDoesNotCallAuthorizationABan(t *testing.T) {
	require.Equal(t, "", ClassifyRemoteFetch(nil))
	require.Equal(t, RemoteFetchUnauthorized, ClassifyRemoteFetch(fmt.Errorf("fetching actor: %w", &FetchError{StatusCode: http.StatusUnauthorized})))
	require.Equal(t, RemoteFetchForbidden, ClassifyRemoteFetch(&FetchError{StatusCode: http.StatusForbidden}))
	require.Equal(t, RemoteFetchNotFound, ClassifyRemoteFetch(&FetchError{StatusCode: http.StatusNotFound}))
	require.Equal(t, RemoteFetchNotFound, ClassifyRemoteFetch(&FetchError{StatusCode: http.StatusGone}))
	require.Equal(t, RemoteFetchTimeout, ClassifyRemoteFetch(context.DeadlineExceeded))
	require.Equal(t, RemoteFetchTimeout, ClassifyRemoteFetch(&net.DNSError{IsTimeout: true}))
	require.Equal(t, RemoteFetchFailed, ClassifyRemoteFetch(errors.New("connection reset by peer")))
	require.Equal(t, RemoteFetchFailed, ClassifyRemoteFetch(&FetchError{StatusCode: http.StatusBadGateway}))
	require.Equal(t, RemoteFetchDomainSuspended, ClassifyRemoteFetch(fmt.Errorf("delivery: %w", ErrDeliveryBlocked)))

	for _, class := range []string{RemoteFetchUnauthorized, RemoteFetchForbidden, RemoteFetchNotFound, RemoteFetchTimeout, RemoteFetchFailed} {
		require.NotEqual(t, RemoteFetchDomainSuspended, class)
		require.NotEqual(t, models.ProfileAvailabilityAccountSuspended, class)
	}
}

func TestResolveSearchAccountDoesNotFetchSuspendedNodes(t *testing.T) {
	testDB := setupWebfingerDB(t)
	require.NoError(t, testDB.GormDB.AutoMigrate(&models.DomainBlock{}))
	require.NoError(t, testDB.GormDB.Create(&models.DomainBlock{Domain: "suspended.example", Severity: "suspend", PrivateComment: "synthetic-private-remark"}).Error)
	require.NoError(t, testDB.GormDB.Create(&models.DomainBlock{Domain: "quiet.example", Severity: "silence"}).Error)
	require.NoError(t, testDB.GormDB.Create(&models.DomainBlock{Domain: "hidden.example", Severity: "suspend", Obfuscate: true}).Error)
	systemsettings.Set(moderation.KeyBlockedHosts, []string{"https://Legacy.Example/path", "*.wild.test"})
	moderation.Invalidate()
	t.Cleanup(func() {
		systemsettings.Set(moderation.KeyBlockedHosts, []string{})
		moderation.Invalidate()
	})

	calls := 0
	previous := sharedHTTPClient
	sharedHTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("unexpected remote profile fetch")
	}), Timeout: time.Second}
	t.Cleanup(func() { sharedHTTPClient = previous })

	for _, acct := range []string{
		"@someone@suspended.example",
		"@someone@child.suspended.example",
		"@someone@legacy.example",
		"@someone@A.Legacy.Example",
		"@someone@a.b.wild.test",
		"@someone@hidden.example",
		"https://suspended.example/users/someone",
	} {
		calls = 0
		_, err := ResolveSearchAccount(context.Background(), acct)
		require.ErrorIs(t, err, ErrDeliveryBlocked, acct)
		require.Equal(t, 0, calls, acct)
	}

	calls = 0
	_, err := ResolveSearchAccount(context.Background(), "@someone@quiet.example")
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrDeliveryBlocked)
	require.Equal(t, 1, calls, "silence is not a node suspension")

	calls = 0
	_, err = ResolveSearchAccount(context.Background(), "@someone@notlegacy.example")
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrDeliveryBlocked)
	require.Equal(t, 1, calls, "a suffix that is not a parent domain is not suspended")
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
