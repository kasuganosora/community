package homeserver

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/stretchr/testify/require"
)

func signedAuxRequest(t *testing.T, hs *HomeServer, r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	payload := map[string]any{"method": method, "uri": req.URL.RequestURI(), "origin": hs.Config.ServerName, "destination": hs.Config.ServerName}
	if body != "" {
		var content any
		require.NoError(t, json.Unmarshal([]byte(body), &content))
		payload["content"] = content
		req.Header.Set("Content-Type", "application/json")
	}
	raw, err := CanonicalJSON(payload)
	require.NoError(t, err)
	sig := base64.RawStdEncoding.EncodeToString(ed25519.Sign(hs.privateKey, raw))
	req.Header.Set("Authorization", fmt.Sprintf(`X-Matrix origin=%q,destination=%q,key=%q,sig=%q`, hs.Config.ServerName, hs.Config.ServerName, hs.keyID, sig))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestAuxiliaryFederationRoutesRequireSignedRequests(t *testing.T) {
	hs := createTestHomeServer(t)
	hs.Config.Enabled = true
	restore := resetGlobalHS(hs)
	defer restore()
	r := gin.New()
	RegisterBackfillRoutes(r)
	RegisterAdditionalFederationRoutes(r)
	RegisterFederationE2EERoutes(r)
	for _, endpoint := range []struct{ method, path string }{
		{"GET", "/backfill/!room:test.local"}, {"GET", "/timestamp_to_event/!room:test.local?ts=0"}, {"GET", "/event_auth/!room:test.local/$event"}, {"POST", "/query_auth/!room:test.local/$event"}, {"POST", "/get_missing_events/!room:test.local"}, {"GET", "/query/user?user_id=@alice:test.local"}, {"GET", "/user/signing_key/@alice:test.local"}, {"POST", "/user/keys/query"}, {"POST", "/user/keys/claim"}, {"PUT", "/send_to_device/m.test/txn"},
	} {
		t.Run(endpoint.path, func(t *testing.T) {
			for _, auth := range []string{"", "Bearer wrong"} {
				req := httptest.NewRequest(endpoint.method, "/_matrix/federation/v1"+endpoint.path, strings.NewReader(`{}`))
				req.Header.Set("Authorization", auth)
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				require.Equal(t, 401, w.Code, w.Body.String())
			}
		})
	}
}

func TestFederationBackfillReturnsSignedDAGAndEnforcesRoomScope(t *testing.T) {
	hs := createTestHomeServer(t)
	hs.Config.Enabled = true
	restore := resetGlobalHS(hs)
	defer restore()
	room := createRoomWithUser(t, hs, "@alice:test.local")
	first := sendMessage(t, hs, room, "@alice:test.local", "first")
	last := sendMessage(t, hs, room, "@alice:test.local", "last")
	other := createRoomWithUser(t, hs, "@alice:test.local")
	foreign := sendMessage(t, hs, other, "@alice:test.local", "private")
	r := gin.New()
	RegisterBackfillRoutes(r)
	RegisterFederationE2EERoutes(r)
	base := "/_matrix/federation/v1"
	w := signedAuxRequest(t, hs, r, "GET", base+"/backfill/"+room+"?v="+url.QueryEscape(last)+"&limit=2", "")
	require.Equal(t, 200, w.Code, w.Body.String())
	var response struct {
		PDUs []map[string]any `json:"pdus"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Len(t, response.PDUs, 2)
	for i, id := range []string{last, first} {
		raw, err := json.Marshal(response.PDUs[i])
		require.NoError(t, err)
		pdu, err := parseAndValidatePDU("10", raw)
		require.NoError(t, err)
		require.Equal(t, id, pdu.EventID())
		require.NotEmpty(t, response.PDUs[i]["signatures"])
		require.NotEmpty(t, response.PDUs[i]["hashes"])
	}
	w = signedAuxRequest(t, hs, r, "GET", base+"/backfill/"+room+"?v="+url.QueryEscape(foreign), "")
	require.Equal(t, 404, w.Code)
	require.Contains(t, w.Body.String(), "M_NOT_FOUND")
	w = signedAuxRequest(t, hs, r, "GET", base+"/event_auth/"+room+"/"+foreign, "")
	require.Equal(t, 404, w.Code)
	body := fmt.Sprintf(`{"latest_events":[%q],"earliest_events":[%q],"limit":2}`, last, first)
	w = signedAuxRequest(t, hs, r, "POST", base+"/get_missing_events/"+room, body)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.JSONEq(t, `{"events":[]}`, w.Body.String())
	w = signedAuxRequest(t, hs, r, "PUT", base+"/send_to_device/m.test/forged", `{"sender":"@evil:other.server","messages":{}}`)
	require.Equal(t, 403, w.Code)
	require.NoError(t, db.GetDB().Model(&MatrixRoomMember{}).Where("room_id = ?", room).Update("membership", MembershipLeave).Error)
	w = signedAuxRequest(t, hs, r, "GET", base+"/backfill/"+room+"?v="+url.QueryEscape(last), "")
	require.Equal(t, 403, w.Code)
}
