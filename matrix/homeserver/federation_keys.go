package homeserver

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
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
//     administrator-trusted notaries via /_matrix/key/v2/query/{server}/{key_id}.
//     MATRIX_TRUSTED_KEY_SERVERS defaults to matrix.org; an empty value disables
//     fallback. A join target is not automatically trusted. A notary response is accepted only if it is
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

type cachedKeysOnlyCtxKey struct{}

func withCachedKeysOnly(ctx context.Context) context.Context {
	return context.WithValue(ctx, cachedKeysOnlyCtxKey{}, true)
}

type keyNotaryCtxKey struct{}

// withKeyNotaries makes key lookups under ctx consult these servers (in order)
// before other trusted servers. These hints never grant notary trust.
func withKeyNotaries(ctx context.Context, servers ...string) context.Context {
	return context.WithValue(ctx, keyNotaryCtxKey{}, servers)
}

// trustedKeyNotaries is an administrator-controlled trust boundary. Join targets
// and client-provided via hints may reorder this list, never extend it.
func trustedKeyNotaries() []string {
	if configured, ok := os.LookupEnv("MATRIX_TRUSTED_KEY_SERVERS"); ok {
		return strings.FieldsFunc(configured, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' })
	}
	return defaultKeyNotaries
}

func keyNotaries(ctx context.Context, hs *HomeServer, target string) []string {
	trusted := map[string]bool{}
	for _, server := range trustedKeyNotaries() {
		if _, _, valid := spec.ParseAndValidateServerName(spec.ServerName(server)); valid {
			trusted[server] = true
		}
	}
	var list []string
	seen := map[string]bool{target: true, hs.Config.ServerName: true}
	add := func(s string) {
		if trusted[s] && !seen[s] {
			seen[s] = true
			list = append(list, s)
		}
	}
	if hints, ok := ctx.Value(keyNotaryCtxKey{}).([]string); ok {
		for _, server := range hints {
			add(server)
		}
	}
	for _, server := range trustedKeyNotaries() {
		add(server)
	}
	return list
}

type serverKeyLock struct {
	gate chan struct{}
	refs int // holder and every waiting lookup
}

var serverKeyLocks = struct {
	sync.Mutex
	entries map[string]*serverKeyLock
}{entries: make(map[string]*serverKeyLock)}

func lockServerKeys(ctx context.Context, server string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	serverKeyLocks.Lock()
	entry := serverKeyLocks.entries[server]
	if entry == nil {
		entry = &serverKeyLock{gate: make(chan struct{}, 1)}
		serverKeyLocks.entries[server] = entry
	}
	entry.refs++
	serverKeyLocks.Unlock()
	releaseReference := func() {
		serverKeyLocks.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(serverKeyLocks.entries, server)
		}
		serverKeyLocks.Unlock()
	}
	select {
	case entry.gate <- struct{}{}:
		var once sync.Once
		unlock := func() { once.Do(func() { <-entry.gate; releaseReference() }) }
		if err := ctx.Err(); err != nil {
			unlock()
			return nil, err
		}
		return unlock, nil
	case <-ctx.Done():
		releaseReference()
		return nil, ctx.Err()
	}
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
	if key, ok := cache.GetForContext(ctx, server, keyID, at, check); ok {
		return key, nil
	}
	if only, _ := ctx.Value(cachedKeysOnlyCtxKey{}).(bool); only {
		return nil, fmt.Errorf("%w for %s:%s (cache-only verification)", errKeyUnavailable, server, keyID)
	}
	if reason, dead := keyFetchFailures.active(server); dead {
		return nil, fmt.Errorf("%w for %s (recent failure: %s)", errKeyUnavailable, server, reason)
	}
	if reason, missing := keyFetchFailures.active(server + "|" + keyID); missing {
		return nil, fmt.Errorf("%w for %s:%s (recent failure: %s)", errKeyUnavailable, server, keyID, reason)
	}
	unlock, lockErr := lockServerKeys(ctx, server)
	if lockErr != nil {
		return nil, lockErr
	}
	defer unlock()
	// Another goroutine may have fetched while we waited for the lock.
	if key, ok := cache.GetForContext(ctx, server, keyID, at, check); ok {
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
		cacheKeyDocumentContext(ctx, server, doc)
		if key, ok := cache.GetForContext(ctx, server, keyID, at, check); ok {
			return key, true
		}
		errs = append(errs, label+": requested signing key missing or expired")
		return nil, false
	}

	doc, err := v.fetchDirectKeyDocument(ctx, server)
	if key, ok := try("direct", doc, err); ok {
		return key, nil
	}
	// Never hold an origin lock while recursively fetching a notary's key.
	// Two concurrent requests may otherwise wait on one another indefinitely.
	unlock()
	if useNotaries && ctx.Err() == nil {
		for _, notary := range keyNotaries(ctx, v.hs, server) {
			doc, err = v.fetchNotaryKeyDocumentFor(ctx, notary, server, keyID, at, check)
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
	return doc, verifyDirectKeyDocument(doc, server)
}

func verifyDirectKeyDocument(doc *ServerKeyResponse, server string) error {
	if err := verifyKeyDocument(doc, server); err != nil {
		return err
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
		return fmt.Errorf("%w: invalid key document self-signature", errBadKeyDocument)
	}
	return nil
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
	return v.fetchNotaryKeyDocumentFor(ctx, notary, server, keyID, at, gmsl.StrictValiditySignatureCheck)
}

func (v federationJSONVerifier) fetchNotaryKeyDocumentFor(ctx context.Context, notary, server, keyID string, at spec.Timestamp, check gmsl.SignatureValidityCheckFunc) (*ServerKeyResponse, error) {
	allowed := false
	for _, trusted := range keyNotaries(context.Background(), v.hs, server) {
		if trusted == notary {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, fmt.Errorf("%w: untrusted key notary", errBadKeyDocument)
	}
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
	var bestValidity int64
	if check == nil {
		check = gmsl.StrictValiditySignatureCheck
	}
	for _, docRaw := range envelope.ServerKeys {
		doc, validity, usable := v.acceptNotaryDocument(ctx, notary, server, keyID, at, check, docRaw)
		if !usable {
			continue
		}
		if best == nil || validity > bestValidity {
			best = doc
			bestValidity = validity
		}
	}
	if best == nil {
		return nil, fmt.Errorf("%w: no notary-signed document for %s", errBadKeyDocument, server)
	}
	return best, nil
}

// acceptNotaryDocument parses one notary-supplied key document and accepts it
// only if it names the wanted server, carries the notary's own valid signature
// and contains the wanted key usable at `at`. Signature verification is the same
// for single and bulk queries.
func (v federationJSONVerifier) acceptNotaryDocument(ctx context.Context, notary, server, keyID string, at spec.Timestamp, check gmsl.SignatureValidityCheckFunc, docRaw json.RawMessage) (*ServerKeyResponse, int64, bool) {
	var doc ServerKeyResponse
	if json.Unmarshal(docRaw, &doc) != nil || doc.ServerName != server || doc.ValidUntil <= 0 {
		return nil, 0, false
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
		return nil, 0, false
	}
	if check == nil {
		check = gmsl.StrictValiditySignatureCheck
	}
	validity, usable := keyDocumentUsableAt(&doc, keyID, at, check)
	if !usable {
		return nil, 0, false
	}
	return &doc, validity, true
}

// bulkKeyWant names the key a bulk notary query needs for one server.
type bulkKeyWant struct {
	keyID string
	at    spec.Timestamp
	check gmsl.SignatureValidityCheckFunc
}

// fetchNotaryKeyDocumentsBulk asks one trusted notary for many servers' keys in
// a single POST /_matrix/key/v2/query. Each returned document is accepted by the
// same rules as a single query (notary signature, server name, usable key); the
// accepted documents are returned by server name. The notary must be an
// administrator-trusted one (keyNotaries).
func (v federationJSONVerifier) fetchNotaryKeyDocumentsBulk(ctx context.Context, notary string, wanted map[string]bulkKeyWant) (map[string]*ServerKeyResponse, error) {
	allowed := false
	for _, trusted := range keyNotaries(context.Background(), v.hs, "") {
		if trusted == notary {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, fmt.Errorf("%w: untrusted key notary", errBadKeyDocument)
	}
	ctx, cancel := context.WithTimeout(ctx, keyFetchTimeout*2)
	defer cancel()
	request := map[string]map[string]map[string]any{}
	for server, w := range wanted {
		criteria := map[string]any{}
		if w.at > 0 {
			criteria["minimum_valid_until_ts"] = int64(w.at)
		}
		request[server] = map[string]map[string]any{w.keyID: criteria}
	}
	client := newFederationClient(v.hs)
	resp, raw, err := client.doFederationRequestContext(ctx, "POST", client.federationBaseURL(notary)+"/_matrix/key/v2/query", notary, map[string]any{"server_keys": request}, keyDocumentLimit*8) //nolint:bodyclose // closed in doFederationRequest
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
	out := map[string]*ServerKeyResponse{}
	bestValidity := map[string]int64{}
	for _, docRaw := range envelope.ServerKeys {
		var head struct {
			ServerName string `json:"server_name"`
		}
		if json.Unmarshal(docRaw, &head) != nil {
			continue
		}
		w, ok := wanted[head.ServerName]
		if !ok {
			continue
		}
		doc, validity, usable := v.acceptNotaryDocument(ctx, notary, head.ServerName, w.keyID, w.at, w.check, docRaw)
		if usable && (out[head.ServerName] == nil || validity > bestValidity[head.ServerName]) {
			out[head.ServerName], bestValidity[head.ServerName] = doc, validity
		}
	}
	return out, nil
}

func keyDocumentUsableAt(doc *ServerKeyResponse, keyID string, at spec.Timestamp, check gmsl.SignatureValidityCheckFunc) (int64, bool) {
	info, exists := doc.VerifyKeys[keyID]
	validity := doc.ValidUntil
	if old, retired := doc.OldVerifyKeys[keyID]; retired {
		info, exists, validity = old, true, old.ExpiredTS
		if validity <= 0 || int64(at) >= validity {
			return 0, false
		}
	} else if !check(at, spec.Timestamp(validity)) {
		return 0, false
	}
	key, err := base64.RawStdEncoding.DecodeString(info.Key)
	return validity, exists && err == nil && len(key) == ed25519.PublicKeySize
}

// cacheKeyDocument stores every key of a verified document.
func cacheKeyDocument(server string, doc *ServerKeyResponse) {
	cacheKeyDocumentContext(context.Background(), server, doc)
}

func cacheKeyDocumentContext(ctx context.Context, server string, doc *ServerKeyResponse) {
	cache := GetKeyCache()
	for id, info := range doc.VerifyKeys {
		if key, err := base64.RawStdEncoding.DecodeString(info.Key); err == nil && len(key) == ed25519.PublicKeySize {
			cache.setContext(ctx, server, id, key, time.UnixMilli(doc.ValidUntil), 0)
		}
	}
	for id, info := range doc.OldVerifyKeys {
		if info.ExpiredTS <= 0 {
			continue
		}
		if key, err := base64.RawStdEncoding.DecodeString(info.Key); err == nil && len(key) == ed25519.PublicKeySize {
			// An old key is only good for events before it expired.
			cache.setContext(ctx, server, id, key, time.UnixMilli(info.ExpiredTS), info.ExpiredTS)
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
