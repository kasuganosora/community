package homeserver

import (
	"errors"
	"fmt"
	"sort"

	gmsl "github.com/matrix-org/gomatrixserverlib"
	"gorm.io/gorm"
)

const historicalStateMaxDepth = 512
const historicalStateMaxEvents = 10000
const historicalStateMaxPredecessors = 20

type historicalStateTuple struct{ eventType, stateKey string }
type historicalStateRecord struct {
	row *MatrixEvent
	pdu gmsl.PDU
}
type historicalStateReader struct {
	tx      *gorm.DB
	roomID  string
	version gmsl.RoomVersion
	records map[string]historicalStateRecord
	before  map[string][]gmsl.PDU
	active  map[string]bool
	// absent lists event IDs a caller already proved are not stored; load
	// answers them without a query.
	absent map[string]bool
}

// stateBeforePDU returns the historical state immediately before p. A persisted
// snapshot is authoritative; otherwise each prev event contributes its own
// historical state plus its state change. Neither current room state nor wall
// clock timestamps can substitute for a missing part of the event DAG.
func stateBeforePDU(tx *gorm.DB, p gmsl.PDU) ([]gmsl.PDU, error) {
	return stateBeforePDUHinted(tx, p, nil)
}

func stateBeforePDUHinted(tx *gorm.DB, p gmsl.PDU, absent map[string]bool) ([]gmsl.PDU, error) {
	if tx == nil || p == nil {
		return nil, errors.New("historical state requires a transaction and PDU")
	}
	if _, err := gmsl.GetRoomVersion(p.Version()); err != nil {
		return nil, err
	}
	r := &historicalStateReader{tx: tx, roomID: p.RoomID().String(), version: p.Version(), records: map[string]historicalStateRecord{}, before: map[string][]gmsl.PDU{}, active: map[string]bool{}, absent: absent}
	// Incoming PDUs need not yet exist in storage. Existing rows must describe
	// the same room and verifiable event identity as the supplied PDU.
	row, err := r.load(p.EventID())
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		r.records[p.EventID()] = historicalStateRecord{pdu: p}
	} else if row.pdu.RoomID() != p.RoomID() || row.pdu.EventID() != p.EventID() {
		return nil, errors.New("historical event identity mismatch")
	}
	return r.stateBefore(p, 0)
}

func (r *historicalStateReader) load(id string) (historicalStateRecord, error) {
	if err := r.tx.Statement.Context.Err(); err != nil {
		return historicalStateRecord{}, err
	}
	if record, ok := r.records[id]; ok {
		return record, nil
	}
	if len(r.records) >= historicalStateMaxEvents {
		return historicalStateRecord{}, errors.New("historical state exceeds event limit")
	}
	if r.absent[id] {
		return historicalStateRecord{}, fmt.Errorf("historical event %s: %w", id, gorm.ErrRecordNotFound)
	}
	var row MatrixEvent
	if err := r.tx.Where("event_id = ?", id).First(&row).Error; err != nil {
		return historicalStateRecord{}, fmt.Errorf("historical event %s: %w", id, err)
	}
	if row.RoomID != r.roomID {
		return historicalStateRecord{}, fmt.Errorf("historical event %s belongs to another room", id)
	}
	if row.FederationJSON == "" {
		return historicalStateRecord{}, fmt.Errorf("historical event %s has no verifiable PDU", id)
	}
	p, err := parseAndValidatePDU(string(r.version), []byte(row.FederationJSON))
	if err != nil {
		return historicalStateRecord{}, fmt.Errorf("historical event %s: %w", id, err)
	}
	if p.EventID() != id || p.RoomID().String() != r.roomID {
		return historicalStateRecord{}, fmt.Errorf("historical event %s has inconsistent room or event identity", id)
	}
	record := historicalStateRecord{row: &row, pdu: p}
	r.records[id] = record
	return record, nil
}

func (r *historicalStateReader) stateBefore(p gmsl.PDU, depth int) ([]gmsl.PDU, error) {
	if depth > historicalStateMaxDepth {
		return nil, errors.New("historical state exceeds traversal depth")
	}
	id := p.EventID()
	if r.active[id] {
		return nil, fmt.Errorf("cycle in historical event DAG at %s", id)
	}
	if state, ok := r.before[id]; ok {
		return state, nil
	}
	r.active[id] = true
	defer delete(r.active, id)
	record, err := r.load(id)
	if err != nil {
		return nil, err
	}
	if record.row != nil && record.row.StateSnapshotKnown {
		if len(record.row.StateBefore) > historicalStateMaxEvents {
			return nil, errors.New("historical snapshot exceeds event limit")
		}
		state := make([]gmsl.PDU, 0, len(record.row.StateBefore))
		for _, stateID := range record.row.StateBefore {
			if stateID == id {
				return nil, fmt.Errorf("event %s occurs in its own before-state", id)
			}
			entry, err := r.load(stateID)
			if err != nil {
				return nil, err
			}
			state = append(state, entry.pdu)
		}
		state, err = validateHistoricalState(state, p)
		if err != nil {
			return nil, err
		}
		r.before[id] = state
		return state, nil
	}
	prevIDs := p.PrevEventIDs()
	if len(prevIDs) > historicalStateMaxPredecessors {
		return nil, errors.New("historical event exceeds predecessor limit")
	}
	if len(prevIDs) == 0 {
		if p.Type() != "m.room.create" || p.StateKey() == nil || *p.StateKey() != "" || len(p.AuthEventIDs()) != 0 {
			return nil, fmt.Errorf("non-create event %s has no historical predecessors", id)
		}
		r.before[id] = []gmsl.PDU{}
		return r.before[id], nil
	}
	states := make([][]gmsl.PDU, 0, len(prevIDs))
	seenPrev := map[string]bool{}
	for _, prevID := range prevIDs {
		if seenPrev[prevID] {
			return nil, fmt.Errorf("duplicate predecessor %s", prevID)
		}
		seenPrev[prevID] = true
		prev, err := r.load(prevID)
		if err != nil {
			return nil, err
		}
		before, err := r.stateBefore(prev.pdu, depth+1)
		if err != nil {
			return nil, err
		}
		indexed := make(map[historicalStateTuple]gmsl.PDU, len(before)+1)
		for _, state := range before {
			indexed[historicalStateTuple{state.Type(), *state.StateKey()}] = state
		}
		if prev.pdu.StateKey() != nil {
			indexed[historicalStateTuple{prev.pdu.Type(), *prev.pdu.StateKey()}] = prev.pdu
		}
		state := make([]gmsl.PDU, 0, len(indexed))
		for _, event := range indexed {
			state = append(state, event)
		}
		states = append(states, state)
	}
	var result []gmsl.PDU
	if len(states) == 1 {
		result = states[0]
	} else {
		auth, err := r.authChain(states)
		if err != nil {
			return nil, err
		}
		// Rejected events are never persisted as accepted DAG/state records.
		result, err = gmsl.ResolveConflictsNew(r.version, states, auth, matrixUserIDForSender, func(string) bool { return false })
		if err != nil {
			return nil, fmt.Errorf("resolve historical state for %s: %w", id, err)
		}
	}
	result, err = validateHistoricalState(result, p)
	if err != nil {
		return nil, err
	}
	r.before[id] = result
	return result, nil
}

// validateHistoricalState enforces a complete, unambiguous state map. Empty
// snapshots are meaningful only before the create event, never missing-state
// stand-ins for ordinary events.
func validateHistoricalState(state []gmsl.PDU, target gmsl.PDU) ([]gmsl.PDU, error) {
	seen := map[historicalStateTuple]bool{}
	hasCreate := false
	for _, event := range state {
		if event.RoomID() != target.RoomID() {
			return nil, errors.New("historical state belongs to another room")
		}
		if event.StateKey() == nil {
			return nil, fmt.Errorf("historical state includes non-state event %s", event.EventID())
		}
		key := historicalStateTuple{event.Type(), *event.StateKey()}
		if seen[key] {
			return nil, fmt.Errorf("historical snapshot contains conflicting tuple %s/%s", key.eventType, key.stateKey)
		}
		seen[key] = true
		if key.eventType == "m.room.create" && key.stateKey == "" {
			hasCreate = true
		}
	}
	if target.Type() == "m.room.create" && len(target.PrevEventIDs()) == 0 {
		if len(state) != 0 {
			return nil, errors.New("create event cannot have preceding room state")
		}
	} else if !hasCreate {
		return nil, errors.New("historical state is missing the create event")
	}
	sort.Slice(state, func(i, j int) bool {
		if state[i].Type() != state[j].Type() {
			return state[i].Type() < state[j].Type()
		}
		return *state[i].StateKey() < *state[j].StateKey()
	})
	return state, nil
}

// authChain collects branch state roots and every transitive auth dependency.
// State resolution v2.1 indexes conflicted roots through the same auth-event
// map while constructing its conflicted subgraph, so roots must be included.
// Missing or cross-room records and cycles fail closed before resolution.
func (r *historicalStateReader) authChain(states [][]gmsl.PDU) ([]gmsl.PDU, error) {
	seen := map[string]bool{}
	active := map[string]bool{}
	events := map[string]gmsl.PDU{}
	var walk func(gmsl.PDU, int) error
	walk = func(p gmsl.PDU, depth int) error {
		if depth > historicalStateMaxDepth {
			return errors.New("historical auth chain exceeds traversal depth")
		}
		if active[p.EventID()] {
			return fmt.Errorf("cycle in historical auth chain at %s", p.EventID())
		}
		if seen[p.EventID()] {
			return nil
		}
		active[p.EventID()] = true
		defer delete(active, p.EventID())
		for _, id := range p.AuthEventIDs() {
			record, err := r.load(id)
			if err != nil {
				return err
			}
			if record.pdu.StateKey() == nil {
				return fmt.Errorf("auth event %s is not a state event", id)
			}
			events[id] = record.pdu
			if err = walk(record.pdu, depth+1); err != nil {
				return err
			}
		}
		seen[p.EventID()] = true
		return nil
	}
	for _, state := range states {
		for _, p := range state {
			events[p.EventID()] = p
			if err := walk(p, 0); err != nil {
				return nil, err
			}
		}
	}
	ids := make([]string, 0, len(events))
	for id := range events {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]gmsl.PDU, 0, len(ids))
	for _, id := range ids {
		out = append(out, events[id])
	}
	return out, nil
}
