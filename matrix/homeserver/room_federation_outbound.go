package homeserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	gmsl "github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"gorm.io/gorm"
)

// signRemoteInvite asks the invitee's homeserver to accept and countersign the
// invite before it is committed locally. Its signature is independently checked.
func (r *RoomService) signRemoteInvite(tx *gorm.DB, event *MatrixEvent) error {
	if event.StateKey == nil {
		return fmt.Errorf("invite requires a state key")
	}
	target := extractServerName(*event.StateKey)
	if target == r.HS.Config.ServerName {
		return nil
	}
	var room MatrixRoom
	if err := tx.Where("room_id = ?", event.RoomID).First(&room).Error; err != nil {
		return err
	}
	wire, err := matrixEventToPDU(event)
	if err != nil {
		return err
	}
	state, err := localStatePDUs(tx, event.RoomID)
	if err != nil {
		return err
	}
	stripped := make([]map[string]any, 0, len(state)+1)
	for _, s := range state {
		// The invite preview requires room metadata and inviter membership;
		// do not disclose every other user's membership to an invited server.
		if s.Type() == "m.room.member" && string(s.SenderID()) != event.Sender {
			continue
		}
		if s.StateKey() == nil {
			continue
		}
		var content any
		if err = json.Unmarshal(s.Content(), &content); err != nil {
			return err
		}
		stripped = append(stripped, map[string]any{"type": s.Type(), "state_key": *s.StateKey(), "sender": string(s.SenderID()), "content": content})
	}
	stripped = append(stripped, map[string]any{"type": string(event.EventType), "state_key": *event.StateKey, "sender": event.Sender, "content": event.Content})
	client := NewFederationClient(r.HS)
	endpoint := fmt.Sprintf("%s/_matrix/federation/v2/invite/%s/%s", client.federationBaseURL(target), url.PathEscape(event.RoomID), url.PathEscape(event.EventID))
	response, body, err := client.doFederationRequestContext(tx.Statement.Context, http.MethodPut, endpoint, target, map[string]any{"room_version": room.RoomVersion, "event": wire, "invite_room_state": stripped}, 2<<20)
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("remote invite failed: HTTP %d: %s", response.StatusCode, string(body))
	}
	var reply struct {
		Event json.RawMessage `json:"event"`
	}
	if err = json.Unmarshal(body, &reply); err != nil {
		return err
	}
	signed, err := parseAndValidatePDU(room.RoomVersion, reply.Event)
	if err != nil {
		return err
	}
	if signed.EventID() != event.EventID {
		return fmt.Errorf("invite response changed the event identity")
	}
	version, err := gmsl.GetRoomVersion(gmsl.RoomVersion(room.RoomVersion))
	if err != nil {
		return err
	}
	redacted, err := version.RedactEventJSON(signed.JSON())
	if err != nil {
		return err
	}
	results, err := (federationJSONVerifier{r.HS}).VerifyJSONs(tx.Statement.Context, []gmsl.VerifyJSONRequest{{ServerName: spec.ServerName(target), AtTS: signed.OriginServerTS(), Message: redacted, ValidityCheckingFunc: gmsl.StrictValiditySignatureCheck}})
	if err != nil {
		return err
	}
	if len(results) != 1 {
		return fmt.Errorf("missing invite signature verification result")
	}
	if results[0].Error != nil {
		return results[0].Error
	}
	*event = *pduToMatrixEvent(signed)
	return nil
}
