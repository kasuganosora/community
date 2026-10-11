package homeserver

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/kasuganosora/community/log"
	gmsl "github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/spec"
)

// Signing key policy
//
//  1. Cache first (memory, then DB). A cached key is used when the room
//     version's validity rule accepts it for the signature's timestamp
//     (gmsl SignatureValidityCheck: v1-v4 accept any known key, v5+ require the
//     key to have been valid at origin_server_ts, capped at 7 days ahead).
//  2. Otherwise fetch /_matrix/key/v2/server directly. Resolution goes through
//     the shared federation discovery transport (.well-known with caching, SRV,
//     8448 fallback), the same path every other federation request uses.
//  3. If the origin is unreachable, or its document lacks a usable key, ask
//     notaries via /_matrix/key/v2/query/{server}/{key_id}: first the server the
//     room is being joined through (it is in the room and already vouches for
//     its state), then matrix.org. A notary response is accepted only if it is
//     signed by the notary's own key (fetched directly from the notary) and
//     names the right server_name.
//  4. All keys of an accepted document are cached with their validity; keys in
//     old_verify_keys are cached with expired_ts as their validity.
//  5. Failure to obtain keys is remembered per server for keyFailureTTL so a
//     response citing many events from a dead server costs one failure.
var (
	// keyFetchTimeout bounds one direct fetch or one notary query.
	keyFetchTimeout = 8 * time.Second
	// keyFailureTTL is how long an unreachable server is not re-queried.
	keyFailureTTL = 3 * time.Minute
	// defaultKeyNotaries are always consulted last.
	defaultKeyNotaries = []string{"matrix.org"}
)

const keyDocumentLimit int64 = 4 << 20

// errKeyUnavailable marks "could not obtain a signing key" (as opposed to a
// signature that was checked and found wrong).
var errKeyUnavailable = errors.New("signing key unavailable")

type keyNotaryCtxKey struct{}

// withKeyNotaries makes key lookups under ctx consult these servers (in order)
// before the defaults. The join flow passes the server it is joining through.
func withKeyNotaries(ctx context.Context, servers ...string) context.Context {
	return context.WithValue(ctx, keyNotaryCtxKey{}, servers)
}

func keyNotaries(ctx context.Context, hs *HomeServer, target string) []string {
	var list []string
	seen := map[string]bool{target: true, hs.Config.ServerName: true}
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			list = append(list, s)
		}
	}
	if v, ok := ctx.Value(keyNotaryCtxKey{}).([]string); ok {
		for _, s := range v {
			add(s)
		}
	}
	for _, s := range defaultKeyNotaries {
		add(s)
	}
	return list
}

var serverKeyLocks sync.Map // server -> *sync.Mutex

func lockServerKeys(server string) func() {
	m, _ := serverKeyLocks.LoadOrStore(server, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// serverKey is the strict "valid right now" lookup used for request signatures.
func (v federationJSONVerifier) serverKey(server, keyID string, at time.Time) (ed25519.PublicKey, error) {
	return v.serverKeyContext(context.Background(), server, keyID, spec.AsTimestamp(at), gmsl.StrictValiditySignatureCheck)
}

func (v federationJSONVerifier) serverKeyContext(ctx context.Context, server, keyID string, at spec.Timestamp, check gmsl.SignatureValidityCheckFunc) (ed25519.PublicKey, error) {
	return v.lookupKey(ctx, server, keyID, at, check, true)
}

func (v federationJSONVerifier) lookupKey(ctx context.Context, server, keyID string, at spec.Timestamp, check gmsl.SignatureValidityCheckFunc, useNotaries bool) (ed25519.PublicKey, error) {
	if server == v.hs.Config.ServerName {
		if keyID != v.hs.keyID {
			return nil, errors.New("unknown local signing key")
		}
		return v.hs.publicKey, nil
	}
	cache := GetKeyCache()
	if key, ok := cache.GetFor(server, keyID, at, check); ok {
		return key, nil
	}
	if reason, dead := keyFetchFailures.active(server); dead {
		return nil, fmt.Errorf("%w for %s (recent failure: %s)", errKeyUnavailable, server, reason)
	}
	if reason, missing := keyFetchFailures.active(server + "|" + keyID); missing {
		return nil, fmt.Errorf("%w for %s:%s (recent failure: %s)", errKeyUnavailable, server, keyID, reason)
	}
	unlock := lockServerKeys(server)
	defer unlock()
	// Another goroutine may have fetched while we waited for the lock.
	if key, ok := cache.GetFor(server, keyID, at, check); ok {
		return key, nil
	}

	var errs []string
	unreachable := true
	try := func(label string, doc *ServerKeyResponse, err error) (ed25519.PublicKey, bool) {
		if err != nil {
			errs = append(errs, label+": "+err.Error())
			var httpErr *federationHTTPError
			if label == "direct" && (errors.As(err, &httpErr) || errors.Is(err, errBadKeyDocument)) {
				unreachable = false // the origin answered; the answer was just unusable
			}
			return nil, false
		}
		if label == "direct" {
			unreachable = false
		}
		cacheKeyDocument(server, doc)
		if key, ok := cache.GetFor(server, keyID, at, check); ok {
			return key, true
		}
		errs = append(errs, label+": requested signing key missing or expired")
		return nil, false
	}

	doc, err := v.fetchDirectKeyDocument(ctx, server)
	if key, ok := try("direct", doc, err); ok {
		return key, nil
	}
	if useNotaries && ctx.Err() == nil {
		for _, notary := range keyNotaries(ctx, v.hs, server) {
			doc, err = v.fetchNotaryKeyDocument(ctx, notary, server, keyID, at)
			if key, ok := try("notary "+notary, doc, err); ok {
				log.Debug("signing key %s:%s obtained from notary %s", server, keyID, notary)
				return key, nil
			}
			if ctx.Err() != nil {
				break
			}
		}
	}
	if ctx.Err() == nil {
		if unreachable {
			keyFetchFailures.mark(server, keyFailureTTL, strings.Join(errs, "; "))
		} else {
			keyFetchFailures.mark(server+"|"+keyID, keyFailureTTL, strings.Join(errs, "; "))
		}
	}
	return nil, fmt.Errorf("%w for %s:%s: %s", errKeyUnavailable, server, keyID, strings.Join(errs, "; "))
}

var errBadKeyDocument = errors.New("invalid key document")

func (v federationJSONVerifier) fetchDirectKeyDocument(ctx context.Context, server string) (*ServerKeyResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, keyFetchTimeout)
	defer cancel()
	client := newFederationClient(v.hs)
	doc, err := client.FetchServerKeyContext(ctx, server)
	if err != nil {
		return nil, err
	}
	if err = verifyKeyDocument(doc, server); err != nil {
		return nil, err
	}
	// A server signs its own document with one of the keys it lists.
	selfSigned := false
	for id, info := range doc.VerifyKeys {
		key, e := base64.RawStdEncoding.DecodeString(info.Key)
		if e != nil || len(key) != ed25519.PublicKeySize {
			continue
		}
		if gmsl.VerifyJSON(server, gmsl.KeyID(id), ed25519.PublicKey(key), doc.RawJSON) == nil {
			selfSigned = true
			break
		}
	}
	if !selfSigned {
		return nil, fmt.Errorf("%w: invalid key document self-signature", errBadKeyDocument)
	}
	return doc, nil
}

func verifyKeyDocument(doc *ServerKeyResponse, server string) error {
	if doc.ServerName != server {
		return fmt.Errorf("%w: key document server name mismatch", errBadKeyDocument)
	}
	if doc.ValidUntil <= 0 {
		return fmt.Errorf("%w: missing valid_until_ts", errBadKeyDocument)
	}
	if len(doc.RawJSON) == 0 {
		return fmt.Errorf("%w: original JSON missing", errBadKeyDocument)
	}
	return nil
}

// fetchNotaryKeyDocument asks `notary` for server's keys. The notary's
// signature is verified against the notary's own directly fetched key.
func (v federationJSONVerifier) fetchNotaryKeyDocument(ctx context.Context, notary, server, keyID string, at spec.Timestamp) (*ServerKeyResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, keyFetchTimeout)
	defer cancel()
	client := newFederationClient(v.hs)
	q := ""
	if at > 0 {
		q = fmt.Sprintf("?minimum_valid_until_ts=%d", int64(at))
	}
	queryURL := fmt.Sprintf("%s/_matrix/key/v2/query/%s/%s%s", client.federationBaseURL(notary), url.PathEscape(server), url.PathEscape(keyID), q)
	resp, raw, err := client.doFederationRequestContext(ctx, "GET", queryURL, notary, nil, keyDocumentLimit) //nolint:bodyclose // closed in doFederationRequest
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, &federationHTTPError{Status: resp.StatusCode, Message: string(raw)}
	}
	var envelope struct {
		ServerKeys []json.RawMessage `json:"server_keys"`
	}
	if err = json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("%w: %v", errBadKeyDocument, err)
	}
	var best *ServerKeyResponse
	for _, docRaw := range envelope.ServerKeys {
		var doc ServerKeyResponse
		if json.Unmarshal(docRaw, &doc) != nil || doc.ServerName != server || doc.ValidUntil <= 0 {
			continue
		}
		doc.RawJSON = append([]byte(nil), docRaw...)
		trusted := false
		for notaryKeyID := range doc.Signatures[notary] {
			key, e := v.lookupKey(ctx, notary, notaryKeyID, spec.AsTimestamp(time.Now()), gmsl.StrictValiditySignatureCheck, false)
			if e != nil {
				continue
			}
			if gmsl.VerifyJSON(notary, gmsl.KeyID(notaryKeyID), key, doc.RawJSON) == nil {
				trusted = true
				break
			}
		}
		if !trusted {
			continue
		}
		if best == nil || doc.ValidUntil > best.ValidUntil {
			d := doc
			best = &d
		}
	}
	if best == nil {
		return nil, fmt.Errorf("%w: no notary-signed document for %s", errBadKeyDocument, server)
	}
	return best, nil
}

// cacheKeyDocument stores every key of a verified document.
func cacheKeyDocument(server string, doc *ServerKeyResponse) {
	cache := GetKeyCache()
	for id, info := range doc.VerifyKeys {
		if key, err := base64.RawStdEncoding.DecodeString(info.Key); err == nil && len(key) == ed25519.PublicKeySize {
			cache.Set(server, id, key, time.UnixMilli(doc.ValidUntil))
		}
	}
	for id, info := range doc.OldVerifyKeys {
		if info.ExpiredTS <= 0 {
			continue
		}
		if key, err := base64.RawStdEncoding.DecodeString(info.Key); err == nil && len(key) == ed25519.PublicKeySize {
			// An old key is only good for events before it expired.
			cache.Set(server, id, key, time.UnixMilli(info.ExpiredTS))
		}
	}
}

// VerifyJSONs verifies signatures with the room version's own validity rule
// (req.ValidityCheckingFunc). A server whose keys cannot be obtained yields
// an error wrapping errKeyUnavailable so callers can tell "unverifiable" from
// "verified and wrong".
func (v federationJSONVerifier) VerifyJSONs(ctx context.Context, requests []gmsl.VerifyJSONRequest) ([]gmsl.VerifyJSONResult, error) {
	out := make([]gmsl.VerifyJSONResult, len(requests))
	for i, req := range requests {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var envelope struct {
			Signatures map[string]map[string]string `json:"signatures"`
		}
		if err := json.Unmarshal(req.Message, &envelope); err != nil {
			out[i].Error = err
			continue
		}
		out[i].Error = errors.New("no valid signature from required server")
		for keyID := range envelope.Signatures[string(req.ServerName)] {
			key, err := v.serverKeyContext(ctx, string(req.ServerName), keyID, req.AtTS, req.ValidityCheckingFunc)
			if err != nil {
				out[i].Error = err
				continue
			}
			if err = gmsl.VerifyJSON(string(req.ServerName), gmsl.KeyID(keyID), key, req.Message); err == nil {
				out[i].Error = nil
				break
			} else {
				out[i].Error = err
			}
		}
	}
	return out, nil
}
