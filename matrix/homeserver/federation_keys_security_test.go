package homeserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gmsl "github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/stretchr/testify/require"
)

func TestJoinTargetCannotVouchForAnotherServersKeys(t *testing.T) {
	attacker, origin := newFakeFed(t), newFakeFed(t)
	hs := joinTestEnv(t, attacker)
	t.Setenv("MATRIX_TRUSTED_KEY_SERVERS", "")
	origin.kill()
	// The room server can fabricate a self-signed document naming the origin,
	// using its own replacement keypair. Its extra notary signature grants no trust.
	forged := &fakeFed{hs: minimalHS(origin.name)}
	attacker.notaryDocs[origin.name] = forged
	ctx := withKeyNotaries(context.Background(), attacker.name)
	verifier := federationJSONVerifier{hs}
	_, err := verifier.serverKeyContext(ctx, origin.name, forged.hs.keyID, spec.AsTimestamp(time.Now()), gmsl.StrictValiditySignatureCheck)
	require.Error(t, err)
	require.Zero(t, attacker.count("query"), "client-selected join host is not a trusted notary")
	_, found := GetKeyCache().GetFor(origin.name, forged.hs.keyID, spec.AsTimestamp(time.Now()), gmsl.StrictValiditySignatureCheck)
	require.False(t, found, "forged keys must not poison the global cache")
	_, err = verifier.fetchNotaryKeyDocument(ctx, attacker.name, origin.name, forged.hs.keyID, spec.AsTimestamp(time.Now()))
	require.ErrorIs(t, err, errBadKeyDocument)
}

func TestKeyNotaryHintsOnlyReorderAdministratorTrust(t *testing.T) {
	hs := minimalHS("local.example")
	t.Setenv("MATRIX_TRUSTED_KEY_SERVERS", "trusted-one.example, trusted-two.example")
	ctx := withKeyNotaries(context.Background(), "attacker.example", "trusted-two.example")
	require.Equal(t, []string{"trusted-two.example", "trusted-one.example"}, keyNotaries(ctx, hs, "origin.example"))
}

func TestKeyCacheRejectsLegacyUnattributedKeysAfterRestart(t *testing.T) {
	database := setupKeyCacheTestDB(t)
	cache := NewKeyCache()
	public, private := ed25519Key()
	require.NotEmpty(t, private)
	require.NoError(t, database.Create(&MatrixServerKey{ServerName: "legacy-origin.example", KeyID: "ed25519:old", PublicKey: base64.RawStdEncoding.EncodeToString(public), ValidUntil: time.Now().Add(time.Hour)}).Error)
	_, found := cache.GetFor("legacy-origin.example", "ed25519:old", spec.AsTimestamp(time.Now()), gmsl.NoStrictValidityCheck)
	require.False(t, found, "even v1-4 permissive expiry policy must not trust old unverified provenance")
	cache.Set("legacy-origin.example", "ed25519:old", public, time.Now().Add(time.Hour))
	require.NoError(t, cache.WaitSaved(context.Background())) // persistence is asynchronous
	reloaded := NewKeyCache()
	key, found := reloaded.GetFor("legacy-origin.example", "ed25519:old", spec.AsTimestamp(time.Now()), gmsl.StrictValiditySignatureCheck)
	require.True(t, found)
	require.Equal(t, public, key)
}

func TestKeyFetchLockHonoursCancellation(t *testing.T) {
	unlock, err := lockServerKeys(context.Background(), "locked.example")
	require.NoError(t, err)
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = lockServerKeys(ctx, "locked.example")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	unlock()
	again, err := lockServerKeys(context.Background(), "locked.example")
	require.NoError(t, err)
	again()
}

func TestRetiredKeyExpiryRemainsStrictForEveryRoomVersion(t *testing.T) {
	setupKeyCacheTestDB(t)
	key, _ := ed25519Key()
	expiry := time.Now().Add(-time.Hour).UnixMilli()
	cacheKeyDocument("retired-origin.example", &ServerKeyResponse{OldVerifyKeys: map[string]KeyInfo{"ed25519:retired": {Key: base64.RawStdEncoding.EncodeToString(key), ExpiredTS: expiry}}})
	require.NoError(t, GetKeyCache().WaitSaved(context.Background())) // persistence is asynchronous
	GetKeyCache().Cleanup() // retired keys must remain reloadable for historical verification
	for _, check := range []gmsl.SignatureValidityCheckFunc{gmsl.StrictValiditySignatureCheck, gmsl.NoStrictValidityCheck} {
		for _, offset := range []int64{-1, 0, 1} {
			_, found := GetKeyCache().GetFor("retired-origin.example", "ed25519:retired", spec.Timestamp(expiry+offset), check)
			require.Equal(t, offset < 0, found)
			_, found = NewKeyCache().GetFor("retired-origin.example", "ed25519:retired", spec.Timestamp(expiry+offset), check)
			require.Equal(t, offset < 0, found, "expiry identity must survive persistence")
		}
	}
}

func TestCachedKeyOnlyVerificationNeverFetchesOrPoisonsFailures(t *testing.T) {
	remote := newFakeFed(t)
	hs := joinTestEnv(t, remote)
	ctx := withCachedKeysOnly(context.Background())
	_, err := (federationJSONVerifier{hs}).serverKeyContext(ctx, remote.name, remote.hs.keyID, spec.AsTimestamp(time.Now()), gmsl.StrictValiditySignatureCheck)
	require.ErrorIs(t, err, errKeyUnavailable)
	require.Zero(t, remote.count("key"))
	_, failed := keyFetchFailures.active(remote.name)
	require.False(t, failed)
	GetKeyCache().Set(remote.name, remote.hs.keyID, remote.hs.publicKey, time.Now().Add(time.Hour))
	key, err := (federationJSONVerifier{hs}).serverKeyContext(ctx, remote.name, remote.hs.keyID, spec.AsTimestamp(time.Now()), gmsl.StrictValiditySignatureCheck)
	require.NoError(t, err)
	require.Equal(t, remote.hs.publicKey, key)
}

func TestNotaryChoosesDocumentContainingRequestedHistoricalKey(t *testing.T) {
	notary, origin := newFakeFed(t), newFakeFed(t)
	hs := joinTestEnv(t, notary)
	t.Setenv("MATRIX_TRUSTED_KEY_SERVERS", notary.name)
	older := NewFederationService(origin.hs).GetServerKey()
	newer := NewFederationService(origin.hs).GetServerKey()
	newer["valid_until_ts"] = time.Now().Add(48 * time.Hour).UnixMilli()
	newer["verify_keys"] = map[string]any{"ed25519:new": map[string]any{"key": base64.RawStdEncoding.EncodeToString(origin.hs.publicKey)}}
	NewFederationService(notary.hs).signEvent(older)
	NewFederationService(notary.hs).signEvent(newer)
	originalHandler := notary.srv.Config.Handler
	notary.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/_matrix/key/v2/query/") {
			_ = json.NewEncoder(w).Encode(map[string]any{"server_keys": []any{newer, older}})
			return
		}
		originalHandler.ServeHTTP(w, r)
	})
	doc, err := (federationJSONVerifier{hs}).fetchNotaryKeyDocument(context.Background(), notary.name, origin.name, origin.hs.keyID, spec.AsTimestamp(time.Now()))
	require.NoError(t, err)
	require.Contains(t, doc.VerifyKeys, origin.hs.keyID, "newest unrelated key document must not hide usable history")
}

func TestKeyCacheReadsDoNotWaitForBusyPersistence(t *testing.T) {
	setupKeyCacheTestDB(t)
	cache := NewKeyCache()
	held, err := cache.lockDB(context.Background())
	require.NoError(t, err)
	defer held()
	started := time.Now()
	_, found := cache.GetForContext(context.Background(), "busy.example", "ed25519:key", spec.AsTimestamp(time.Now()), gmsl.StrictValiditySignatureCheck)
	require.False(t, found)
	require.Less(t, time.Since(started), time.Second, "a key lookup must not queue behind batched database writes")
}

func TestKeyFetchLocksReleaseHostEntriesAndSerializeWaiters(t *testing.T) {
	for i := range 100 {
		host := fmt.Sprintf("lock-churn-%d.example", i)
		unlock, err := lockServerKeys(context.Background(), host)
		require.NoError(t, err)
		unlock()
		unlock() // deferred and explicit release remain idempotent
		serverKeyLocks.Lock()
		_, retained := serverKeyLocks.entries[host]
		serverKeyLocks.Unlock()
		require.False(t, retained)
	}
	const host = "contended-lock.example"
	var active atomic.Int64
	var violation atomic.Bool
	var wait sync.WaitGroup
	for range 8 {
		wait.Go(func() {
			for range 20 {
				unlock, err := lockServerKeys(context.Background(), host)
				if err != nil {
					violation.Store(true)
					return
				}
				if active.Add(1) != 1 {
					violation.Store(true)
				}
				runtime.Gosched()
				active.Add(-1)
				unlock()
			}
		})
	}
	wait.Wait()
	require.False(t, violation.Load())
	serverKeyLocks.Lock()
	_, retained := serverKeyLocks.entries[host]
	serverKeyLocks.Unlock()
	require.False(t, retained, "last waiter must release the per-host lock entry")
}
