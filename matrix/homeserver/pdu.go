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
		if roomVersion == "11" {
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
	state, err := localStatePDUs(tx, event.RoomID)
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
	// Every event not referenced by another persisted event is a forward
	// extremity. Referencing all of them merges concurrent branches of the DAG.
	var history []MatrixEvent
	if err = tx.Select("event_id", "prev_events", "depth").Where("room_id = ?", event.RoomID).Find(&history).Error; err != nil {
		return err
	}
	leaves := make(map[string]int64, len(history))
	referenced := make(map[string]bool)
	for _, p := range history {
		leaves[p.EventID] = p.Depth
		for _, id := range p.PrevEvents {
			referenced[id] = true
		}
	}
	prev := make([]string, 0)
	depth := int64(1)
	for id, d := range leaves {
		if !referenced[id] {
			prev = append(prev, id)
			if d >= depth {
				depth = d + 1
			}
		}
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
	normalized.StateBefore = make([]string, 0, len(state))
	for _, p := range state {
		normalized.StateBefore = append(normalized.StateBefore, p.EventID())
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
	if string(p.Version()) == "11" {
		var content struct {
			Redacts string `json:"redacts"`
		}
		_ = json.Unmarshal(p.Content(), &content)
		return content.Redacts
	}
	return p.Redacts()
}
