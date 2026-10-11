package homeserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	gmsl "github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// federationJSONVerifier verifies exactly the key named by the signature. A key
// document is fetched over verified TLS and its self-signature is checked before
// any of its keys enter the cache. No request can choose a different destination.
type federationJSONVerifier struct{ hs *HomeServer }

func federationUserID(_ spec.RoomID, sender spec.SenderID) (*spec.UserID, error) {
	return spec.NewUserID(string(sender), true)
}

func requireFederationAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		hs := GetHomeServer()
		reject := func(msg string) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, MatrixError{ErrCode: "M_UNAUTHORIZED", Error: msg})
		}
		header := c.GetHeader("Authorization")
		if !strings.HasPrefix(header, "X-Matrix ") {
			reject("Matrix request signature required")
			return
		}
		origin, destination, keyID, sig, err := parseXMatrixAuth(header)
		if err != nil || destination != hs.Config.ServerName {
			reject("Invalid federation destination or authorization")
			return
		}
		key, err := (federationJSONVerifier{hs}).serverKey(origin, keyID, time.Now())
		if err != nil || !verifyRequestSignature(c.Request, origin, destination, keyID, sig, key) {
			reject("Invalid request signature")
			return
		}
		c.Set("origin", origin)
		c.Next()
	}
}

func federationRoomVersion(roomID string) (string, error) {
	var room MatrixRoom
	if err := db.GetDB().Where("room_id = ?", roomID).First(&room).Error; err != nil {
		return "", err
	}
	if _, _, partial := parsePartialStateMarker(room.CurrentStateID); partial {
		return "", errRoomPartialState
	}
	if room.RoomVersion == "" {
		return "10", nil
	}
	return room.RoomVersion, nil
}

func eventPDUFromDB(tx *gorm.DB, id, version string) (gmsl.PDU, error) {
	var event MatrixEvent
	if err := tx.Where("event_id = ?", id).First(&event).Error; err != nil {
		return nil, err
	}
	wire, err := matrixEventToPDU(&event)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(wire)
	if err != nil {
		return nil, err
	}
	return parseAndValidatePDU(version, raw)
}

func currentStatePDUs(tx *gorm.DB, roomID, version string) ([]gmsl.PDU, error) {
	var entries []MatrixRoomState
	if err := tx.Where("room_id = ?", roomID).Find(&entries).Error; err != nil {
		return nil, err
	}
	out := make([]gmsl.PDU, 0, len(entries))
	for _, entry := range entries {
		p, err := eventPDUFromDB(tx, entry.EventID, version)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func checkPDUAuthorization(p gmsl.PDU, events map[string]gmsl.PDU) error {
	auth := make([]gmsl.PDU, 0, len(p.AuthEventIDs()))
	for _, id := range p.AuthEventIDs() {
		e, ok := events[id]
		if !ok {
			return fmt.Errorf("missing auth event %s", id)
		}
		if e.RoomID() != p.RoomID() {
			return errors.New("auth event belongs to another room")
		}
		auth = append(auth, e)
	}
	provider, err := gmsl.NewAuthEvents(auth)
	if err != nil {
		return err
	}
	return gmsl.Allowed(p, provider, federationUserID)
}

func validateIncomingPDU(ctx context.Context, hs *HomeServer, p gmsl.PDU, againstCurrent bool) error {
	if err := gmsl.VerifyEventSignatures(ctx, p, federationJSONVerifier{hs}, federationUserID); err != nil {
		return err
	}
	events := make(map[string]gmsl.PDU)
	for _, id := range p.AuthEventIDs() {
		e, err := eventPDUFromDB(db.GetDB(), id, string(p.Version()))
		if err != nil {
			return fmt.Errorf("auth event %s: %w", id, err)
		}
		events[id] = e
	}
	if err := checkPDUAuthorization(p, events); err != nil {
		return err
	}
	if againstCurrent {
		state, err := currentStatePDUs(db.GetDB(), p.RoomID().String(), string(p.Version()))
		if err != nil {
			return err
		}
		provider, err := gmsl.NewAuthEvents(state)
		if err != nil {
			return err
		}
		return gmsl.Allowed(p, provider, federationUserID)
	}
	return nil
}

func wirePDU(p gmsl.PDU) (map[string]any, error) {
	var out map[string]any
	err := json.Unmarshal(p.JSON(), &out)
	return out, err
}

// persistVerifiedPDU is called only after signature and room authorization checks.
func persistVerifiedPDU(tx *gorm.DB, hs *HomeServer, p gmsl.PDU, applyState bool) error {
	state, err := stateBeforePDU(tx, p)
	if err != nil && applyState {
		return err
	}
	return persistVerifiedPDUAtState(tx, hs, p, applyState, state, err == nil)
}
func persistVerifiedPDUAtState(tx *gorm.DB, hs *HomeServer, p gmsl.PDU, applyState bool, stateBefore []gmsl.PDU, snapshotKnown bool) error {
	event := pduToMatrixEvent(p)
	event.StateSnapshotKnown = snapshotKnown
	if snapshotKnown {
		if applyState {
			auth, err := gmsl.NewAuthEvents(stateBefore)
			if err != nil {
				return err
			}
			if err = gmsl.Allowed(p, auth, federationUserID); err != nil {
				return fmt.Errorf("event authorization against prior state: %w", err)
			}
		}
		event.StateBefore = make([]string, 0, len(stateBefore))
		for _, e := range stateBefore {
			event.StateBefore = append(event.StateBefore, e.EventID())
		}
	}

	if applyState {
		var room MatrixRoom
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("room_id = ?", event.RoomID).First(&room).Error; err != nil {
			return err
		}
		state, err := currentStatePDUs(tx, event.RoomID, string(p.Version()))
		if err != nil {
			return err
		}
		auth, err := gmsl.NewAuthEvents(state)
		if err != nil {
			return err
		}
		if err = gmsl.Allowed(p, auth, federationUserID); err != nil {
			return err
		}
	}
	var existing MatrixEvent
	if err := tx.Where("event_id = ?", event.EventID).First(&existing).Error; err == nil {
		return nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if err := tx.Create(event).Error; err != nil {
		return err
	}
	if event.StateKey == nil {
		if err := hs.queueOutboundDirect(tx, event); err != nil {
			return err
		}
	}
	if err := applyAuthorizedFederationRedaction(tx, hs, p); err != nil {
		return err
	}
	var priorRedactions []MatrixEvent
	if err := tx.Where("room_id = ? AND redacts = ?", event.RoomID, event.EventID).Find(&priorRedactions).Error; err != nil {
		return err
	}
	for _, redaction := range priorRedactions {
		if redaction.FederationJSON == "" {
			continue
		}
		rp, err := parseAndValidatePDU(string(p.Version()), []byte(redaction.FederationJSON))
		if err != nil {
			return err
		}
		if err = applyAuthorizedFederationRedaction(tx, hs, rp); err != nil {
			return err
		}
	}

	if applyState && event.StateKey != nil {
		hs.Room.updateRoomState(tx, event.RoomID, event)
		if event.EventType == EventTypeRoomMember {
			membership, _ := event.Content["membership"].(string)
			hs.Room.updateRoomMember(tx, event.RoomID, *event.StateKey, MembershipState(membership), event.EventID)
			if err := hs.syncBridgeMembershipDeparture(tx, event); err != nil {
				return err
			}
		}
	}
	return nil
}

func requireFederationRoomAccess(c *gin.Context, roomID string) bool {
	if _, _, partial := roomPartialState(db.GetDB(), roomID); partial {
		federationFailure(c, 404, errRoomPartialState)
		return false
	}
	origin := c.GetString("origin")
	var members []MatrixRoomMember
	if origin != "" && db.GetDB().Where("room_id = ? AND membership = ?", roomID, MembershipJoin).Find(&members).Error == nil {
		for _, member := range members {
			if extractServerName(member.UserID) == origin {
				return true
			}
		}
	}
	federationFailure(c, 403, fmt.Errorf("authenticated server has no joined member in room"))
	return false
}

func federationUserJoined(roomID, userID string) bool {
	var count int64
	return db.GetDB().Model(&MatrixRoomMember{}).Where("room_id = ? AND user_id = ? AND membership = ?", roomID, userID, MembershipJoin).Count(&count).Error == nil && count > 0
}

func applyAuthorizedFederationRedaction(tx *gorm.DB, hs *HomeServer, p gmsl.PDU) error {
	if p.Type() != "m.room.redaction" || pduRedactionTarget(p) == "" {
		return nil
	}
	var target MatrixEvent
	if err := tx.Where("event_id = ? AND room_id = ?", pduRedactionTarget(p), p.RoomID().String()).First(&target).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	allowed := target.Sender == string(p.SenderID())
	if !allowed {
		for _, id := range p.AuthEventIDs() {
			a, err := eventPDUFromDB(tx, id, string(p.Version()))
			if err != nil {
				return err
			}
			if privilegedEventCreator(p, a) {
				allowed = true
				break
			}
			if a.Type() == "m.room.power_levels" {
				pl, err := gmsl.NewPowerLevelContentFromEvent(a)
				if err != nil {
					return err
				}
				allowed = pl.UserLevel(p.SenderID()) >= pl.Redact
			}
		}
	}
	// Valid redaction events can enter the DAG without permission to redact this target.
	if allowed {
		if target.Sender == string(p.SenderID()) {
			targetID := pduRedactionTarget(p)
			redaction := MatrixEvent{EventID: p.EventID(), RoomID: p.RoomID().String(), Sender: string(p.SenderID()), EventType: EventTypeRoomRedaction, Redacts: &targetID}
			if err := hs.queueDirectBridgeRedaction(tx, &redaction); err != nil {
				return err
			}
		}
		return applyStoredRedactionTx(tx, p.RoomID().String(), pduRedactionTarget(p))
	}
	return nil
}
