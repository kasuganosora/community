package status

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func postRaw(t *testing.T, ts *testSetup, contentType string, body []byte) (int, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/statuses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", contentType)
	setAccount(c, ts.acct)
	safeHandler(c, Create)
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w.Code, resp
}

func TestCreateAcceptsFormURLEncoded(t *testing.T) {
	ts := newTestSetup(t)
	form := url.Values{
		"status":            {"form post"},
		"visibility":        {"unlisted"},
		"sensitive":         {"true"},
		"spoiler_text":      {"cw"},
		"poll[options][]":   {"a", "b"},
		"poll[expires_in]":  {"3600"},
		"poll[multiple]":    {"1"},
		"poll[hide_totals]": {"false"},
	}
	code, resp := postRaw(t, ts, "application/x-www-form-urlencoded", []byte(form.Encode()))
	require.Equal(t, 200, code, resp)
	require.Equal(t, "unlisted", resp["visibility"])
	require.Equal(t, true, resp["sensitive"])
	require.Equal(t, "cw", resp["spoiler_text"])
	poll, _ := resp["poll"].(map[string]any)
	require.NotNil(t, poll, resp)
	require.Len(t, poll["options"], 2)
	require.Equal(t, true, poll["multiple"])
}

func TestCreateAcceptsMultipart(t *testing.T) {
	ts := newTestSetup(t)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range map[string]string{"status": "multipart post", "visibility": "public", "sensitive": "false", "media_ids[]": ""} {
		require.NoError(t, mw.WriteField(k, v))
	}
	require.NoError(t, mw.Close())
	code, resp := postRaw(t, ts, mw.FormDataContentType(), buf.Bytes())
	require.Equal(t, 200, code, resp)
	require.Contains(t, resp["content"], "multipart post")
	require.Equal(t, false, resp["sensitive"])
}

func TestCreateFormRejectsBadBoolean(t *testing.T) {
	ts := newTestSetup(t)
	code, _ := postRaw(t, ts, "application/x-www-form-urlencoded", []byte("status=x&visibility=public&sensitive=maybe"))
	require.Equal(t, 400, code)
	code, _ = postRaw(t, ts, "application/x-www-form-urlencoded", []byte("status=x&visibility="+strings.Repeat("z", 3)))
	require.Equal(t, 400, code)
}
