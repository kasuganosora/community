package homeserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/kasuganosora/community/log"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	gmsl "github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"gorm.io/gorm"
)

type SendJoinResponse struct {
	Origin         string         `json:"origin"`
	State          []gin.H        `json:"state"`
	AuthChain      []gin.H        `json:"auth_chain"`
	Event          map[string]any `json:"event,omitempty"`
	MembersOmitted bool           `json:"members_omitted"`
}

// federationFailure writes a Matrix error. A missing database record is never a
// server fault: it is reported as 404 M_NOT_FOUND instead of 500.
func federationFailure(c *gin.Context, status int, err error) {
	if status >= 500 && errors.Is(err, gorm.ErrRecordNotFound) {
		status = http.StatusNotFound
	}
	code := "M_UNKNOWN"
	switch status {
	case http.StatusBadRequest:
		code = "M_BAD_JSON"
	case http.StatusForbidden:
		code = "M_FORBIDDEN"
	case http.StatusNotFound:
		code = "M_NOT_FOUND"
	}
	msg := err.Error()
	if status >= 500 {
		log.Warn("federation %s %s failed: %v", c.Request.Method, c.Request.URL.Path, err)
		msg = "internal server error"
	}
	c.JSON(status, MatrixError{ErrCode: code, Error: msg})
}

func handleMakeJoin(c *gin.Context) {
	hs := GetHomeServer()
	roomID, userID := c.Param("roomId"), c.Param("userId")
	if extractServerName(userID) != c.GetString("origin") {
		federationFailure(c, 403, fmt.Errorf("user does not belong to authenticated origin"))
		return
	}
	version, err := federationRoomVersion(roomID)
	if err != nil {
		federationFailure(c, 404, err)
		return
	}
	compatible := false
	for _, v := range c.QueryArray("ver") {
		if v == version {
			compatible = true
		}
	}
	if !compatible {
		c.JSON(400, gin.H{"errcode": "M_INCOMPATIBLE_ROOM_VERSION", "room_version": version, "error": "Room version not supported"})
		return
	}
	event := &MatrixEvent{RoomID: roomID, Sender: userID, StateKey: &userID, EventType: EventTypeRoomMember, Content: map[string]any{"membership": "join"}}
	if err = buildLocalPDU(db.GetDB(), hs, event, version); err != nil {
		federationFailure(c, 403, err)
		return
	}
	wire, err := matrixEventToPDU(event)
	if err != nil {
		federationFailure(c, 500, err)
		return
	}
	delete(wire, "signatures")
	delete(wire, "hashes")
	c.JSON(200, MakeJoinResponse{Origin: hs.Config.ServerName, OriginServerTS: time.Now().UnixMilli(), Event: wire, RoomVersion: version})
}

func handleSendJoin(c *gin.Context)   { handleVerifiedSendJoin(c, false) }
func handleSendJoinV1(c *gin.Context) { handleVerifiedSendJoin(c, true) }
func handleVerifiedSendJoin(c *gin.Context, v1 bool) {
	hs := GetHomeServer()
	roomID := c.Param("roomId")
	version, err := federationRoomVersion(roomID)
	if err != nil {
		federationFailure(c, 404, err)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil {
		federationFailure(c, 400, err)
		return
	}
	p, err := parseAndValidatePDU(version, raw)
	if err != nil {
		federationFailure(c, 400, err)
		return
	}
	membership, _ := p.Membership()
	if p.RoomID().String() != roomID || p.EventID() != c.Param("eventId") || p.Type() != "m.room.member" || p.StateKey() == nil || *p.StateKey() != string(p.SenderID()) || membership != "join" || extractServerName(string(p.SenderID())) != c.GetString("origin") {
		federationFailure(c, 403, fmt.Errorf("join event identity mismatch"))
		return
	}
	if err = validateIncomingPDU(c.Request.Context(), hs, p, true); err != nil {
		federationFailure(c, 403, err)
		return
	}
	state, err := currentStatePDUs(db.GetDB(), roomID, version)
	if err != nil {
		federationFailure(c, 500, err)
		return
	}
	auth, err := completeAuthChain(db.GetDB(), state, version)
	if err != nil {
		federationFailure(c, 500, err)
		return
	}
	response := SendJoinResponse{Origin: hs.Config.ServerName, State: []gin.H{}, AuthChain: []gin.H{}}
	for _, e := range state {
		w, _ := wirePDU(e)
		response.State = append(response.State, w)
	}
	for _, e := range auth {
		w, _ := wirePDU(e)
		response.AuthChain = append(response.AuthChain, w)
	}
	response.Event, _ = wirePDU(p)
	if err = db.GetDB().Transaction(func(tx *gorm.DB) error { return persistVerifiedPDU(tx, hs, p, true) }); err != nil {
		federationFailure(c, 500, err)
		return
	}
	go hs.NotifyRoomEvent(roomID)
	if v1 {
		c.JSON(http.StatusOK, []any{200, response})
	} else {
		c.JSON(http.StatusOK, response)
	}
}

func completeAuthChain(tx *gorm.DB, state []gmsl.PDU, version string) ([]gmsl.PDU, error) {
	seen := map[string]bool{}
	queue := append([]gmsl.PDU{}, state...)
	out := []gmsl.PDU{}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, id := range p.AuthEventIDs() {
			if seen[id] {
				continue
			}
			seen[id] = true
			e, err := eventPDUFromDB(tx, id, version)
			if err != nil {
				return nil, err
			}
			out = append(out, e)
			queue = append(queue, e)
		}
	}
	return out, nil
}

func decodeWirePDU(version string, value any) (gmsl.PDU, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return parseAndValidatePDU(version, raw)
}

func handleMakeLeave(c *gin.Context) {
	hs := GetHomeServer()
	roomID, userID := c.Param("roomId"), c.Param("userId")
	if extractServerName(userID) != c.GetString("origin") {
		federationFailure(c, 403, fmt.Errorf("origin mismatch"))
		return
	}
	version, err := federationRoomVersion(roomID)
	if err != nil {
		federationFailure(c, 404, err)
		return
	}
	event := &MatrixEvent{RoomID: roomID, Sender: userID, StateKey: &userID, EventType: EventTypeRoomMember, Content: map[string]any{"membership": "leave"}}
	if err = buildLocalPDU(db.GetDB(), hs, event, version); err != nil {
		federationFailure(c, 403, err)
		return
	}
	wire, _ := matrixEventToPDU(event)
	delete(wire, "signatures")
	delete(wire, "hashes")
	c.JSON(200, gin.H{"room_version": version, "event": wire})
}
func handleSendLeave(c *gin.Context) {
	hs := GetHomeServer()
	version, err := federationRoomVersion(c.Param("roomId"))
	if err != nil {
		federationFailure(c, 404, err)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil {
		federationFailure(c, 400, err)
		return
	}
	p, err := parseAndValidatePDU(version, raw)
	if err != nil {
		federationFailure(c, 400, err)
		return
	}
	membership, _ := p.Membership()
	if p.EventID() != c.Param("eventId") || p.RoomID().String() != c.Param("roomId") || p.StateKey() == nil || *p.StateKey() != string(p.SenderID()) || membership != "leave" || extractServerName(string(p.SenderID())) != c.GetString("origin") {
		federationFailure(c, 403, fmt.Errorf("invalid leave identity"))
		return
	}
	if err = validateIncomingPDU(c.Request.Context(), hs, p, true); err != nil {
		federationFailure(c, 403, err)
		return
	}
	if err = db.GetDB().Transaction(func(tx *gorm.DB) error { return persistVerifiedPDU(tx, hs, p, true) }); err != nil {
		federationFailure(c, 500, err)
		return
	}
	go hs.NotifyRoomEvent(p.RoomID().String())
	c.JSON(200, gin.H{})
}
func handleFederationInvite(c *gin.Context) {
	hs := GetHomeServer()
	var request struct {
		RoomVersion     string            `json:"room_version"`
		Event           json.RawMessage   `json:"event"`
		InviteRoomState []json.RawMessage `json:"invite_room_state"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		federationFailure(c, 400, err)
		return
	}
	p, err := parseAndValidatePDU(request.RoomVersion, request.Event)
	if err != nil {
		federationFailure(c, 400, err)
		return
	}
	membership, _ := p.Membership()
	origin := c.GetString("origin")
	if p.RoomID().String() != c.Param("roomId") || p.EventID() != c.Param("eventId") || p.Type() != "m.room.member" || p.StateKey() == nil || extractServerName(*p.StateKey()) != hs.Config.ServerName || extractServerName(string(p.SenderID())) != origin || membership != "invite" {
		federationFailure(c, 403, fmt.Errorf("invalid invite identity"))
		return
	}
	impl, _ := gmsl.GetRoomVersion(p.Version())
	redacted, err := impl.RedactEventJSON(p.JSON())
	if err != nil {
		federationFailure(c, 400, err)
		return
	}
	verified, err := (federationJSONVerifier{hs}).VerifyJSONs(c.Request.Context(), []gmsl.VerifyJSONRequest{{ServerName: spec.ServerName(origin), AtTS: p.OriginServerTS(), Message: redacted}})
	if err != nil || len(verified) != 1 || verified[0].Error != nil {
		federationFailure(c, 403, fmt.Errorf("invalid invite signature"))
		return
	}
	// An invite is a signed proposal, not an authoritative room state snapshot.
	// Authorization against the complete state is deferred to the join response.
	p = p.Sign(hs.Config.ServerName, gmsl.KeyID(hs.keyID), hs.privateKey)
	err = db.GetDB().Transaction(func(tx *gorm.DB) error {
		if err := persistVerifiedPDU(tx, hs, p, false); err != nil {
			return err
		}
		hs.Room.updateRoomMember(tx, p.RoomID().String(), *p.StateKey(), MembershipInvite, p.EventID())
		return nil
	})
	if err != nil {
		federationFailure(c, 500, err)
		return
	}
	go hs.NotifyRoomEvent(p.RoomID().String())
	wire, _ := wirePDU(p)
	c.JSON(200, gin.H{"event": wire})
}
