package homeserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kasuganosora/community/log"
	gmsl "github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/spec"
)

// Remote room join policy
//
//   - Candidate servers are tried in this order: the room ID's domain, the
//     caller's via/server_name hints, the alias directory's servers, the
//     inviter's server. Duplicates and ourselves are dropped, servers that
//     failed at the network level in the last MATRIX_JOIN_DEAD_HOST_TTL are
//     skipped (unless nothing else is left), and at most
//     MATRIX_JOIN_MAX_ATTEMPTS are used.
//   - Up to MATRIX_JOIN_PARALLELISM attempts run concurrently; the first to
//     finish validating wins and the rest are cancelled. make_join runs in
//     parallel, but only one attempt at a time may send_join, so a room never
//     receives two different join events from us.
//   - Everything runs under one deadline (MATRIX_JOIN_TOTAL_TIMEOUT, default
//     25s, below the 30s HTTP handler timeout) derived from the request
//     context. Cancelling it aborts HTTP calls, key fetches and validation;
//     JoinRoom waits for every attempt goroutine before it returns.
//   - At most MATRIX_JOIN_MAX_CONCURRENT joins validate/hold state at once.
//   - send_join responses are capped (MATRIX_FEDERATION_SEND_JOIN_MAX_MB).
//   - send_join asks for a partial-state answer (MSC3706 omit_members=true,
//     MATRIX_JOIN_OMIT_MEMBERS=0 disables it): a resident server that supports
//     it leaves out most member events, which keeps the answer small enough
//     for the budget in huge rooms. The join then completes with the room
//     flagged partial and the members are fetched in the background (see
//     federation_join_async.go). A server that ignores the parameter answers
//     in full as before; one that rejects it is asked again without it; an
//     omit_members answer that fails validation is replaced by a full one when
//     the budget allows.
type joinSettings struct {
	OmitMembers     bool
	Total           time.Duration
	MakeJoinTimeout time.Duration
	SendJoinTimeout time.Duration
	MaxAttempts     int
	Parallelism     int
	MaxConcurrent   int
	DeadHostTTL     time.Duration
	MaxBodyBytes    int64
}

func envDuration(name string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return d
	}
	if n, err := strconv.ParseFloat(v, 64); err == nil && n > 0 {
		return time.Duration(n * float64(time.Second))
	}
	return def
}

func envBool(name string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

func envInt(name string, def, min, max int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name))); err == nil && n >= min && n <= max {
		return n
	}
	return def
}

func loadJoinSettings() joinSettings {
	return joinSettings{
		OmitMembers:     envBool("MATRIX_JOIN_OMIT_MEMBERS", true),
		Total:           envDuration("MATRIX_JOIN_TOTAL_TIMEOUT", 25*time.Second),
		MakeJoinTimeout: envDuration("MATRIX_JOIN_MAKE_TIMEOUT", 10*time.Second),
		SendJoinTimeout: envDuration("MATRIX_JOIN_SEND_TIMEOUT", 20*time.Second),
		MaxAttempts:     envInt("MATRIX_JOIN_MAX_ATTEMPTS", 5, 1, 20),
		Parallelism:     envInt("MATRIX_JOIN_PARALLELISM", 3, 1, 8),
		MaxConcurrent:   envInt("MATRIX_JOIN_MAX_CONCURRENT", 3, 1, 32),
		DeadHostTTL:     envDuration("MATRIX_JOIN_DEAD_HOST_TTL", 5*time.Minute),
		MaxBodyBytes:    sendJoinBodyLimit(),
	}
}

// joinSettingsSource is replaceable in tests.
var joinSettingsSource = loadJoinSettings

// newFederationClient builds the client used for joins and key fetches;
// tests substitute one that trusts their fake servers.
var newFederationClient = NewFederationClient

// joinGateWait is how long a join waits for a free slot before M_LIMIT_EXCEEDED.
// joinAttemptsRunning counts live attempt goroutines (tests assert it is back
// to zero when a join returns).
var joinAttemptsRunning atomic.Int64

var joinGateWait = 5 * time.Second

const joinTimeoutPrefix = "Timed out joining the room"

func joinTimeoutError(tried int, total time.Duration) *MatrixError {
	return &MatrixError{ErrCode: "M_UNKNOWN", Error: fmt.Sprintf("%s: no server answered in time (%d tried within %s). Try again later or join with a different via server.", joinTimeoutPrefix, tried, total.Round(time.Second))}
}

func joinTooBusyError() *MatrixError {
	return &MatrixError{ErrCode: "M_LIMIT_EXCEEDED", Error: "Too many room joins are in progress, try again shortly"}
}

// ---- concurrency gate -------------------------------------------------------

var (
	joinGateMu  sync.Mutex
	joinGateCap int
	joinGateCh  chan struct{}
)

func joinGate(capacity int) chan struct{} {
	joinGateMu.Lock()
	defer joinGateMu.Unlock()
	if joinGateCh == nil || joinGateCap != capacity {
		// A capacity change (tests, env edits at runtime) starts a fresh gate;
		// holders of the old channel release into it harmlessly.
		joinGateCh = make(chan struct{}, capacity)
		joinGateCap = capacity
	}
	return joinGateCh
}

// ---- candidate selection ----------------------------------------------------

// orderJoinServers returns the candidate list in policy order, deduplicated.
func orderJoinServers(self, roomID string, via, directory []string, inviter string) []string {
	var out []string
	seen := map[string]bool{"": true, self: true}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s != "" && !usableServerName(s) {
			return
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	add(extractServerName(roomID))
	for _, s := range via {
		add(s)
	}
	for i, s := range directory {
		if i >= maxDirectoryCandidates {
			break
		}
		add(s)
	}
	add(inviter)
	return out
}

// maxDirectoryCandidates bounds how much of a (remote, untrusted) directory
// answer is considered; the attempt cap is far smaller anyway.
const maxDirectoryCandidates = 20

// usableServerName reports whether s is a syntactically valid Matrix server
// name. Remote directory answers and client hints are untrusted input.
func usableServerName(s string) bool {
	_, _, ok := spec.ParseAndValidateServerName(spec.ServerName(strings.TrimSpace(s)))
	return ok
}

func selectJoinCandidates(servers []string, s joinSettings) []string {
	servers = dropDelegationTargets(servers)
	var live []string
	for _, server := range servers {
		if _, dead := deadFederationHosts.active(server); !dead {
			live = append(live, server)
		}
	}
	if len(live) == 0 && len(servers) > 0 {
		// Everything recently failed: still probe the best candidates instead
		// of refusing outright, the hosts may have come back.
		live = servers[:min(len(servers), s.Parallelism)]
	}
	if len(live) > s.MaxAttempts {
		live = live[:s.MaxAttempts]
	}
	return live
}

// dropDelegationTargets removes candidates that are only the delegation target
// of another candidate: matrix.org delegates to matrix-federation.matrix.org:443,
// and a caller that passes that host as a via/server_name would otherwise make
// us dial matrix-federation.matrix.org:8448, which does not answer. The
// delegating server name (already resolved by the alias lookup or an earlier
// attempt) reaches the very same machine the right way.
func dropDelegationTargets(servers []string) []string {
	d := peekSharedDiscovery()
	if d == nil || len(servers) < 2 {
		return servers
	}
	present := make(map[string]bool, len(servers))
	for _, s := range servers {
		present[strings.ToLower(s)] = true
	}
	out := make([]string, 0, len(servers))
	for _, s := range servers {
		if name, ok := d.delegatedFrom(s); ok && present[strings.ToLower(string(name))] {
			log.Info("matrix join: not trying %s separately, it is the delegation target of %s", s, name)
			continue
		}
		out = append(out, s)
	}
	return out
}

// isDeadHostError reports failures that say the host itself is unusable right
// now: transport errors (DNS, refused, TLS, timeout) and gateway/5xx answers
// (Cloudflare 52x included). Matrix-level answers (403, 404, M_*) are not.
func isDeadHostError(err error) bool {
	var te *federationTransportError
	if errors.As(err, &te) {
		return true
	}
	var he *federationHTTPError
	if errors.As(err, &he) {
		return he.Status == 502 || he.Status == 503 || he.Status == 504 || (he.Status >= 520 && he.Status <= 530)
	}
	return false
}

type federationTransportError struct{ err error }

func (e *federationTransportError) Error() string { return "request failed: " + e.err.Error() }
func (e *federationTransportError) Unwrap() error { return e.err }

// ---- orchestration ----------------------------------------------------------

type joinAttemptOutcome struct {
	server   string
	prepared *preparedFederationJoin
	failure  *MatrixError
	err      error
	diag     joinAttemptDiag
}

// joinAttemptDiag says how far one candidate got, for the join log.
type joinAttemptDiag struct {
	phase   string // make_join, send_join(...), decode, validate, ...
	started time.Time
	// sendJoin is set once the attempt reached send_join.
	sendJoin  *sendJoinStats
	sendStart time.Time
	// omitted: the answer used was a members_omitted one.
	omitted bool
	note    string
}

func (d joinAttemptDiag) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "phase=%s elapsed=%dms", d.phase, time.Since(d.started).Milliseconds())
	if d.sendJoin != nil {
		fmt.Fprintf(&b, " send_join_elapsed=%dms %s", time.Since(d.sendStart).Milliseconds(), d.sendJoin)
	}
	if d.note != "" {
		b.WriteString(" " + d.note)
	}
	return b.String()
}

// logJoinFailure writes one complete line per failed candidate: class, phase,
// timing, send_join byte counts and the untruncated error chain.
func logJoinFailure(roomID string, f joinAttemptOutcome) {
	log.Warn("matrix join %s via %s failed: class=%s %s error=%q", roomID, f.server, classifyFederationError(f.err), f.diag, fmt.Sprint(f.err))
}

// federationJoin 通过联邦协议加入远程房间.
func (r *RoomService) federationJoin(userID, roomID string, servers []string) (string, *MatrixError) {
	return r.federationJoinContext(context.Background(), userID, roomID, servers)
}

func (r *RoomService) federationJoinContext(ctx context.Context, userID, roomID string, servers []string) (string, *MatrixError) {
	s := joinSettingsSource()
	started := time.Now()
	ctx, cancel := context.WithTimeout(ctx, s.Total)
	defer cancel()

	gate := joinGate(s.MaxConcurrent)
	wait := time.NewTimer(joinGateWait)
	defer wait.Stop()
	select {
	case gate <- struct{}{}:
		defer func() { <-gate }()
	case <-wait.C:
		log.Warn("matrix join %s: %d joins already in progress, refusing %s", roomID, s.MaxConcurrent, userID)
		return "", joinTooBusyError()
	case <-ctx.Done():
		return "", joinContextError(ctx, 0, s.Total)
	}

	candidates := selectJoinCandidates(servers, s)
	if len(candidates) == 0 {
		return "", &MatrixError{ErrCode: "M_UNKNOWN", Error: "Unable to contact a server for this room"}
	}

	attemptCtx, cancelAttempts := context.WithCancel(ctx)
	defer cancelAttempts()
	results := make(chan joinAttemptOutcome, len(candidates))
	sendSlot := make(chan struct{}, 1)
	var wg sync.WaitGroup
	next, inflight := 0, 0
	var winner *joinAttemptOutcome
	var failures []joinAttemptOutcome
	expired := false
loop:
	for winner == nil && (inflight > 0 || next < len(candidates)) {
		for inflight < s.Parallelism && next < len(candidates) {
			server := candidates[next]
			next++
			inflight++
			wg.Add(1)
			joinAttemptsRunning.Add(1)
			go func() {
				defer wg.Done()
				defer joinAttemptsRunning.Add(-1)
				results <- r.attemptFederationJoin(attemptCtx, s, userID, roomID, server, sendSlot)
			}()
		}
		select {
		case out := <-results:
			inflight--
			if out.prepared != nil {
				winner = &out
			} else {
				failures = append(failures, out)
			}
		case <-ctx.Done():
			expired = true
			break loop
		}
	}
	// Nothing may outlive this call: cancel the losers and wait for them.
	cancelAttempts()
	wg.Wait()
	// Attempts that were cut off by the deadline still report why; they must
	// show up in the log instead of vanishing with the channel.
	for drained := false; !drained; {
		select {
		case out := <-results:
			if out.prepared != nil {
				out.prepared, out.err = nil, errors.New("join response was ready only after the join deadline")
			}
			failures = append(failures, out)
		default:
			drained = true
		}
	}

	if winner != nil && ctx.Err() == nil {
		storeStart := time.Now()
		rem, err := r.persistFederationJoinStaged(ctx, winner.prepared, winner.server)
		if err != nil {
			log.Warn("matrix join %s via %s failed: class=%s phase=store elapsed=%dms (%s) error=%q", roomID, winner.server, classifyFederationError(err), time.Since(storeStart).Milliseconds(), winner.diag, fmt.Sprint(err))
			if ctx.Err() != nil {
				return "", joinContextError(ctx, next, s.Total)
			}
			return "", &MatrixError{ErrCode: "M_UNKNOWN", Error: "Remote server returned an invalid room join response"}
		}
		dropped := winner.prepared.dropped
		omitted := winner.prepared.membersOmitted
		log.Debug("matrix join %s via %s: stage-1 stored in %dms", roomID, winner.server, time.Since(storeStart).Milliseconds())
		// Only the remainder (if any) may outlive this call; release the rest.
		winner.prepared.events, winner.prepared.state, winner.prepared.critical = nil, nil, nil
		deferred := 0
		if rem != nil {
			deferred = r.finishOrDeferJoinRemainder(ctx, rem)
		}
		log.Info("matrix join %s via %s ok in %s (members_omitted=%t, dropped %d unverifiable events, %d state events completing in background, tried %d of %d candidates; %s)",
			roomID, winner.server, time.Since(started).Round(time.Millisecond), omitted, dropped, deferred, next, len(candidates), winner.diag)
		go r.HS.NotifyRoomEvent(roomID)
		return roomID, nil
	}
	if winner != nil {
		expired = true
	}
	for _, f := range failures {
		logJoinFailure(roomID, f)
	}
	if expired || ctx.Err() != nil {
		log.Warn("matrix join %s: gave up after %s (%d candidates, %d attempted, %d failed)", roomID, time.Since(started).Round(time.Millisecond), len(candidates), next, len(failures))
		return "", joinContextError(ctx, next, s.Total)
	}
	return "", bestJoinFailure(failures)
}

func joinContextError(ctx context.Context, tried int, total time.Duration) *MatrixError {
	if errors.Is(ctx.Err(), context.Canceled) {
		return &MatrixError{ErrCode: "M_UNKNOWN", Error: "The join request was cancelled"}
	}
	return joinTimeoutError(tried, total)
}

func joinFailureRank(e *MatrixError) int {
	switch e.ErrCode {
	case "M_FORBIDDEN", "M_UNABLE_TO_AUTHORISE_JOIN", "M_UNABLE_TO_GRANT_JOIN", "M_INCOMPATIBLE_ROOM_VERSION", "M_UNSUPPORTED_ROOM_VERSION", "M_LIMIT_EXCEEDED", "M_TOO_LARGE":
		return 3
	case "M_NOT_FOUND":
		return 2
	}
	return 1
}

// bestJoinFailure prefers a decisive answer (forbidden, version) over "not
// found" from an unrelated server, and both over a generic failure.
func bestJoinFailure(failures []joinAttemptOutcome) *MatrixError {
	best := &MatrixError{ErrCode: "M_UNKNOWN", Error: "Unable to contact a server for this room"}
	bestRank := 0
	unreachable, notActive := 0, 0
	for _, f := range failures {
		if isDeadHostError(f.err) {
			unreachable++
		}
		if f.failure != nil && f.failure.ErrCode == "M_NOT_FOUND" {
			notActive++
		}
		if f.failure != nil && joinFailureRank(f.failure) > bestRank {
			best, bestRank = f.failure, joinFailureRank(f.failure)
		}
	}
	if best.ErrCode == "M_NOT_FOUND" && unreachable > 0 {
		// A 404 from one server that is not (or no longer) in the room proves
		// nothing about the room when the others could not even be asked.
		return &MatrixError{ErrCode: "M_NOT_FOUND", Error: fmt.Sprintf("No reachable server could join this room: %d server(s) did not answer and %d reported it is not an active room on their server", unreachable, notActive)}
	}
	return best
}

func (r *RoomService) attemptFederationJoin(ctx context.Context, s joinSettings, userID, roomID, server string, sendSlot chan struct{}) joinAttemptOutcome {
	diag := joinAttemptDiag{phase: "make_join", started: time.Now()}
	// done fills the outcome with the diagnostics collected so far.
	done := func(o joinAttemptOutcome) joinAttemptOutcome { o.diag = diag; return o }
	fail := func(err error, m *MatrixError) joinAttemptOutcome {
		if ctx.Err() == nil && isDeadHostError(err) {
			deadFederationHosts.mark(server, s.DeadHostTTL, err.Error())
		}
		if m == nil {
			m = federationJoinError(err)
		}
		return done(joinAttemptOutcome{server: server, err: err, failure: m})
	}
	client := newFederationClient(r.HS)

	mjCtx, cancelMJ := context.WithTimeout(ctx, s.MakeJoinTimeout)
	response, err := client.MakeJoinContext(mjCtx, server, roomID, userID)
	cancelMJ()
	if err != nil {
		return fail(fmt.Errorf("make_join: %w", err), nil)
	}
	log.Debug("matrix join %s via %s: make_join ok after %dms", roomID, server, time.Since(diag.started).Milliseconds())
	diag.phase = "build_join_event"
	deadFederationHosts.clear(server)
	version, _ := response["room_version"].(string)
	if version == "" {
		version = "1"
	}
	impl, err := gmsl.GetRoomVersion(gmsl.RoomVersion(version))
	if err != nil {
		return fail(fmt.Errorf("room version %s: %w", version, err), &MatrixError{ErrCode: "M_INCOMPATIBLE_ROOM_VERSION", Error: "This server does not support room version " + version})
	}
	raw, err := json.Marshal(response["event"])
	if err != nil {
		return fail(err, &MatrixError{ErrCode: "M_UNKNOWN", Error: "Remote server returned an invalid join template"})
	}
	builder := impl.NewEventBuilder()
	if err = json.Unmarshal(raw, builder); err != nil {
		return fail(err, &MatrixError{ErrCode: "M_UNKNOWN", Error: "Remote server returned an invalid join template"})
	}
	builder.RoomID, builder.SenderID, builder.Type, builder.StateKey = roomID, userID, "m.room.member", &userID
	content, err := federationJoinContent(builder.Content)
	if err != nil {
		return fail(err, nil)
	}
	if err = builder.SetContent(content); err != nil {
		return fail(err, nil)
	}
	p, err := builder.Build(time.Now(), spec.ServerName(r.HS.Config.ServerName), gmsl.KeyID(r.HS.keyID), r.HS.privateKey)
	if err != nil {
		return fail(fmt.Errorf("build join event: %w", err), &MatrixError{ErrCode: "M_UNKNOWN", Error: "Unable to build the join event"})
	}
	wire, _ := wirePDU(p)
	wire["event_id"] = p.EventID()

	// One send_join at a time (see policy above).
	diag.phase = "wait_send_slot"
	select {
	case sendSlot <- struct{}{}:
	case <-ctx.Done():
		return done(joinAttemptOutcome{server: server, err: ctx.Err()})
	}
	// The slot stays held on success: a queued attempt must not send a second
	// join while the winner is being handed back and the others cancelled.
	succeeded := false
	defer func() {
		if !succeeded {
			<-sendSlot
		}
	}()

	// sendJoin runs one send_join exchange (partial-state or full) and
	// records how it went.
	sendJoin := func(omit bool) (*sendJoinResponse, *joinAttemptOutcome) {
		stats := &sendJoinStats{}
		diag.sendJoin, diag.sendStart, diag.phase = stats, time.Now(), "send_join"
		remaining := time.Duration(0)
		if dl, ok := ctx.Deadline(); ok {
			remaining = time.Until(dl).Round(time.Millisecond)
		}
		log.Info("matrix join %s via %s: send_join start omit_members=%t (timeout %s, join budget left %s, body cap %d MiB)",
			roomID, server, omit, s.SendJoinTimeout, remaining, s.MaxBodyBytes>>20)
		sjCtx, cancelSJ := context.WithTimeout(ctx, s.SendJoinTimeout)
		joined, err := client.SendJoinWithOptions(sjCtx, server, roomID, wire, s.MaxBodyBytes, sendJoinOptions{OmitMembers: omit, Stats: stats})
		cancelSJ()
		if err != nil {
			diag.phase = stats.phase()
			var syntax *json.SyntaxError
			var typeErr *json.UnmarshalTypeError
			if errors.As(err, &syntax) || errors.As(err, &typeErr) {
				diag.phase = "decode"
			}
			var tooMany *sendJoinTooManyEventsError
			if errors.As(err, &tooMany) {
				log.Warn("matrix join %s via %s refused: %v", roomID, server, err)
				o := fail(err, joinStateTooLargeError(tooMany.limit))
				return nil, &o
			}
			if errors.Is(err, errFederationResponseTooLarge) {
				o := fail(err, &MatrixError{ErrCode: "M_TOO_LARGE", Error: "The room's join state is larger than this server accepts"})
				return nil, &o
			}
			o := fail(fmt.Errorf("send_join: %w", err), nil)
			return nil, &o
		}
		diag.phase = "validate"
		diag.omitted = joined.MembersOmitted
		log.Info("matrix join %s via %s: send_join answered in %dms: %d state + %d auth chain events, %d bytes, members_omitted=%t, servers_in_room=%d (%s)",
			roomID, server, time.Since(diag.sendStart).Milliseconds(), len(joined.State), len(joined.AuthChain), stats.bytes.Load(), joined.MembersOmitted, len(joined.ServersInRoom), stats)
		return joined, nil
	}

	omit := s.OmitMembers
	joined, o := sendJoin(omit)
	if o != nil {
		return *o
	}
	validateStart := time.Now()
	prepared, err := r.validateFederationJoin(withKeyNotaries(ctx, server), roomID, userID, version, p.JSON(), joined)
	if err != nil && joined.MembersOmitted && ctx.Err() == nil {
		// Our partial-state expectations may be stricter than this remote's
		// answer (e.g. a membership our own join needs was omitted). The same
		// signed join event can be sent again; ask for the full state.
		log.Warn("matrix join %s via %s: members_omitted answer failed validation after %dms (%v); retrying send_join without omit_members",
			roomID, server, time.Since(validateStart).Milliseconds(), err)
		joined = nil
		if joined, o = sendJoin(false); o != nil {
			return *o
		}
		validateStart = time.Now()
		prepared, err = r.validateFederationJoin(withKeyNotaries(ctx, server), roomID, userID, version, p.JSON(), joined)
	}
	if err != nil {
		diag.phase = "validate"
		return fail(fmt.Errorf("validate join response: %w", err), &MatrixError{ErrCode: "M_UNKNOWN", Error: "Remote server returned an invalid room join response"})
	}
	log.Debug("matrix join %s via %s: join response validated in %dms (%d events)", roomID, server, time.Since(validateStart).Milliseconds(), len(prepared.events))
	succeeded = true
	diag.phase = "store"
	return done(joinAttemptOutcome{server: server, prepared: prepared})
}
