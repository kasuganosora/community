package translation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kasuganosora/community/reqid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLibreTranslateSendsRequestID(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get(reqid.Header))
		_ = json.NewEncoder(w).Encode(map[string]any{"translatedText": "hello"})
	}))
	defer srv.Close()
	svc := NewServiceWithProvider(&LibreTranslateProvider{baseURL: srv.URL, client: srv.Client()})

	// the incoming request's id is reused
	_, err := svc.TranslateContext(reqid.With(context.Background(), "req-abc-123"), "你好", "zh", "en")
	require.NoError(t, err)
	// without one, a fresh id is generated
	_, err = svc.TranslateContext(context.Background(), "你好", "zh", "en")
	require.NoError(t, err)

	require.Len(t, got, 2)
	assert.Equal(t, "req-abc-123", got[0])
	assert.NotEmpty(t, got[1])
	assert.NotEqual(t, got[0], got[1])
}
