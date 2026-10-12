package homeserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	gmsl "github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"gorm.io/gorm"
)

// Join response validation policy
//
// A send_join answer carries the room's current state and the auth chain of
// that state, signed by whichever servers sent those events - often long dead.
// Requiring every one of those signatures to verify made a join depend on the
// availability of every server that ever spoke in the room. Instead:
//
//	REQUIRED (any failure aborts the join, and the next server is tried):
//	  - our own join event and, for restricted rooms, the countersigned event
//	    returned by the resident server;
//	  - exactly one m.room.create event, signed and authorised;
//	  - every event in the transitive auth chain of the join event, each with a
//	    valid signature and passing the auth rules against its own auth events;
//	  - current join-auth state and security policy (ACL, history visibility,
//	    encryption), with their transitive auth ancestors;
//	  - the join event being allowed by the resulting state.
//
//	BEST EFFORT (older state and history; failures only drop the event):
//	  - every other state/auth-chain event. If its signature cannot be verified
//	    (signer unreachable, key unobtainable even via notaries, key not valid
//	    at the event's timestamp) or it is not authorised by the remaining
//	    events, it is skipped: not persisted and not part of the state we apply.
//	    Such drops are logged at debug per event and summarised once at info.
//	    Unverified events are never stored; "skipped" is the soft-fail policy.
//
// Structural violations by the remote (events of another room, duplicate state
// tuples) always abort. Signature checks use the room version's own validity
// rule (v1-v4 accept expired keys, v5+ require validity at origin_server_ts).
type preparedFederationJoin struct {
	roomID  string
	version string
	events  map[string]gmsl.PDU
	state   []gmsl.PDU
	join    gmsl.PDU
	creator string
	dropped int
	// critical is the set of event IDs the join's own authorisation depends on
	// (create, join auth chain, security policy and their ancestors).
	critical map[string]bool
	// membersOmitted: the remote answered with MSC3706 partial state, so the
	// member list is incomplete and a full state fetch must follow.
	membersOmitted bool
	serversInRoom  []string
}

func rawIsNull(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || bytes.Equal(t, []byte("null"))
}

func (r *RoomService) validateFederationJoin(ctx context.Context, roomID, joiningUserID, version string, localJoin json.RawMessage, resp *sendJoinResponse) (*preparedFederationJoin, error) {
	if resp == nil {
		return nil, fmt.Errorf("empty join response")
	}
	var returned any
	if !rawIsNull(resp.Event) {
		returned = resp.Event
	}
	join, err := verifiedFederationJoinContext(ctx, r.HS, version, roomID, joiningUserID, localJoin, returned)
	if err != nil {
		return nil, fmt.Errorf("join event: %w", err)
	}
	// A members_omitted (MSC3706) answer carries the same kind of data minus
	// most m.room.member events: everything provided is validated exactly as
	// in a full answer (the auth chain of every non-member state event is
	// included by the remote), and the missing members are fetched, validated
	// and stored later by the partial-state background sync.
	pj, err := r.validateFederationStateSet(ctx, roomID, joiningUserID, version, join, resp, false)
	if err != nil {
		return nil, err
	}
	pj.membersOmitted = resp.MembersOmitted
	if resp.MembersOmitted {
		pj.serversInRoom = cleanServersInRoom(resp.ServersInRoom, r.HS.Config.ServerName)
	}
	return pj, nil
}

// cleanServersInRoom keeps the syntactically valid, distinct server names of an
// untrusted servers_in_room list (never ourselves).
func cleanServersInRoom(in []string, self string) []string {
	seen := map[string]bool{"": true, self: true}
	out := make([]string, 0, min(len(in), maxServersInRoom))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if seen[s] || !usableServerName(s) {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// validateFederationStateSet validates the state/auth chain of a join response
// against the (already verified) join event. With resume set, the join event
// is the one stored from an earlier join and the state is a later /state
// answer that no longer contains the pre-join state of the joining user, so
// the join-specific requirements (join auth events present, join allowed by
// the returned state) are not demanded again; every event is still signature-
// and auth-checked before it can be stored.
func (r *RoomService) validateFederationStateSet(ctx context.Context, roomID, joiningUserID, version string, join gmsl.PDU, resp *sendJoinResponse, resume bool) (*preparedFederationJoin, error) {
	if resume {
		ctx = withCPUThrottle(ctx) // background work yields to request handling
	}
	neededState := map[string]bool{}
	needed := gmsl.StateNeededForAuth([]gmsl.PDU{join})
	for _, tuple := range needed.Tuples() {
		neededState[tuple.EventType+"\x00"+tuple.StateKey] = true
	}
	// Dropping these changes federation access, history disclosure or the
	// encryption policy even though they are not membership auth events.
	for _, kind := range []string{"m.room.server_acl", "m.room.history_visibility", "m.room.encryption"} {
		neededState[kind+"\x00"] = true
	}
	criticalState := []string{}
	events := make(map[string]gmsl.PDU)
	stateIDs := make(map[string]bool)
	tuples := map[string]bool{}
	var undecodable int
	for _, field := range []struct {
		name  string
		list  *[]json.RawMessage
		state bool
	}{{"auth_chain", &resp.AuthChain, false}, {"state", &resp.State, true}} {
		if *field.list == nil {
			return nil, fmt.Errorf("missing %s", field.name)
		}
		for i, raw := range *field.list {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			throttleTick(ctx)
			requiredState := false
			if field.state {
				var header struct {
					Type     string  `json:"type"`
					StateKey *string `json:"state_key"`
				}
				if err := json.Unmarshal(raw, &header); err != nil {
					return nil, fmt.Errorf("invalid state event envelope: %w", err)
				}
				if header.StateKey == nil || header.Type == "" {
					return nil, fmt.Errorf("state event has no type or state_key")
				}
				requiredState = neededState[header.Type+"\x00"+*header.StateKey]
			}
			p, err := parseAndValidatePDU(version, raw)
			(*field.list)[i] = nil // release the raw copy as soon as it is parsed
			if err != nil {
				if field.state {
					return nil, fmt.Errorf("invalid current state event: %w", err)
				}
				undecodable++
				log.Debug("federation join %s: skipping undecodable %s event: %v", roomID, field.name, err)
				continue
			}
			if p.RoomID().String() != roomID {
				return nil, fmt.Errorf("invalid room state event")
			}
			if p.StateKey() == nil {
				undecodable++
				continue
			}
			events[p.EventID()] = p
			if requiredState {
				criticalState = append(criticalState, p.EventID())
			}
			if field.state {
				tuple := p.Type() + "\x00" + *p.StateKey()
				if tuples[tuple] {
					return nil, fmt.Errorf("duplicate state tuple")
				}
				tuples[tuple] = true
				stateIDs[p.EventID()] = true
			}
		}
	}

	// Required set: the create event plus the join event's auth chain.
	critical := map[string]bool{}
	todo := append([]string(nil), criticalState...)
	if !resume {
		todo = append(todo, join.AuthEventIDs()...)
	}
	for len(todo) > 0 {
		id := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		if critical[id] {
			continue
		}
		event, ok := events[id]
		if !ok {
			return nil, fmt.Errorf("join auth event %s missing from the response", id)
		}
		critical[id] = true
		todo = append(todo, event.AuthEventIDs()...)
	}
	creates := 0
	for id, e := range events {
		if e.Type() == "m.room.create" {
			creates++
			critical[id] = true
		}
	}
	if creates != 1 {
		return nil, fmt.Errorf("expected exactly one m.room.create event, found %d", creates)
	}

	// Verify the mandatory auth/security state first. Optional history must
	// never consume the network budget needed to establish that trust.
	verifier := federationJSONVerifier{r.HS}
	optional := make(map[string]gmsl.PDU)
	for id, p := range events {
		if !critical[id] {
			optional[id] = p
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := gmsl.VerifyEventSignatures(ctx, p, verifier, federationUserID); err != nil {
			return nil, fmt.Errorf("required event %s (%s): %w", id, p.Type(), err)
		}
	}
	// Inline joins keep a short key budget so a join never waits on old history.
	// The background completion of a partial-state join (resume) has minutes, and
	// thousands of origin servers to ask: a 2 s budget there leaves nearly every
	// old member event "unverifiable" for reasons of our own making.
	budget := 2 * time.Second
	if resume {
		budget = envDuration("MATRIX_JOIN_KEY_BUDGET", 120*time.Second)
	}
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline) / 4; remaining < budget {
			budget = remaining
		}
	}
	prefetchCtx, cancel := context.WithTimeout(ctx, budget)
	prefetch := r.prefetchSigningKeys(prefetchCtx, optional, resume)
	cancel()
	// Do not restart failed/unfinished network fetches serially for each old
	// event. Missing optional keys are dropped; signatures are still checked.
	cachedCtx := withCachedKeysOnly(ctx)
	droppedIDs := map[string]bool{}
	var unverifiable, badSig int
	failingOrigins := map[string]int{}
	for id, p := range optional {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		throttleTick(ctx)
		if err := gmsl.VerifyEventSignatures(cachedCtx, p, verifier, federationUserID); err != nil {
			if errors.Is(err, errKeyUnavailable) {
				unverifiable++
				failingOrigins[extractServerName(string(p.SenderID()))]++
			} else {
				badSig++
			}
			log.Debug("federation join %s: dropping %s %s: %v", roomID, p.Type(), id, err)
			droppedIDs[id] = true
		}
	}
	for id := range droppedIDs {
		delete(events, id)
		delete(stateIDs, id)
	}

	// Validate in dependency order. A failed dependency invalidates every
	// descendant, regardless of chain length or Go map iteration order.
	unauthorised, err := pruneInvalidJoinAuth(ctx, events, stateIDs, critical)
	if err != nil {
		return nil, err
	}

	state := make([]gmsl.PDU, 0, len(stateIDs))
	for id := range stateIDs {
		state = append(state, events[id])
	}
	sort.Slice(state, func(i, j int) bool { return state[i].EventID() < state[j].EventID() })

	provider, err := gmsl.NewAuthEvents(state)
	if err != nil {
		return nil, err
	}
	if !resume {
		if err = gmsl.Allowed(join, provider, federationUserID); err != nil {
			return nil, err
		}
	}
	if join.RoomID().String() != roomID || string(join.SenderID()) != joiningUserID {
		return nil, fmt.Errorf("join identity mismatch")
	}
	creator := ""
	for _, p := range state {
		if p.Type() == "m.room.create" {
			creator = string(p.SenderID())
		}
	}
	if creator == "" {
		return nil, fmt.Errorf("missing room create event")
	}
	dropped := len(droppedIDs) + unauthorised + undecodable
	if dropped > 0 {
		log.Info("federation join %s: kept %d events, skipped %d (unverifiable key: %d, bad signature: %d, unauthorised: %d, undecodable: %d)",
			roomID, len(events), dropped, unverifiable, badSig, unauthorised, undecodable)
	}
	if len(optional) > 0 && (unverifiable > 0 || prefetch.servers > 0) {
		log.Info("federation join %s: signing keys: %s; top origins with unverifiable events: %s",
			roomID, prefetch.String(), topOrigins(failingOrigins, 10))
	}
	return &preparedFederationJoin{roomID: roomID, version: version, events: events, state: state, join: join, creator: creator, dropped: dropped, critical: critical}, nil
}

// pruneInvalidJoinAuth checks each auth DAG node once and propagates rejection
// through its dependants. Cycles and missing ancestors never become trusted state.
func pruneInvalidJoinAuth(ctx context.Context, events map[string]gmsl.PDU, stateIDs, critical map[string]bool) (int, error) {
	pending := make(map[string]int, len(events))
	dependants := make(map[string][]string, len(events))
	invalid := make(map[string]bool)
	queue := make([]string, 0, len(events))
	for id, event := range events {
		refs := make(map[string]bool)
		for _, parent := range event.AuthEventIDs() {
			if refs[parent] {
				continue
			}
			refs[parent] = true
			if _, found := events[parent]; !found {
				invalid[id] = true
				continue
			}
			pending[id]++
			dependants[parent] = append(dependants[parent], id)
		}
		if pending[id] == 0 {
			queue = append(queue, id)
		}
	}
	processed := make(map[string]bool, len(events))
	for cursor := 0; cursor < len(queue); cursor++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		throttleTick(ctx)
		id := queue[cursor]
		processed[id] = true
		if !invalid[id] {
			if err := checkPDUAuthorization(events[id], events); err != nil {
				invalid[id] = true
			}
		}
		for _, child := range dependants[id] {
			if invalid[id] {
				invalid[child] = true
			}
			pending[child]--
			if pending[child] == 0 {
				queue = append(queue, child)
			}
		}
	}
	for id := range events {
		if !processed[id] {
			invalid[id] = true
		}
	}
	for id := range invalid {
		if critical[id] {
			return 0, fmt.Errorf("required event %s has invalid or incomplete auth ancestry", id)
		}
	}
	for id := range invalid {
		delete(events, id)
		delete(stateIDs, id)
	}
	return len(invalid), nil
}

// This collector only discovers signing requirements for bounded prefetch.
// Its result is never used as signature verification or as an authorization decision.
type joinSignatureRequests struct{ requests []gmsl.VerifyJSONRequest }

func (c *joinSignatureRequests) VerifyJSONs(_ context.Context, requests []gmsl.VerifyJSONRequest) ([]gmsl.VerifyJSONResult, error) {
	c.requests = append(c.requests, requests...)
	return make([]gmsl.VerifyJSONResult, len(requests)), nil
}

// keyPrefetchStats summarises one bulk key prefetch for the join log.
type keyPrefetchStats struct {
	servers, cached, direct, notary, failed int
	elapsed                                 time.Duration
	budgetExhausted                         bool
}

func (k keyPrefetchStats) String() string {
	return fmt.Sprintf("%d origin servers, %d already cached, %d fetched directly, %d via notary, %d unavailable (elapsed %s, budget exhausted: %t)",
		k.servers, k.cached, k.direct, k.notary, k.failed, k.elapsed.Round(time.Millisecond), k.budgetExhausted)
}

// topOrigins renders the n origins with the most affected events.
func topOrigins(counts map[string]int, n int) string {
	type kv struct {
		server string
		n      int
	}
	list := make([]kv, 0, len(counts))
	for server, c := range counts {
		list = append(list, kv{server, c})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].n != list[j].n {
			return list[i].n > list[j].n
		}
		return list[i].server < list[j].server
	})
	if len(list) == 0 {
		return "none"
	}
	var parts []string
	for i, e := range list {
		if i == n {
			parts = append(parts, fmt.Sprintf("... %d more origins", len(list)-n))
			break
		}
		reason := ""
		if why, dead := keyFetchFailures.active(e.server); dead {
			reason = " [" + truncateString(why, 80) + "]"
		}
		parts = append(parts, fmt.Sprintf("%s=%d%s", e.server, e.n, reason))
	}
	return strings.Join(parts, ", ")
}

func truncateString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// prefetchSigningKeys warms the key cache for every signing server in the
// response so verification itself never serialises on slow or dead servers
// (each costs at most one failure). Servers are asked directly with
// MATRIX_JOIN_KEY_WORKERS (default 24) concurrent workers; the servers that did
// not answer are then resolved with bulk queries to the trusted notaries (one
// POST per 100 servers) instead of one 8 s round trip each. Verification is
// unchanged: every document is checked exactly as before.
func (r *RoomService) prefetchSigningKeys(ctx context.Context, events map[string]gmsl.PDU, resume bool) keyPrefetchStats {
	started := time.Now()
	type want struct {
		keyID string
		at    spec.Timestamp
		check gmsl.SignatureValidityCheckFunc
	}
	wanted := map[string]want{}
	for _, p := range events {
		if ctx.Err() != nil {
			return keyPrefetchStats{elapsed: time.Since(started), budgetExhausted: true}
		}
		throttleTick(ctx)
		// Once an origin is known, further ordinary events of the same origin
		// ask for no new signer. Room v1/v2 event IDs and restricted joins name
		// extra signers, so those are always examined.
		if domain := extractServerName(string(p.SenderID())); domain != "" {
			if _, known := wanted[domain]; known && !strings.Contains(p.EventID(), ":") && !bytes.Contains(p.Content(), []byte("join_authorised_via_users_server")) {
				continue
			}
		}
		// Let gomatrixserverlib identify required signers. Arbitrary additional
		// signatures supplied by a remote must not trigger outbound key requests.
		collector := &joinSignatureRequests{}
		if gmsl.VerifyEventSignatures(ctx, p, collector, federationUserID) != nil {
			continue
		}
		for _, request := range collector.requests {
			server := string(request.ServerName)
			if server == r.HS.Config.ServerName {
				continue
			}
			if _, exists := wanted[server]; exists {
				continue
			}
			var env struct {
				Signatures map[string]map[string]json.RawMessage `json:"signatures"`
			}
			if json.Unmarshal(request.Message, &env) != nil {
				continue
			}
			for keyID := range env.Signatures[server] {
				wanted[server] = want{keyID: keyID, at: request.AtTS, check: request.ValidityCheckingFunc}
				break
			}
		}
	}
	stats := keyPrefetchStats{servers: len(wanted)}
	servers := make([]string, 0, len(wanted))
	cache := GetKeyCache()
	for s, w := range wanted {
		if _, ok := cache.GetForContext(ctx, s, w.keyID, w.at, w.check); ok {
			stats.cached++
			continue
		}
		servers = append(servers, s)
	}
	sort.Strings(servers)

	workers := envInt("MATRIX_JOIN_KEY_WORKERS", 24, 1, 128)
	if !resume {
		workers = min(workers, 8)
	}
	// Direct phase gets most of the remaining time; the rest is kept for the bulk
	// notary pass, which is what resolves dead servers.
	directCtx := ctx
	if deadline, ok := ctx.Deadline(); ok {
		var cancelDirect context.CancelFunc
		directCtx, cancelDirect = context.WithDeadline(ctx, time.Now().Add(time.Until(deadline)*2/3))
		defer cancelDirect()
	}
	verifier := federationJSONVerifier{r.HS}
	var direct atomic.Int64
	jobs := make(chan string)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for server := range jobs {
				w := wanted[server]
				if _, err := verifier.lookupKey(directCtx, server, w.keyID, w.at, w.check, false); err != nil {
					log.Debug("federation join: prefetch key %s:%s: %v", server, w.keyID, err)
				} else {
					direct.Add(1)
				}
			}
		}()
	}
sendJobs:
	for _, s := range servers {
		select {
		case jobs <- s:
		case <-directCtx.Done():
			break sendJobs
		}
	}
	close(jobs)
	wg.Wait()
	stats.direct = int(direct.Load())

	// Bulk notary pass for everything still missing.
	missing := map[string]bulkKeyWant{}
	for _, s := range servers {
		w := wanted[s]
		if _, ok := cache.GetForContext(ctx, s, w.keyID, w.at, w.check); !ok {
			missing[s] = bulkKeyWant{keyID: w.keyID, at: w.at, check: w.check}
		}
	}
	bulkAnswered := false
	if len(missing) > 0 && ctx.Err() == nil {
		for _, notary := range keyNotaries(ctx, r.HS, "") {
			if len(missing) == 0 || ctx.Err() != nil {
				break
			}
			names := make([]string, 0, len(missing))
			for s := range missing {
				names = append(names, s)
			}
			sort.Strings(names)
			for start := 0; start < len(names) && ctx.Err() == nil; start += 100 {
				group := map[string]bulkKeyWant{}
				for _, s := range names[start:min(start+100, len(names))] {
					group[s] = missing[s]
				}
				docs, err := verifier.fetchNotaryKeyDocumentsBulk(ctx, notary, group)
				if err != nil {
					log.Debug("federation join: bulk key query to notary %s failed: %v", notary, err)
					break
				}
				bulkAnswered = true
				for server, doc := range docs {
					cacheKeyDocumentContext(ctx, server, doc)
					w := group[server]
					if _, ok := cache.GetForContext(ctx, server, w.keyID, w.at, w.check); ok {
						stats.notary++
						delete(missing, server)
					}
				}
			}
		}
	}
	// A notary that does not implement the bulk POST gets the classic per-server
	// GET queries instead, with the same worker pool.
	if !bulkAnswered && len(missing) > 0 && ctx.Err() == nil {
		names := make([]string, 0, len(missing))
		for s := range missing {
			names = append(names, s)
		}
		sort.Strings(names)
		var viaNotary atomic.Int64
		jobs := make(chan string)
		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for server := range jobs {
					w := wanted[server]
					// The direct-only pass above recorded these servers as failed;
					// that must not stop the notary attempt that is now due.
					keyFetchFailures.clear(server)
					keyFetchFailures.clear(server + "|" + w.keyID)
					if _, err := verifier.serverKeyContext(ctx, server, w.keyID, w.at, w.check); err == nil {
						viaNotary.Add(1)
					}
				}
			}()
		}
	notaryJobs:
		for _, s := range names {
			select {
			case jobs <- s:
			case <-ctx.Done():
				break notaryJobs
			}
		}
		close(jobs)
		wg.Wait()
		stats.notary += int(viaNotary.Load())
		for _, s := range names {
			w := wanted[s]
			if _, ok := cache.GetForContext(ctx, s, w.keyID, w.at, w.check); ok {
				delete(missing, s)
			}
		}
	}
	stats.failed = len(missing)
	stats.budgetExhausted = ctx.Err() != nil
	stats.elapsed = time.Since(started)
	return stats
}

func (r *RoomService) persistFederationJoin(pj *preparedFederationJoin) error {
	return r.persistFederationJoinContext(context.Background(), pj)
}

func (r *RoomService) persistFederationJoinContext(ctx context.Context, pj *preparedFederationJoin) error {
	room := MatrixRoom{RoomID: pj.roomID, RoomVersion: pj.version, Creator: pj.creator}
	return db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("room_id = ?", pj.roomID).FirstOrCreate(&room).Error; err != nil {
			return err
		}
		for _, p := range pj.events {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := persistVerifiedPDU(tx, r.HS, p, false); err != nil {
				return err
			}
		}
		// Auth-chain events are historical, not current state. Apply only the snapshot.
		for _, p := range pj.state {
			if err := ctx.Err(); err != nil {
				return err
			}
			e := pduToMatrixEvent(p)
			r.updateRoomState(tx, pj.roomID, e)
			if e.EventType == EventTypeRoomMember {
				m, _ := e.Content["membership"].(string)
				r.updateRoomMember(tx, pj.roomID, *e.StateKey, MembershipState(m), e.EventID)
			}
		}
		return persistVerifiedPDUAtState(tx, r.HS, pj.join, true, pj.state, true)
	})
}

// processFederationJoinResponse validates and stores a loosely typed
// send_join response (version in "_verified_room_version", our signed event in
// "_local_join"). The join flow itself uses validateFederationJoin directly.
func (r *RoomService) processFederationJoinResponse(roomID, joiningUserID string, resp map[string]any) error {
	version, _ := resp["_verified_room_version"].(string)
	if version == "" {
		return fmt.Errorf("missing negotiated room version")
	}
	toRaw := func(field string) ([]json.RawMessage, error) {
		list, ok := resp[field].([]any)
		if !ok {
			return nil, nil
		}
		out := make([]json.RawMessage, 0, len(list))
		for _, v := range list {
			b, err := json.Marshal(v)
			if err != nil {
				return nil, err
			}
			out = append(out, b)
		}
		return out, nil
	}
	typed := &sendJoinResponse{}
	if value, exists := resp["members_omitted"]; exists {
		var ok bool
		typed.MembersOmitted, ok = value.(bool)
		if !ok {
			return fmt.Errorf("invalid members_omitted flag")
		}
	}
	var err error
	if typed.State, err = toRaw("state"); err != nil {
		return err
	}
	if typed.AuthChain, err = toRaw("auth_chain"); err != nil {
		return err
	}
	if ev, ok := resp["event"]; ok && ev != nil {
		if typed.Event, err = json.Marshal(ev); err != nil {
			return err
		}
	}
	var local json.RawMessage
	switch v := resp["_local_join"].(type) {
	case json.RawMessage:
		local = v
	case nil:
	default:
		if local, err = json.Marshal(v); err != nil {
			return err
		}
	}
	prepared, err := r.validateFederationJoin(context.Background(), roomID, joiningUserID, version, local, typed)
	if err != nil {
		return err
	}
	return r.persistFederationJoin(prepared)
}
