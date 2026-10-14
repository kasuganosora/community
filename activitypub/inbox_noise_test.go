package activitypub

import (
	"bytes"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestUnverifiableSelfDeleteIsAcknowledgedQuietly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		body string
		code int
	}{
		{`{"id":"https://gone.example/users/a#delete","type":"Delete","actor":"https://gone.example/users/a","object":"https://gone.example/users/a"}`, 202},
		{`{"id":"https://gone.example/users/a#delete","type":"Delete","actor":"https://gone.example/users/a","object":{"id":"https://gone.example/users/a","type":"Person"}}`, 202},
		// Anything else unsigned is still refused.
		{`{"id":"https://gone.example/s/1#delete","type":"Delete","actor":"https://gone.example/users/a","object":"https://gone.example/s/1"}`, 401},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/inbox", bytes.NewBufferString(tc.body))
		c.Request.Header.Set("Content-Type", "application/activity+json")
		SharedInbox(c)
		require.Equal(t, tc.code, w.Code, tc.body)
	}
}

func TestRoutineSignatureFailures(t *testing.T) {
	require.True(t, isRoutineSignatureFailure(errors.New("no signature header")))
	require.True(t, isRoutineSignatureFailure(fmt.Errorf("getting public key: %w", fmt.Errorf("fetching actor: %w", &FetchError{StatusCode: 410}))))
	require.False(t, isRoutineSignatureFailure(errors.New("digest verification failed: digest mismatch")))
}
