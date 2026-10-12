package homeserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	gmsl "github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/spec"
)

// Leaving a room that lives on other servers.
//
// A user's leave must be accepted by the room, not only recorded here. For a
// room whose origin is remote (including rooms joined through MSC3706 partial
// state, whose local DAG is incomplete and cannot yield valid prev_events) the
// leave event is obtained with make_leave from a resident server, signed by us
// and delivered with send_leave. Candidates, in order: the server that served
// our join, the servers_in_room hints of a partial join, the inviting server
// (invites), the room's origin and a few servers with joined members. Each
// attempt has its own timeout; the whole exchange is bounded by
// MATRIX_LEAVE_TIMEOUT (default 20s, below the 30s server write timeout).
//
//	MATRIX_LEAVE_TIMEOUT       overall federation budget of one leave (20s)
//	MATRIX_LEAVE_ATTEMPTS      servers tried at most (4)
//
// A remote that refuses with M_FORBIDDEN/M_NOT_FOUND is treated as "not in the
// room": the next candidate is tried and, if none accepts, the leave is applied
// locally so a user is never stuck in a dead room. If no server could be reached
// at all the user gets M_BAD_GATEWAY (502) instead of a generic 500, except for
// invites, which are always rejected locally as a fallback.

// MakeLeaveContext asks destServer for a leave event template.
func (c *FederationClient) MakeLeaveContext(ctx context.Context, destServer, roomID, userID string) (map[string]any, error) {
	u := fmt.Sprintf("%s/_matrix/federation/v1/make_leave/%s/%s", c.federationBaseURL(destServer), url.PathEscape(roomID), url.PathEscape(userID))
	resp, body, err := c.doFederationRequestContext(ctx, http.MethodGet, u, destServer, nil, 1<<20) //nolint:bodyclose // closed in doFederationRequest
	if err != nil {
		return nil, fmt.Errorf("make_leave request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &federationHTTPError{Status: resp.StatusCode, Message: string(body)}
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("failed to parse make_leave response: %w", err)
	}
	return out, nil
}

// SendLeaveContext delivers the signed leave event (v2, falling back to v1).
func (c *FederationClient) SendLeaveContext(ctx context.Context, destServer, roomID string, event map[string]any) error {
	eventID, _ := event["event_id"].(string)
	if eventID == "" {
		return errors.New("send_leave requires a computed event ID")
	}
	event = maps.Clone(event)
	if !strings.Contains(eventID, ":") {
		delete(event, "event_id")
	}
	var lastErr error
	for _, version := range []string{"v2", "v1"} {
		u := fmt.Sprintf("%s/_matrix/federation/%s/send_leave/%s/%s", c.federationBaseURL(destServer), version, url.PathEscape(roomID), url.PathEscape(eventID))
		resp, body, err := c.doFederationRequestContext(ctx, http.MethodPut, u, destServer, event, 1<<20) //nolint:bodyclose // closed in doFederationRequest
		if err != nil {
			return fmt.Errorf("send_leave request failed: %w", err)
		}
		if resp.StatusCode == http.StatusOK {
			return nil
		}
		lastErr = &federationHTTPError{Status: resp.StatusCode, Message: string(body)}
		// Only a missing/unsupported endpoint justifies trying the older one.
		if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMethodNotAllowed && resp.StatusCode != http.StatusNotImplemented {
			break
		}
		var matrixErr MatrixError
		if json.Unmarshal(body, &matrixErr) == nil && matrixErr.ErrCode != "" && matrixErr.ErrCode != "M_UNRECOGNIZED" {
			break // a real answer (e.g. M_NOT_FOUND room unknown), not a missing endpoint
		}
	}
	return lastErr
}

type leaveAttemptError struct {
	server string
	phase  string
	err    error
}

func (e leaveAttemptError) String() string { return fmt.Sprintf("%s %s: %v", e.server, e.phase, e.err) }

// remoteLeaveCandidates lists distinct, usable remote servers to try, best first.
func (r *RoomService) remoteLeaveCandidates(roomID string, member *MatrixRoomMember, room *MatrixRoom) []string {
	self := r.HS.Config.ServerName
	seen := map[string]bool{"": true, self: true}
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if seen[s] || !usableServerName(s) {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	if _, server, partial := roomPartialState(db.GetDB(), roomID); partial {
		add(server)
		for _, h := range partialRoomServerHints(roomID, maxPartialFanout()) {
			add(h)
		}
	}
	if member != nil && member.Membership == MembershipInvite && member.EventID != "" {
		var inv MatrixEvent
		if db.GetDB().Select("event_id", "sender").Where("event_id = ?", member.EventID).First(&inv).Error == nil {
			add(extractServerName(inv.Sender))
		}
	}
	if room != nil {
		add(extractServerName(room.Creator))
		if i := strings.Index(roomID, ":"); i > 0 {
			add(roomID[i+1:])
		}
	}
	var rows []MatrixRoomMember
	if err := db.GetDB().Select("user_id").Where("room_id = ? AND membership = ? AND user_id NOT LIKE ?", roomID, MembershipJoin, "%:"+likeEscape(self)).Limit(64).Find(&rows).Error; err == nil {
		for _, m := range rows {
			add(extractServerName(m.UserID))
		}
	}
	// Servers marked dead recently go last rather than first.
	live, dead := out[:0:0], []string{}
	for _, s := range out {
		if _, isDead := deadFederationHosts.active(s); isDead {
			dead = append(dead, s)
		} else {
			live = append(live, s)
		}
	}
	return append(live, dead...)
}

// leaveViaFederation performs make_leave + send_leave against a resident server
// and returns the signed leave PDU. It never persists anything.
func (r *RoomService) leaveViaFederation(ctx context.Context, userID, roomID string, candidates []string, reason string) (gmsl.PDU, string, []leaveAttemptError) {
	client := newFederationClient(r.HS)
	maxAttempts := envInt("MATRIX_LEAVE_ATTEMPTS", 4, 1, 20)
	var failures []leaveAttemptError
	for i, server := range candidates {
		if i >= maxAttempts || ctx.Err() != nil {
			break
		}
		fail := func(phase string, err error) {
			failures = append(failures, leaveAttemptError{server: server, phase: phase, err: err})
			if ctx.Err() == nil && isDeadHostError(err) {
				deadFederationHosts.mark(server, 2*time.Minute, err.Error())
			}
		}
		mlCtx, cancelML := context.WithTimeout(ctx, envDuration("MATRIX_LEAVE_MAKE_TIMEOUT", 8*time.Second))
		response, err := client.MakeLeaveContext(mlCtx, server, roomID, userID)
		cancelML()
		if err != nil {
			fail("make_leave", err)
			continue
		}
		version, _ := response["room_version"].(string)
		if version == "" {
			version = "1"
		}
		impl, err := gmsl.GetRoomVersion(gmsl.RoomVersion(version))
		if err != nil {
			fail("room_version", err)
			continue
		}
		raw, err := json.Marshal(response["event"])
		if err != nil {
			fail("template", err)
			continue
		}
		builder := impl.NewEventBuilder()
		if err = json.Unmarshal(raw, builder); err != nil {
			fail("template", err)
			continue
		}
		// The template is only trusted for prev/auth events and depth; identity
		// and content are ours.
		builder.RoomID, builder.SenderID, builder.Type, builder.StateKey = roomID, userID, "m.room.member", &userID
		content := map[string]any{"membership": "leave"}
		if reason != "" {
			content["reason"] = reason
		}
		if err = builder.SetContent(content); err != nil {
			fail("template", err)
			continue
		}
		p, err := builder.Build(time.Now(), spec.ServerName(r.HS.Config.ServerName), gmsl.KeyID(r.HS.keyID), r.HS.privateKey)
		if err != nil {
			fail("build", err)
			continue
		}
		wire, err := wirePDU(p)
		if err != nil {
			fail("build", err)
			continue
		}
		wire["event_id"] = p.EventID()
		slCtx, cancelSL := context.WithTimeout(ctx, envDuration("MATRIX_LEAVE_SEND_TIMEOUT", 10*time.Second))
		err = client.SendLeaveContext(slCtx, server, roomID, wire)
		cancelSL()
		if err != nil {
			fail("send_leave", err)
			continue
		}
		deadFederationHosts.clear(server)
		return p, server, failures
	}
	return nil, "", failures
}

// leaveFailureError turns the federation failures of a leave into a Matrix
// error: a remote refusal keeps its code, anything else is M_BAD_GATEWAY.
func leaveFailureError(failures []leaveAttemptError) *MatrixError {
	for _, f := range failures {
		var remote *federationHTTPError
		if errors.As(f.err, &remote) && (remote.Status == 403 || remote.Status == 404) {
			if m := federationJoinError(f.err); m.ErrCode == "M_FORBIDDEN" || m.ErrCode == "M_NOT_FOUND" {
				return m
			}
		}
	}
	return &MatrixError{ErrCode: "M_BAD_GATEWAY", Error: "No server in the room accepted the leave; try again later"}
}

func summariseLeaveFailures(failures []leaveAttemptError) string {
	parts := make([]string, 0, len(failures))
	for _, f := range failures {
		parts = append(parts, f.String())
	}
	return strings.Join(parts, "; ")
}

// LeaveRoomContext leaves roomID as userID, using federation for remote rooms.
func (r *RoomService) LeaveRoomContext(ctx context.Context, userID, roomID string, req *LeaveRoomRequest) *MatrixError {
	var member MatrixRoomMember
	if err := db.GetDB().Where("room_id = ? AND user_id = ?", roomID, userID).First(&member).Error; err != nil {
		return ErrNotFound
	}
	if member.Membership == MembershipLeave || member.Membership == MembershipBan {
		return nil
	}
	reason := ""
	if req != nil {
		reason = req.Reason
	}
	var room MatrixRoom
	roomErr := db.GetDB().Where("room_id = ?", roomID).First(&room).Error
	_, _, partial := roomPartialState(db.GetDB(), roomID)
	remoteRoom := partial
	if roomErr == nil && room.Creator != "" && extractServerName(room.Creator) != r.HS.Config.ServerName {
		remoteRoom = true
	}
	if member.Membership == MembershipInvite && roomErr != nil {
		remoteRoom = true // invite to a room we have no state for
	}
	if !remoteRoom {
		return r.leaveRoomLocally(userID, roomID, reason, member, "local")
	}

	budget := envDuration("MATRIX_LEAVE_TIMEOUT", 20*time.Second)
	fctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	candidates := r.remoteLeaveCandidates(roomID, &member, &room)
	started := time.Now()
	p, server, failures := r.leaveViaFederation(fctx, userID, roomID, candidates, reason)
	if p == nil {
		log.Warn("matrix leave room=%s user=%s phase=federation partial=%t candidates=%d elapsed=%s: %s",
			roomID, userID, partial, len(candidates), time.Since(started).Round(time.Millisecond), summariseLeaveFailures(failures))
		remoteErr := leaveFailureError(failures)
		// Invites, rooms nobody remembers (M_NOT_FOUND/M_FORBIDDEN from every
		// server) and ordinary rooms with a usable local DAG fall back to a local
		// departure. A partial-state room has no usable DAG: report the failure.
		switch {
		case member.Membership == MembershipInvite:
			return r.leaveRoomLocally(userID, roomID, reason, member, "invite-fallback")
		case remoteErr.ErrCode == "M_FORBIDDEN" || remoteErr.ErrCode == "M_NOT_FOUND":
			return r.leaveRoomLocally(userID, roomID, reason, member, "remote-refused")
		case !partial:
			if err := r.leaveRoomLocally(userID, roomID, reason, member, "federation-failed"); err == nil {
				return nil
			}
		}
		return remoteErr
	}
	event := pduToMatrixEvent(p)
	if err := r.persistMembershipDeparture(event); err != nil {
		log.Error("matrix leave room=%s user=%s phase=persist (remote leave accepted by %s): %v", roomID, userID, server, err)
		return &MatrixError{ErrCode: "M_UNKNOWN", Error: "The room accepted the leave but it could not be recorded; retry to complete it"}
	}
	r.finishLeftRoom(roomID)
	log.Info("matrix leave room=%s user=%s via %s ok in %s (partial=%t)", roomID, userID, server, time.Since(started).Round(time.Millisecond), partial)
	go r.HS.NotifyRoomEvent(roomID)
	return nil
}

// leaveRoomLocally records the departure with a locally built leave event and
// federates it. Errors are logged with their real cause; the client gets a
// Matrix error.
func (r *RoomService) leaveRoomLocally(userID, roomID, reason string, _ MatrixRoomMember, mode string) *MatrixError {
	content := map[string]any{"membership": "leave"}
	if reason != "" {
		content["reason"] = reason
	}
	event := &MatrixEvent{
		EventID:        "$" + uuid.New().String(),
		RoomID:         roomID,
		EventType:      EventTypeRoomMember,
		Sender:         userID,
		StateKey:       &userID,
		Content:        content,
		OriginServerTS: time.Now().UnixMilli(),
		SenderDomain:   r.HS.Config.ServerName,
	}
	if err := r.persistMembershipDeparture(event); err != nil {
		log.Error("matrix leave room=%s user=%s phase=local-persist mode=%s: %v", roomID, userID, mode, err)
		return ErrInternal
	}
	r.finishLeftRoom(roomID)
	go r.HS.NotifyRoomEvent(roomID)
	if r.HS.Event != nil && !isBridgeDepartureRoom(roomID) {
		go r.HS.Event.federateEvent(event)
	}
	return nil
}

// finishLeftRoom drops the state-sync machinery of a room the user left: the
// background job is cancelled, the partial marker cleared and queued inbound
// PDUs discarded. It is a no-op for ordinary rooms.
func (r *RoomService) finishLeftRoom(roomID string) {
	if _, _, partial := roomPartialState(db.GetDB(), roomID); !partial {
		return
	}
	asyncJoins.cancelRoom(roomID, 5*time.Second)
	if err := db.GetDB().Model(&MatrixRoom{}).Where("room_id = ? AND current_state_id LIKE ?", roomID, partialStatePrefix+"%").Update("current_state_id", nil).Error; err != nil {
		log.Warn("matrix leave room=%s: clearing partial-state marker failed: %v", roomID, err)
	}
	forgetPartialRoomServers(roomID)
	takePartialPDUs(roomID)
}
