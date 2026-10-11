package homeserver

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gmsl "github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/stretchr/testify/require"
)

func TestFederationJoinPreservesRestrictedAuthoriser(t *testing.T) {
	content, err := federationJoinContent([]byte(`{"membership":"join","join_authorised_via_users_server":"@approver:remote.example","displayname":"Alice"}`))
	require.NoError(t, err)
	require.Equal(t, "@approver:remote.example", content["join_authorised_via_users_server"])
	require.Equal(t, "Alice", content["displayname"])
	_, err = federationJoinContent([]byte(`{"join_authorised_via_users_server":false}`))
	require.Error(t, err)
}

func TestFederationJoinResponseRetainsCountersignedEvent(t *testing.T) {
	hs := createTestHomeServer(t)
	version, err := gmsl.GetRoomVersion("10")
	require.NoError(t, err)
	userID := "@alice:test.local"
	roomID := "!room:test.local"
	build := func(content map[string]any) gmsl.PDU {
		builder := version.NewEventBuilder()
		builder.RoomID, builder.SenderID, builder.Type, builder.StateKey = roomID, userID, "m.room.member", &userID
		builder.Depth = 1
		require.NoError(t, builder.SetContent(content))
		event, e := builder.Build(time.Now(), spec.ServerName(hs.Config.ServerName), gmsl.KeyID(hs.keyID), hs.privateKey)
		require.NoError(t, e)
		return event
	}
	event := build(map[string]any{"membership": "join", "displayname": "Alice", "join_authorised_via_users_server": "@approver:test.local"})
	raw := json.RawMessage(event.JSON())
	joined, err := verifiedFederationJoin(hs, "10", roomID, userID, raw, raw)
	require.NoError(t, err)
	require.Equal(t, event.EventID(), joined.EventID())
	_, err = verifiedFederationJoin(hs, "10", roomID, userID, raw, nil)
	require.ErrorContains(t, err, "countersigned")
	var wire map[string]any
	require.NoError(t, json.Unmarshal(raw, &wire))
	delete(wire, "signatures")
	_, err = verifiedFederationJoin(hs, "10", roomID, userID, raw, wire)
	require.Error(t, err, "a returned event without signatures must be rejected")
	changed := build(map[string]any{"membership": "join", "displayname": "Mallory", "join_authorised_via_users_server": "@approver:test.local"})
	_, err = verifiedFederationJoin(hs, "10", roomID, userID, raw, json.RawMessage(changed.JSON()))
	require.Error(t, err, "the remote server must not replace our event")
	_, err = verifiedFederationJoin(hs, "10", roomID, "@other:test.local", raw, raw)
	require.ErrorContains(t, err, "identity")
	remotePublic, remotePrivate, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	GetKeyCache().Set("authoriser.example", "ed25519:join", remotePublic, time.Now().Add(time.Hour))
	restricted := build(map[string]any{"membership": "join", "join_authorised_via_users_server": "@approver:authoriser.example"})
	restrictedRaw := json.RawMessage(restricted.JSON())
	_, err = verifiedFederationJoin(hs, "10", roomID, userID, restrictedRaw, restrictedRaw)
	require.Error(t, err, "restricted join needs authorising server signature as well as our signature")
	signed := restricted.Sign("authoriser.example", "ed25519:join", remotePrivate)
	joined, err = verifiedFederationJoin(hs, "10", roomID, userID, restrictedRaw, json.RawMessage(signed.JSON()))
	require.NoError(t, err)
	require.Equal(t, signed.JSON(), joined.JSON(), "persist the countersigned event, not the unsigned local template")
	public := build(map[string]any{"membership": "join"})
	_, err = verifiedFederationJoin(hs, "10", roomID, userID, json.RawMessage(public.JSON()), nil)
	require.NoError(t, err, "older public-room send_join responses need not return event")
}

func TestFederationJoinReportsRemoteFailure(t *testing.T) {
	for _, code := range []string{"M_INCOMPATIBLE_ROOM_VERSION", "M_UNABLE_TO_AUTHORISE_JOIN", "M_UNABLE_TO_GRANT_JOIN", "M_FORBIDDEN"} {
		t.Run(code, func(t *testing.T) {
			failure := federationJoinError(&federationHTTPError{Status: 400, Message: `{"errcode":"` + code + `","error":"Join rejected"}`})
			require.Equal(t, code, failure.ErrCode)
			require.Equal(t, "Join rejected", failure.Error)
		})
	}
	require.Equal(t, "M_UNKNOWN", federationJoinError(&federationHTTPError{Status: 502, Message: "Bad gateway"}).ErrCode)
}

func TestSendJoinPreservesVersionOneAndTwoEventID(t *testing.T) {
	for _, eventID := range []string{"$legacy:origin.local", "$reference_hash"} {
		t.Run(eventID, func(t *testing.T) {
			for _, fallback := range []bool{false, true} {
				requests := 0
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests++
					var body map[string]any
					require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
					if eventID == "$legacy:origin.local" {
						require.Equal(t, eventID, body["event_id"])
					} else {
						require.NotContains(t, body, "event_id")
					}
					if fallback && requests == 1 {
						w.WriteHeader(http.StatusNotFound)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					if fallback {
						_, _ = w.Write([]byte(`[200,{"state":[],"auth_chain":[]}]`))
					} else {
						_, _ = w.Write([]byte(`{"state":[],"auth_chain":[]}`))
					}
				}))
				client, address := newTLSFederationClient(server)
				original := map[string]any{"event_id": eventID, "type": "m.room.member"}
				_, err := client.SendJoin(address, "!room:remote.example", original)
				server.Close()
				require.NoError(t, err)
				require.Equal(t, eventID, original["event_id"], "transport must not mutate the signed event supplied by caller")
				if fallback {
					require.Equal(t, 2, requests)
				} else {
					require.Equal(t, 1, requests)
				}
			}
		})
	}
}

func TestFederationJoinFailureHTTPStatus(t *testing.T) {
	for _, code := range []string{"M_INCOMPATIBLE_ROOM_VERSION", "M_UNSUPPORTED_ROOM_VERSION", "M_UNABLE_TO_AUTHORISE_JOIN", "M_UNABLE_TO_GRANT_JOIN"} {
		require.Equal(t, http.StatusBadRequest, matrixErrorStatus(&MatrixError{ErrCode: code}))
	}
	require.Equal(t, http.StatusForbidden, matrixErrorStatus(federationJoinError(&federationHTTPError{Status: 403, Message: `{"errcode":"M_FORBIDDEN","error":"Banned"}`})))
}
