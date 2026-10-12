package homeserver

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	gmsl "github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// parseAndValidatePDU uses the room-version implementation for canonical JSON,
// content hashes and reference IDs. The caller must also verify signatures and
// event authorization before accepting an untrusted PDU into the room.
func parseAndValidatePDU(roomVersion string, raw []byte) (gmsl.PDU, error) {
	version, err := gmsl.GetRoomVersion(gmsl.RoomVersion(roomVersion))
	if err != nil {
		return nil, err
	}
	pdu, err := version.NewEventFromUntrustedJSON(raw)
	if err != nil {
		return nil, err
	}
	if pdu.Redacted() {
		redacted, err := version.RedactEventJSON(raw)
		if err != nil {
			return nil, err
		}
		// Hash-mismatched full content is not an accepted substitute for a
		// redacted event. Legitimate redacted PDUs contain only redaction-safe
		// fields and can still pass their original server signature.
		var originalObj, redactedObj map[string]any
		if err = json.Unmarshal(raw, &originalObj); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(redacted, &redactedObj); err != nil {
			return nil, err
		}
		for _, key := range []string{"unsigned", "event_id", "outlier", "destinations", "age_ts"} {
			delete(originalObj, key)
			delete(redactedObj, key)
		}
		originalJSON, err := CanonicalJSON(originalObj)
		if err != nil {
			return nil, err
		}
		redactedJSON, err := CanonicalJSON(redactedObj)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(originalJSON, redactedJSON) {
			return nil, errors.New("event content hash mismatch")
		}
	}
	return pdu, nil
}

func pduToMatrixEvent(pdu gmsl.PDU) *MatrixEvent {
	event := &MatrixEvent{
		EventID: pdu.EventID(), RoomID: pdu.RoomID().String(), EventType: EventType(pdu.Type()),
		Sender: string(pdu.SenderID()), StateKey: pdu.StateKey(), PrevEvents: pdu.PrevEventIDs(), AuthEvents: pdu.AuthEventIDs(),
		Depth: pdu.Depth(), OriginServerTS: int64(pdu.OriginServerTS()), SenderDomain: extractServerName(string(pdu.SenderID())),
		FederationJSON: string(pdu.JSON()),
	}
	_ = json.Unmarshal(pdu.Content(), &event.Content)
	var fields struct {
		Hashes     map[string]string            `json:"hashes"`
		Signatures map[string]map[string]string `json:"signatures"`
	}
	_ = json.Unmarshal(pdu.JSON(), &fields)
	event.Hashes, event.Signatures = fields.Hashes, fields.Signatures
	if target := pduRedactionTarget(pdu); target != "" {
		event.Redacts = new(target)
	}
	return event
}

func matrixEventToPDU(event *MatrixEvent) (map[string]any, error) {
	if event.FederationJSON == "" {
		return nil, fmt.Errorf("event %s has no verifiable federation PDU", event.EventID)
	}
	var wire map[string]any
	if err := json.Unmarshal([]byte(event.FederationJSON), &wire); err != nil {
		return nil, err
	}
	return wire, nil
}

func localStatePDUs(tx *gorm.DB, roomID string) ([]gmsl.PDU, error) {
	var room MatrixRoom
	if err := tx.Where("room_id = ?", roomID).First(&room).Error; err != nil {
		return nil, err
	}
	var state []MatrixRoomState
	if err := tx.Where("room_id = ?", roomID).Find(&state).Error; err != nil {
		return nil, err
	}
	result := make([]gmsl.PDU, 0, len(state))
	for _, s := range state {
		var row MatrixEvent
		if err := tx.Where("event_id = ?", s.EventID).First(&row).Error; err != nil {
			return nil, err
		}
		if row.FederationJSON == "" {
			return nil, fmt.Errorf("room %s contains legacy event %s without a signed PDU", roomID, row.EventID)
		}
		pdu, err := parseAndValidatePDU(room.RoomVersion, []byte(row.FederationJSON))
		if err != nil {
			return nil, err
		}
		result = append(result, pdu)
	}
	return result, nil
}

func matrixUserIDForSender(_ spec.RoomID, sender spec.SenderID) (*spec.UserID, error) {
	return spec.NewUserID(string(sender), true)
}

// buildLocalPDU creates an authorized, hashed and signed event from the current
// room state. Call on the room's write transaction so its DAG cannot change
// between collecting auth/prev events and persisting this event.
func buildLocalPDU(tx *gorm.DB, hs *HomeServer, event *MatrixEvent, roomVersion string) error {
	if hs == nil || len(hs.privateKey) != ed25519.PrivateKeySize {
		return errors.New("local Matrix signing key unavailable")
	}
	version, err := gmsl.GetRoomVersion(gmsl.RoomVersion(roomVersion))
	if err != nil {
		return err
	}
	builder := version.NewEventBuilder()
	builder.RoomID, event.SenderDomain = event.RoomID, hs.Config.ServerName
	builder.SenderID, builder.Type, builder.StateKey = event.Sender, string(event.EventType), event.StateKey
	if event.Redacts != nil {
		if redactionTargetInContent(version) {
			content := make(map[string]any, len(event.Content)+1)
			for k, v := range event.Content {
				content[k] = v
			}
			content["redacts"] = *event.Redacts
			event.Content = content
		} else {
			builder.Redacts = *event.Redacts
		}
	}
	if err = builder.SetContent(event.Content); err != nil {
		return err
	}
	// Only the state this event is authorised by is loaded and parsed: create,
	// power levels, join rules and the relevant member events. Loading and parsing
	// the whole current state made every send in a 16k-event room cost thousands of
	// queries. The ID list of the full state is still recorded as the event's
	// pre-event snapshot (IDs only, one query).
	needed, err := gmsl.StateNeededForProtoEvent(&gmsl.ProtoEvent{
		Type: builder.Type, StateKey: builder.StateKey, Content: builder.Content, SenderID: builder.SenderID, Version: version,
	})
	if err != nil {
		return err
	}
	state, err := localAuthStatePDUs(tx, event.RoomID, roomVersion, needed.Tuples())
	if err != nil {
		return err
	}
	auth, err := gmsl.NewAuthEvents(state)
	if err != nil {
		return err
	}
	if err = builder.AddAuthEvents(auth); err != nil {
		return err
	}
	var stateIDs []string
	if err = tx.Model(&MatrixRoomState{}).Where("room_id = ?", event.RoomID).Pluck("event_id", &stateIDs).Error; err != nil {
		return err
	}
	// Forward extremities: events no stored event references. The DAG of a room
	// joined through partial state has thousands of unreferenced stubs (state
	// events whose prev_events were never stored), and a federated peer rejects
	// more than 20 prev_events, so only the newest events are considered and at
	// most 20 extremities (highest depth first) are referenced. This also keeps the
	// query bounded for big rooms (served by idx_matrix_events_room_ts).
	var history []MatrixEvent
	if err = tx.Select("event_id", "prev_events", "depth").Where("room_id = ?", event.RoomID).Order("origin_server_ts DESC, event_id DESC").Limit(recentExtremityWindow).Find(&history).Error; err != nil {
		return err
	}
	leaves := make(map[string]int64, len(history))
	referenced := make(map[string]bool)
	var maxDepth int64
	for _, p := range history {
		leaves[p.EventID] = p.Depth
		maxDepth = max(maxDepth, p.Depth)
		for _, id := range p.PrevEvents {
			referenced[id] = true
		}
	}
	prev := make([]string, 0)
	for id := range leaves {
		if !referenced[id] {
			prev = append(prev, id)
		}
	}
	if len(prev) == 0 && len(history) > 0 {
		prev = append(prev, history[0].EventID)
	}
	sort.Slice(prev, func(i, j int) bool {
		if leaves[prev[i]] != leaves[prev[j]] {
			return leaves[prev[i]] > leaves[prev[j]]
		}
		return prev[i] < prev[j]
	})
	if len(prev) > maxPrevEvents {
		prev = prev[:maxPrevEvents]
	}
	depth := int64(1)
	if len(history) > 0 {
		depth = maxDepth + 1
	}
	sort.Strings(prev)
	builder.PrevEvents, builder.Depth = prev, depth
	if event.OriginServerTS == 0 {
		event.OriginServerTS = time.Now().UnixMilli()
	}
	pdu, err := builder.Build(time.UnixMilli(event.OriginServerTS), spec.ServerName(hs.Config.ServerName), gmsl.KeyID(hs.keyID), hs.privateKey)
	if err != nil {
		return err
	}
	if err = gmsl.Allowed(pdu, auth, matrixUserIDForSender); err != nil {
		return fmt.Errorf("authorize local %s: %w", event.EventType, err)
	}
	normalized := pduToMatrixEvent(pdu)
	normalized.StateSnapshotKnown = true
	normalized.StateBefore = stateIDs
	if normalized.StateBefore == nil {
		normalized.StateBefore = []string{}
	}
	sort.Strings(normalized.StateBefore)
	// Client idempotency metadata is deliberately excluded from the signed PDU.
	normalized.TxnID, normalized.SenderDeviceID, normalized.CreatedAt = event.TxnID, event.SenderDeviceID, event.CreatedAt
	normalized.TxnKey = event.TxnKey
	*event = *normalized
	return nil
}

// preparePersistedLocalPDU upgrades newly created local rooms to signed
// DAGs. Existing legacy rooms remain usable locally; fabricating signatures for
// their historical UUID events would misrepresent their identities.
func preparePersistedLocalPDU(tx *gorm.DB, event *MatrixEvent) error {
	if event.FederationJSON != "" {
		return nil
	}
	hs := GlobalHS
	if signer, ok := tx.Get("matrix:signer"); ok {
		hs, _ = signer.(*HomeServer)
	}
	if hs == nil || hs.Config == nil || len(hs.privateKey) != ed25519.PrivateKeySize || extractServerName(event.Sender) != hs.Config.ServerName {
		return nil
	}
	var room MatrixRoom
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("room_id = ?", event.RoomID).First(&room).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if _, err := gmsl.GetRoomVersion(gmsl.RoomVersion(room.RoomVersion)); err != nil {
		return nil
	}
	if event.EventType != EventTypeRoomCreate {
		var create MatrixEvent
		err := tx.Where("room_id = ? AND event_type = ?", event.RoomID, EventTypeRoomCreate).First(&create).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if create.FederationJSON == "" {
			return captureLegacyLocalState(tx, event, &room)
		}
	}
	return buildLocalPDU(tx, hs, event, room.RoomVersion)
}

// Room v11 moved the redaction target into content; the library's Redacts
// accessor still exposes the legacy top-level field.
func pduRedactionTarget(p gmsl.PDU) string {
	version, err := gmsl.GetRoomVersion(p.Version())
	if err == nil && redactionTargetInContent(version) {
		var content struct {
			Redacts string `json:"redacts"`
		}
		_ = json.Unmarshal(p.Content(), &content)
		return content.Redacts
	}
	return p.Redacts()
}

// Ask the room version's redaction algorithm which field is protected rather
// than assuming all later room versions have the legacy top-level field.
func redactionTargetInContent(version gmsl.IRoomVersion) bool {
	raw, err := version.RedactEventJSON([]byte(`{"type":"m.room.redaction","content":{"redacts":"$target"}}`))
	if err != nil {
		return false
	}
	var event struct {
		Content struct {
			Redacts string `json:"redacts"`
		} `json:"content"`
	}
	return json.Unmarshal(raw, &event) == nil && event.Content.Redacts == "$target"
}

// recentExtremityWindow is how many of the newest events are examined to find a
// new event's prev_events; maxPrevEvents is the federation limit on them.
const (
	recentExtremityWindow = 256
	maxPrevEvents         = 20
)

// localAuthStatePDUs returns the current-state PDUs for the given (type,
// state_key) tuples that exist in the room.
func localAuthStatePDUs(tx *gorm.DB, roomID, version string, tuples []gmsl.StateKeyTuple) ([]gmsl.PDU, error) {
	if len(tuples) == 0 {
		return nil, nil
	}
	query := tx.Model(&MatrixRoomState{}).Where("room_id = ?", roomID)
	cond, args := "", []any{}
	for i, t := range tuples {
		if i > 0 {
			cond += " OR "
		}
		cond += "(event_type = ? AND state_key = ?)"
		args = append(args, t.EventType, t.StateKey)
	}
	var rows []MatrixRoomState
	if err := query.Where("("+cond+")", args...).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]gmsl.PDU, 0, len(rows))
	for _, s := range rows {
		var row MatrixEvent
		if err := tx.Where("event_id = ?", s.EventID).First(&row).Error; err != nil {
			return nil, fmt.Errorf("state event %s (%s/%s): %w", s.EventID, s.EventType, s.StateKey, err)
		}
		if row.FederationJSON == "" {
			return nil, fmt.Errorf("room %s contains legacy event %s without a signed PDU", roomID, row.EventID)
		}
		pdu, err := parseAndValidatePDU(version, []byte(row.FederationJSON))
		if err != nil {
			return nil, fmt.Errorf("state event %s: %w", row.EventID, err)
		}
		out = append(out, pdu)
	}
	return out, nil
}
