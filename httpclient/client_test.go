package httpclient

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClient(t *testing.T) {
	client := NewClient()
	assert.NotNil(t, client)
}

func TestNewNoProxyClient(t *testing.T) {
	client := NewNoProxyClient()
	assert.NotNil(t, client)
}

func TestRequest_SetHeader(t *testing.T) {
	client := NewNoProxyClient()
	req := client.NewRequest("GET", "http://example.com")
	req.SetHeader("X-Custom", "test-value")

	assert.Equal(t, "test-value", req.header.Get("X-Custom"))
}

func TestRequest_SetTimeout(t *testing.T) {
	client := NewNoProxyClient()
	req := client.NewRequest("GET", "http://example.com")
	result := req.SetTimeout(30)

	assert.Equal(t, 30, req.timeout)
	assert.Equal(t, req, result) // should return self for chaining
}

func TestRequest_SetTryTimes(t *testing.T) {
	client := NewNoProxyClient()
	req := client.NewRequest("GET", "http://example.com")
	result := req.SetTryTimes(3)

	assert.Equal(t, 3, req.tryTimes)
	assert.Equal(t, req, result)
}

func TestRequest_SetUrlParams(t *testing.T) {
	client := NewNoProxyClient()
	req := client.NewRequest("GET", "http://example.com")
	req.SetUrlParams("key", "value")

	assert.Equal(t, "value", req.urlParams.Get("key"))
}

func TestRequest_SetFormUrlencoded(t *testing.T) {
	client := NewNoProxyClient()
	req := client.NewRequest("POST", "http://example.com")

	params := make(url.Values)
	params["field1"] = []string{"val1"}
	req.SetFormUrlencoded(params)

	assert.Equal(t, "application/x-www-form-urlencoded", req.header.Get("Content-Type"))
	assert.NotNil(t, req.body)
}

func TestRequest_SetJsonBody(t *testing.T) {
	client := NewNoProxyClient()
	req := client.NewRequest("POST", "http://example.com")

	data := map[string]string{"key": "value"}
	req.SetJsonBody(data)

	assert.Equal(t, "application/json", req.header.Get("Content-Type"))
	assert.NotNil(t, req.body)
}

func TestRequest_Do_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	client := NewNoProxyClient()
	req := client.NewRequest("GET", server.URL)
	req.SetTimeout(5)

	resp, err := req.Do()
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, 200, resp.StatusCode)
	assert.True(t, resp.StatusOk())
}

func TestRequest_Do_WithRetry(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount < 3 {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := NewNoProxyClient()
	req := client.NewRequest("GET", server.URL)
	req.SetTryTimes(3)
	req.SetTimeout(5)

	resp, err := req.Do()
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, 3, callCount)
}

func TestRequest_Do_PostWithBody_Retry(t *testing.T) {
	// Verify that POST body is resent correctly on retry
	callCount := 0
	var receivedBodies []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		body := make([]byte, r.ContentLength)
		r.Body.Read(body)
		receivedBodies = append(receivedBodies, string(body))

		if callCount < 2 {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
	}))
	defer server.Close()

	client := NewNoProxyClient()
	req := client.NewRequest("POST", server.URL)
	req.SetJsonBody(map[string]string{"test": "data"})
	req.SetTryTimes(2)
	req.SetTimeout(5)

	resp, err := req.Do()
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, 2, callCount)
	// Both attempts should have the same body
	assert.Equal(t, receivedBodies[0], receivedBodies[1])
}

func TestResponse_Json(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"name":"test","count":42}`))
	}))
	defer server.Close()

	client := NewNoProxyClient()
	req := client.NewRequest("GET", server.URL)
	req.SetTimeout(5)

	resp, err := req.Do()
	require.NoError(t, err)

	var result struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	err = resp.Json(&result)
	require.NoError(t, err)
	assert.Equal(t, "test", result.Name)
	assert.Equal(t, 42, result.Count)
}

func TestResponse_StatusOk(t *testing.T) {
	t.Run("2xx returns ok and no error", func(t *testing.T) {
		for _, code := range []int{200, 201, 204, 299} {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(code)
			}))

			client := NewNoProxyClient()
			req := client.NewRequest("GET", server.URL)
			req.SetTimeout(5)

			resp, err := req.Do()
			require.NoError(t, err)
			assert.True(t, resp.StatusOk())
			server.Close()
		}
	})

	t.Run("non-2xx returns error from Do", func(t *testing.T) {
		for _, code := range []int{301, 400, 404, 500} {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(code)
			}))

			client := NewNoProxyClient()
			req := client.NewRequest("GET", server.URL)
			req.SetTimeout(5)

			_, err := req.Do()
			assert.Error(t, err, "expected error for status %d", code)
			server.Close()
		}
	})
}

func TestRequest_Do_InvalidURL(t *testing.T) {
	client := NewNoProxyClient()
	req := client.NewRequest("GET", "http://invalid.host.that.does.not.exist.example:1")
	req.SetTimeout(2)

	resp, err := req.Do()
	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestDefaultConstants(t *testing.T) {
	assert.Equal(t, 10, DefaultTimeout)
	assert.Equal(t, "Community/1.0", DefaultUA)
	assert.Equal(t, 0, DefaultTryTimes)
}
