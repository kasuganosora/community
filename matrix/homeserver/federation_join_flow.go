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
type joinSettings struct {
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

func envInt(name string, def, min, max int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name))); err == nil && n >= min && n <= max {
		return n
	}
	return def
}

func loadJoinSettings() joinSettings {
	return joinSettings{
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
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	add(extractServerName(roomID))
	for _, s := range via {
		add(s)
	}
	for _, s := range directory {
		add(s)
	}
	add(inviter)
	return out
}

func selectJoinCandidates(servers []string, s joinSettings) []string {
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

	if winner != nil && ctx.Err() == nil {
		if err := r.persistFederationJoinContext(ctx, winner.prepared); err != nil {
			log.Warn("matrix join %s via %s: storing validated join failed: %v", roomID, winner.server, err)
			if ctx.Err() != nil {
				return "", joinContextError(ctx, next, s.Total)
			}
			return "", &MatrixError{ErrCode: "M_UNKNOWN", Error: "Remote server returned an invalid room join response"}
		}
		log.Info("matrix join %s via %s ok in %s (dropped %d unverifiable events, tried %d of %d candidates)",
			roomID, winner.server, time.Since(started).Round(time.Millisecond), winner.prepared.dropped, next, len(candidates))
		go r.HS.NotifyRoomEvent(roomID)
		return roomID, nil
	}
	if winner != nil {
		expired = true
	}
	for _, f := range failures {
		log.Warn("matrix join %s via %s failed: %v", roomID, f.server, f.err)
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
	case "M_FORBIDDEN", "M_UNABLE_TO_AUTHORISE_JOIN", "M_UNABLE_TO_GRANT_JOIN", "M_INCOMPATIBLE_ROOM_VERSION", "M_UNSUPPORTED_ROOM_VERSION", "M_LIMIT_EXCEEDED":
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
	for _, f := range failures {
		if f.failure != nil && joinFailureRank(f.failure) > bestRank {
			best, bestRank = f.failure, joinFailureRank(f.failure)
		}
	}
	return best
}

func (r *RoomService) attemptFederationJoin(ctx context.Context, s joinSettings, userID, roomID, server string, sendSlot chan struct{}) joinAttemptOutcome {
	fail := func(err error, m *MatrixError) joinAttemptOutcome {
		if ctx.Err() == nil && isDeadHostError(err) {
			deadFederationHosts.mark(server, s.DeadHostTTL, err.Error())
		}
		if m == nil {
			m = federationJoinError(err)
		}
		return joinAttemptOutcome{server: server, err: err, failure: m}
	}
	client := newFederationClient(r.HS)

	mjCtx, cancelMJ := context.WithTimeout(ctx, s.MakeJoinTimeout)
	response, err := client.MakeJoinContext(mjCtx, server, roomID, userID)
	cancelMJ()
	if err != nil {
		return fail(fmt.Errorf("make_join: %w", err), nil)
	}
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
	select {
	case sendSlot <- struct{}{}:
	case <-ctx.Done():
		return joinAttemptOutcome{server: server, err: ctx.Err()}
	}
	// The slot stays held on success: a queued attempt must not send a second
	// join while the winner is being handed back and the others cancelled.
	succeeded := false
	defer func() {
		if !succeeded {
			<-sendSlot
		}
	}()

	sjCtx, cancelSJ := context.WithTimeout(ctx, s.SendJoinTimeout)
	joined, err := client.SendJoinContext(sjCtx, server, roomID, wire, s.MaxBodyBytes)
	cancelSJ()
	if err != nil {
		if errors.Is(err, errFederationResponseTooLarge) {
			return fail(err, &MatrixError{ErrCode: "M_TOO_LARGE", Error: "The room's join state is larger than this server accepts"})
		}
		return fail(fmt.Errorf("send_join: %w", err), nil)
	}
	prepared, err := r.validateFederationJoin(withKeyNotaries(ctx, server), roomID, userID, version, p.JSON(), joined)
	if err != nil {
		return fail(fmt.Errorf("validate join response: %w", err), &MatrixError{ErrCode: "M_UNKNOWN", Error: "Remote server returned an invalid room join response"})
	}
	succeeded = true
	return joinAttemptOutcome{server: server, prepared: prepared}
}
