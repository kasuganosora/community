package homeserver

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCanonicalJSON_MinimalStringEscapes(t *testing.T) {
	value := "<>&🎉\u2028\u2029"
	for _, object := range []any{
		map[string]any{value: value},
		gin.H{value: value},
		map[string]string{value: value},
	} {
		got, err := CanonicalJSON(object)
		require.NoError(t, err)
		assert.Equal(t, `{"`+value+`":"`+value+`"}`, string(got))
	}
	got, err := CanonicalJSON([]string{value})
	require.NoError(t, err)
	assert.Equal(t, `["`+value+`"]`, string(got))
}

func TestFederationRequest_SignatureMatchesMatrixCanonicalURI(t *testing.T) {
	client, _, publicKey, _ := newTestFederationClient()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _, _, signature, err := parseXMatrixAuth(r.Header.Get("Authorization"))
		if !assert.NoError(t, err) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		raw, err := base64.RawStdEncoding.DecodeString(signature)
		if !assert.NoError(t, err) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// Independent canonical bytes: Matrix leaves the query's ampersand
		// literal. Go's ordinary json.Marshal escapes it as \u0026.
		canonical := fmt.Sprintf(`{"destination":"dest.server","method":"GET","origin":"origin.local","uri":"%s"}`, r.RequestURI)
		if !assert.True(t, ed25519.Verify(publicKey, []byte(canonical), raw), "signature must verify against the exact wire URI") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	response, _, err := client.doFederationRequest(http.MethodGet, server.URL+"/_matrix/federation/v1/make_join/%21room:dest/%40alice:origin?ver=9&ver=10", "dest.server", nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, response.StatusCode)
}
