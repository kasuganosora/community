package homeserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	gmsl "github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"gorm.io/gorm"
)

// Join response validation policy (modelled on Synapse and Dendrite)
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
}

func rawIsNull(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || bytes.Equal(t, []byte("null"))
}

func (r *RoomService) validateFederationJoin(ctx context.Context, roomID, joiningUserID, version string, localJoin json.RawMessage, resp *sendJoinResponse) (*preparedFederationJoin, error) {
	var returned any
	if !rawIsNull(resp.Event) {
		returned = resp.Event
	}
	join, err := verifiedFederationJoinContext(ctx, r.HS, version, roomID, joiningUserID, localJoin, returned)
	if err != nil {
		return nil, fmt.Errorf("join event: %w", err)
	}

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
			p, err := parseAndValidatePDU(version, raw)
			(*field.list)[i] = nil // release the raw copy as soon as it is parsed
			if err != nil {
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
	var walk func(id string)
	walk = func(id string) {
		if critical[id] {
			return
		}
		e, ok := events[id]
		if !ok {
			return
		}
		critical[id] = true
		for _, a := range e.AuthEventIDs() {
			walk(a)
		}
	}
	for _, id := range join.AuthEventIDs() {
		if _, ok := events[id]; !ok {
			return nil, fmt.Errorf("join auth event %s missing from the response", id)
		}
		walk(id)
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

	r.prefetchSigningKeys(ctx, events)

	// Signatures.
	droppedIDs := map[string]bool{}
	verifier := federationJSONVerifier{r.HS}
	var unverifiable, badSig int
	for id, p := range events {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := gmsl.VerifyEventSignatures(ctx, p, verifier, federationUserID); err != nil {
			if critical[id] {
				return nil, fmt.Errorf("required event %s (%s): %w", id, p.Type(), err)
			}
			if errors.Is(err, errKeyUnavailable) {
				unverifiable++
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

	// Authorisation against the surviving events. Required events must pass;
	// others are dropped, repeating a few rounds because dropping an event can
	// orphan events that cite it.
	for id := range critical {
		if err := checkPDUAuthorization(events[id], events); err != nil {
			return nil, fmt.Errorf("remote state authorization (%s): %w", id, err)
		}
	}
	unauthorised := 0
	for round := 0; round < 3; round++ {
		changed := false
		for id, p := range events {
			if critical[id] {
				continue
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if err := checkPDUAuthorization(p, events); err != nil {
				log.Debug("federation join %s: dropping unauthorised %s %s: %v", roomID, p.Type(), id, err)
				delete(events, id)
				delete(stateIDs, id)
				unauthorised++
				changed = true
			}
		}
		if !changed {
			break
		}
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
	if err = gmsl.Allowed(join, provider, federationUserID); err != nil {
		return nil, err
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
	return &preparedFederationJoin{roomID: roomID, version: version, events: events, state: state, join: join, creator: creator, dropped: dropped}, nil
}

// prefetchSigningKeys warms the key cache for every signing server in the
// response with a few concurrent fetches, so verification itself never
// serialises on slow or dead servers (each costs at most one failure).
func (r *RoomService) prefetchSigningKeys(ctx context.Context, events map[string]gmsl.PDU) {
	type want struct {
		keyID string
		at    spec.Timestamp
		check gmsl.SignatureValidityCheckFunc
	}
	wanted := map[string]want{}
	for _, p := range events {
		var env struct {
			Signatures map[string]map[string]json.RawMessage `json:"signatures"`
		}
		if json.Unmarshal(p.JSON(), &env) != nil {
			continue
		}
		impl, err := gmsl.GetRoomVersion(p.Version())
		if err != nil {
			continue
		}
		for server, keys := range env.Signatures {
			if server == r.HS.Config.ServerName {
				continue
			}
			if _, ok := wanted[server]; ok {
				continue
			}
			for keyID := range keys {
				wanted[server] = want{keyID: keyID, at: p.OriginServerTS(), check: impl.SignatureValidityCheck}
				break
			}
		}
	}
	servers := make([]string, 0, len(wanted))
	for s := range wanted {
		servers = append(servers, s)
	}
	sort.Strings(servers)
	jobs := make(chan string)
	var wg sync.WaitGroup
	verifier := federationJSONVerifier{r.HS}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for server := range jobs {
				w := wanted[server]
				if _, err := verifier.serverKeyContext(ctx, server, w.keyID, w.at, w.check); err != nil {
					log.Debug("federation join: prefetch key %s:%s: %v", server, w.keyID, err)
				}
			}
		}()
	}
	for _, s := range servers {
		select {
		case jobs <- s:
		case <-ctx.Done():
		}
	}
	close(jobs)
	wg.Wait()
}

func (r *RoomService) persistFederationJoin(pj *preparedFederationJoin) error {
	room := MatrixRoom{RoomID: pj.roomID, RoomVersion: pj.version, Creator: pj.creator}
	return db.GetDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("room_id = ?", pj.roomID).FirstOrCreate(&room).Error; err != nil {
			return err
		}
		for _, p := range pj.events {
			if err := persistVerifiedPDU(tx, r.HS, p, false); err != nil {
				return err
			}
		}
		// Auth-chain events are historical, not current state. Apply only the snapshot.
		for _, p := range pj.state {
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
