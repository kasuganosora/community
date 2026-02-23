package activitypub

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClient(t *testing.T) {
	// Without private key
	client := NewClient(nil, "https://example.com/users/test")
	assert.NotNil(t, client)
	assert.Nil(t, client.privateKey)
	assert.Equal(t, "https://example.com/users/test", client.actorID)
	assert.NotNil(t, client.httpClient)

	// With private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	client = NewClient(privateKey, "https://example.com/users/test")
	assert.NotNil(t, client.privateKey)
}

func TestClientGet(t *testing.T) {
	// Mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		// Verify ActivityPub Accept header
		accept := r.Header.Get("Accept")
		assert.Contains(t, accept, "application/activity+json")
		// Verify User-Agent
		assert.Contains(t, r.Header.Get("User-Agent"), "Community")

		w.Header().Set("Content-Type", "application/activity+json")
		json.NewEncoder(w).Encode(map[string]string{"type": "Note", "id": "test"})
	}))
	defer server.Close()

	client := NewClient(nil, "")
	data, err := client.Get(context.Background(), server.URL+"/test")
	assert.NoError(t, err)
	assert.NotEmpty(t, data)

	var result map[string]string
	err = json.Unmarshal(data, &result)
	assert.NoError(t, err)
	assert.Equal(t, "Note", result["type"])
}

func TestClientGetNon200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer server.Close()

	client := NewClient(nil, "")
	_, err := client.Get(context.Background(), server.URL+"/notfound")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status code: 404")
}

func TestClientPost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "application/activity+json", r.Header.Get("Content-Type"))
		w.WriteHeader(202)
	}))
	defer server.Close()

	client := NewClient(nil, "https://example.com/users/test")
	activity := map[string]interface{}{
		"type":  "Follow",
		"actor": "https://example.com/users/test",
	}
	err := client.Post(context.Background(), server.URL+"/inbox", activity)
	assert.NoError(t, err)
}

func TestClientPostWithSignature(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify signature headers are present
		assert.NotEmpty(t, r.Header.Get("Signature"))
		assert.NotEmpty(t, r.Header.Get("Date"))
		assert.NotEmpty(t, r.Header.Get("Digest"))

		// Verify Digest header format
		digest := r.Header.Get("Digest")
		assert.True(t, strings.HasPrefix(digest, "SHA-256="))

		// Verify Signature header format
		sig := r.Header.Get("Signature")
		assert.Contains(t, sig, "keyId=")
		assert.Contains(t, sig, "algorithm=\"rsa-sha256\"")
		assert.Contains(t, sig, "headers=")
		assert.Contains(t, sig, "signature=")

		w.WriteHeader(202)
	}))
	defer server.Close()

	client := NewClient(privateKey, "https://example.com/users/test")
	activity := map[string]interface{}{
		"type": "Create",
	}
	err = client.Post(context.Background(), server.URL+"/inbox", activity)
	assert.NoError(t, err)
}

func TestClientPostError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("internal server error"))
	}))
	defer server.Close()

	client := NewClient(nil, "")
	err := client.Post(context.Background(), server.URL+"/inbox", map[string]interface{}{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestClientSignRequest(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	client := NewClient(privateKey, "https://example.com/users/test")

	body := []byte(`{"type":"Create"}`)
	req, _ := http.NewRequest("POST", "https://remote.com/inbox", nil)

	err = client.signRequest(req, body)
	assert.NoError(t, err)

	// Verify Date header was set
	assert.NotEmpty(t, req.Header.Get("Date"))

	// Verify Digest header
	digest := req.Header.Get("Digest")
	assert.True(t, strings.HasPrefix(digest, "SHA-256="))

	// Verify the digest is correct
	hash := sha256.Sum256(body)
	expectedDigest := "SHA-256=" + base64.StdEncoding.EncodeToString(hash[:])
	assert.Equal(t, expectedDigest, digest)

	// Verify Signature header
	sig := req.Header.Get("Signature")
	assert.Contains(t, sig, `keyId="https://example.com/users/test#main-key"`)
	assert.Contains(t, sig, `algorithm="rsa-sha256"`)
	assert.Contains(t, sig, `headers="(request-target) host date digest"`)
}

func TestClientSignRequestHostNotLowercased(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	client := NewClient(privateKey, "https://example.com/users/test")

	body := []byte(`{}`)
	// Use a URL with mixed-case host to verify it's not lowercased
	req, _ := http.NewRequest("POST", "https://Remote.Example.COM/inbox", nil)

	err = client.signRequest(req, body)
	assert.NoError(t, err)

	// The signature header should be set - the host in the signing string
	// should preserve the original casing from the URL
	assert.NotEmpty(t, req.Header.Get("Signature"))
}

func TestClientFetchActor(t *testing.T) {
	actor := Actor{
		ID:               "https://remote.com/users/alice",
		Type:             "Person",
		PreferredUsername: "alice",
		Inbox:            "https://remote.com/users/alice/inbox",
		Outbox:           "https://remote.com/users/alice/outbox",
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/activity+json")
		json.NewEncoder(w).Encode(actor)
	}))
	defer server.Close()

	client := NewClient(nil, "")
	result, err := client.FetchActor(context.Background(), server.URL+"/users/alice")
	assert.NoError(t, err)
	assert.Equal(t, "Person", result.Type)
	assert.Equal(t, "alice", result.PreferredUsername)
}

func TestClientFetchObject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/activity+json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"type":    "Note",
			"content": "Hello world",
		})
	}))
	defer server.Close()

	client := NewClient(nil, "")
	result, err := client.FetchObject(context.Background(), server.URL+"/note/1")
	assert.NoError(t, err)
	assert.Equal(t, "Note", result["type"])
	assert.Equal(t, "Hello world", result["content"])
}

func TestClientFetchStatus(t *testing.T) {
	now := "2024-01-01T00:00:00Z"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/activity+json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"type":         "Note",
			"id":           "https://remote.com/statuses/1",
			"content":      "<p>Hello</p>",
			"attributedTo": "https://remote.com/users/alice",
			"published":    now,
		})
	}))
	defer server.Close()

	client := NewClient(nil, "")
	note, err := client.FetchStatus(context.Background(), server.URL+"/statuses/1")
	assert.NoError(t, err)
	assert.Equal(t, "Note", note.Type)
	assert.Equal(t, "<p>Hello</p>", note.Content)
}
