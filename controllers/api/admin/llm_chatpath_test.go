package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/llmsvc"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/utils/translation"
	"github.com/stretchr/testify/require"
)

func TestLLMChatPathSavedAndUsedByConnectionTest(t *testing.T) {
	env := newAdminTestEnv(t)
	require.NoError(t, env.gdb.AutoMigrate(llmsvc.Models()...))
	if config.C.EncryptedKey == "" {
		config.C.EncryptedKey = "unit-test-key-0123456789abcdef"
		t.Cleanup(func() { config.C.EncryptedKey = "" })
	}
	for _, k := range []string{llmsvc.KeyConfig, translation.KeyMode} {
		old := systemsettings.Get(k, nil)
		t.Cleanup(func() { systemsettings.Set(k, old) })
	}
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer srv.Close()

	const secret = "sk-example-chatpath-4321"
	save := func(base, chatPath string) llmsvc.PublicProvider {
		body := map[string]any{
			"providers": []map[string]any{{"name": "Zhipu", "type": "openai_compatible", "base_url": base, "chat_path": chatPath, "model": "m", "api_key": secret}},
			"services":  map[string]string{},
		}
		c, w := newJSONContext(t, http.MethodPut, nil, body)
		UpdateLLMSettings(c)
		require.Equal(t, 200, w.Code, w.Body.String())
		var resp llmSettingsResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		return resp.Providers[0]
	}
	test := func(id string) llmsvc.TestResult {
		c, w := newJSONContext(t, http.MethodPost, nil, map[string]string{"id": id})
		TestLLMProvider(c)
		require.Equal(t, 200, w.Code, w.Body.String())
		require.NotContains(t, w.Body.String(), secret)
		var r llmsvc.TestResult
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &r))
		return r
	}

	// Versioned base (Zhipu coding endpoint style): no /v1 is added.
	p := save(srv.URL+"/api/coding/paas/v4/", "")
	require.Equal(t, "", p.ChatPath)
	r := test(p.ID)
	require.True(t, r.OK, r.Code)
	require.Equal(t, srv.URL+"/api/coding/paas/v4/chat/completions", r.Endpoint)

	// Explicit chat path.
	p = save(srv.URL, "/custom/chat")
	require.Equal(t, "/custom/chat", p.ChatPath)
	r = test(p.ID)
	require.True(t, r.OK, r.Code)
	require.Equal(t, srv.URL+"/custom/chat", r.Endpoint)

	// Unversioned base keeps the OpenAI default.
	p = save(srv.URL, "")
	r = test(p.ID)
	require.Equal(t, srv.URL+"/v1/chat/completions", r.Endpoint)

	mu.Lock()
	require.Equal(t, []string{"/api/coding/paas/v4/chat/completions", "/custom/chat", "/v1/chat/completions"}, paths)
	mu.Unlock()

	// Invalid chat path is rejected with a stable code.
	body := map[string]any{"providers": []map[string]any{{"name": "Bad", "type": "openai_compatible", "base_url": srv.URL, "chat_path": "no-slash path", "model": "m"}}, "services": map[string]string{}}
	c, w := newJSONContext(t, http.MethodPut, nil, body)
	UpdateLLMSettings(c)
	require.Equal(t, 422, w.Code)
	require.Contains(t, w.Body.String(), "llm_invalid_chat_path")
}
