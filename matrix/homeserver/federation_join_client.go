package homeserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	gmsl "github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/spec"
)

// Keep the authorising member chosen by make_join for restricted rooms. The
// receiving server must countersign this exact event in send_join.
func federationJoinContent(raw []byte) (map[string]any, error) {
	var content map[string]any
	if err := json.Unmarshal(raw, &content); err != nil {
		return nil, err
	}
	if content == nil {
		content = map[string]any{}
	}
	content["membership"] = "join"
	if via, ok := content["join_authorised_via_users_server"]; ok {
		id, ok := via.(string)
		if !ok {
			return nil, fmt.Errorf("invalid restricted join authorising user")
		}
		if _, err := spec.NewUserID(id, true); err != nil {
			return nil, err
		}
	}
	return content, nil
}

func federationJoinError(err error) *MatrixError {
	var remote *federationHTTPError
	if errors.As(err, &remote) {
		var body MatrixError
		if json.Unmarshal([]byte(remote.Message), &body) == nil {
			switch body.ErrCode {
			case "M_INCOMPATIBLE_ROOM_VERSION", "M_UNSUPPORTED_ROOM_VERSION", "M_UNABLE_TO_AUTHORISE_JOIN", "M_UNABLE_TO_GRANT_JOIN", "M_FORBIDDEN", "M_NOT_FOUND", "M_LIMIT_EXCEEDED":
				return &body
			}
		}
		if remote.Status == 403 {
			return ErrForbidden
		}
		if remote.Status == 404 {
			return ErrNotFound
		}
	}
	return &MatrixError{ErrCode: "M_UNKNOWN", Error: "Unable to complete the join with the remote server"}
}

func verifiedFederationJoin(hs *HomeServer, version, roomID, userID string, localRaw, returnedRaw any) (gmsl.PDU, error) {
	return verifiedFederationJoinContext(context.Background(), hs, version, roomID, userID, localRaw, returnedRaw)
}

func verifiedFederationJoinContext(ctx context.Context, hs *HomeServer, version, roomID, userID string, localRaw, returnedRaw any) (gmsl.PDU, error) {
	local, err := decodeWirePDU(version, localRaw)
	if err != nil {
		return nil, err
	}
	if local.RoomID().String() != roomID || string(local.SenderID()) != userID || local.Type() != "m.room.member" || local.StateKey() == nil || *local.StateKey() != userID {
		return nil, fmt.Errorf("join identity mismatch")
	}
	var content map[string]any
	if err = json.Unmarshal(local.Content(), &content); err != nil {
		return nil, err
	}
	if content["membership"] != "join" {
		return nil, fmt.Errorf("invalid join membership")
	}
	join := local
	if returnedRaw != nil {
		join, err = decodeWirePDU(version, returnedRaw)
		if err != nil {
			return nil, err
		}
		if join.EventID() != local.EventID() {
			return nil, fmt.Errorf("remote server changed join event ID")
		}
		// Signatures and unsigned metadata may change. Every signed event field,
		// including unredacted profile content, must remain ours.
		canonical := func(p gmsl.PDU) ([]byte, error) {
			var wire map[string]json.RawMessage
			if e := json.Unmarshal(p.JSON(), &wire); e != nil {
				return nil, e
			}
			delete(wire, "signatures")
			delete(wire, "unsigned")
			delete(wire, "event_id")
			return CanonicalJSON(wire)
		}
		expected, e := canonical(local)
		if e != nil {
			return nil, e
		}
		actual, e := canonical(join)
		if e != nil {
			return nil, e
		}
		if !bytes.Equal(expected, actual) {
			return nil, fmt.Errorf("remote server changed join event content")
		}
	} else if content["join_authorised_via_users_server"] != nil {
		return nil, fmt.Errorf("restricted join response lacks countersigned event")
	}
	if err = gmsl.VerifyEventSignatures(ctx, join, federationJSONVerifier{hs}, federationUserID); err != nil {
		return nil, err
	}
	return join, nil
}
