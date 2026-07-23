package homeserver

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"maps"
	"testing"
)

// ===== Matrix Spec Canonical JSON Test Vectors =====
// https://spec.matrix.org/v1.13/appendices/#canonical-json

func TestCanonicalJSON_SpecVectors(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty object",
			input:    `{}`,
			expected: `{}`,
		},
		{
			name:     "simple object with whitespace stripped",
			input:    `{"one": 1, "two": "Two"}`,
			expected: `{"one":1,"two":"Two"}`,
		},
		{
			name:     "keys sorted alphabetically (b before a)",
			input:    `{"b": "2", "a": "1"}`,
			expected: `{"a":"1","b":"2"}`,
		},
		{
			name:     "keys sorted no whitespace",
			input:    `{"b":"2","a":"1"}`,
			expected: `{"a":"1","b":"2"}`,
		},
		{
			name:     "nested object with arrays",
			input:    `{"auth":{"success":true,"mxid":"@john.doe:example.com","profile":{"display_name":"John Doe","three_pids":[{"medium":"email","address":"john.doe@example.org"},{"medium":"msisdn","address":"123456789"}]}}}`,
			expected: `{"auth":{"mxid":"@john.doe:example.com","profile":{"display_name":"John Doe","three_pids":[{"address":"john.doe@example.org","medium":"email"},{"address":"123456789","medium":"msisdn"}]},"success":true}}`,
		},
		{
			name:     "non-ASCII UTF-8 characters (Japanese)",
			input:    `{"a": "日本語"}`,
			expected: `{"a":"日本語"}`,
		},
		{
			name:     "Unicode keys sorted by codepoint",
			input:    `{"本": 2, "日": 1}`,
			expected: `{"日":1,"本":2}`,
		},
		{
			name:     "Unicode escape normalized to UTF-8",
			input:    `{"a": "\u65E5"}`,
			expected: `{"a":"日"}`,
		},
		{
			name:     "null value",
			input:    `{"a": null}`,
			expected: `{"a":null}`,
		},
		{
			name:     "negative zero and exponential notation",
			input:    `{"a": -0, "b": 1e10}`,
			expected: `{"a":0,"b":10000000000}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Parse input JSON into interface{}
			var parsed any
			if err := json.Unmarshal([]byte(tt.input), &parsed); err != nil {
				t.Fatalf("failed to parse input JSON: %v", err)
			}

			result, err := CanonicalJSON(parsed)
			if err != nil {
				t.Fatalf("CanonicalJSON returned error: %v", err)
			}

			if string(result) != tt.expected {
				t.Errorf("CanonicalJSON mismatch:\n  input:    %s\n  got:      %s\n  expected: %s", tt.input, string(result), tt.expected)
			}
		})
	}
}

// ===== Matrix Spec JSON Signing Test Vectors =====
// https://spec.matrix.org/v1.13/appendices/#cryptographic-test-vectors

// getSpecTestSigningKey returns the ed25519 key pair from the spec test vectors.
// Seed: YJDBA9Xnr2sVqXD9Vj7XVUnmFZcZrlw8Md7kMW+3XA1
// Server: "domain", Key ID: "ed25519:1".
func getSpecTestSigningKey(t *testing.T) (ed25519.PrivateKey, ed25519.PublicKey) {
	t.Helper()

	seedBytes, err := base64.RawStdEncoding.DecodeString("YJDBA9Xnr2sVqXD9Vj7XVUnmFZcZrlw8Md7kMW+3XA1")
	if err != nil {
		t.Fatalf("failed to decode signing key seed: %v", err)
	}

	privateKey := ed25519.NewKeyFromSeed(seedBytes)
	publicKey := privateKey.Public().(ed25519.PublicKey) //nolint:forcetypeassert

	return privateKey, publicKey
}

func TestSignJSON_SpecVector_EmptyObject(t *testing.T) {
	privateKey, _ := getSpecTestSigningKey(t)
	serverName := "domain"
	keyID := "ed25519:1"

	// Create a FederationService with the spec test key
	hs := &HomeServer{
		Config:     &HSConfig{ServerName: serverName},
		privateKey: privateKey,
		keyID:      keyID,
	}
	fed := NewFederationService(hs)

	// Sign empty object
	input := map[string]any{}
	sig := fed.signJSON(input)

	expectedSig := "K8280/U9SSy9IVtjBuVeLr+HpOB4BQFWbg+UZaADMtTdGYI7Geitb76LTrr5QV/7Xg4ahLwYGYZzuHGZKM5ZAQ"
	if sig != expectedSig {
		t.Errorf("signJSON(empty object) signature mismatch:\n  got:      %s\n  expected: %s", sig, expectedSig)
	}
}

func TestSignJSON_SpecVector_SimpleObject(t *testing.T) {
	privateKey, _ := getSpecTestSigningKey(t)
	serverName := "domain"
	keyID := "ed25519:1"

	hs := &HomeServer{
		Config:     &HSConfig{ServerName: serverName},
		privateKey: privateKey,
		keyID:      keyID,
	}
	fed := NewFederationService(hs)

	// Sign object with data
	input := map[string]any{
		"one": float64(1),
		"two": "Two",
	}
	sig := fed.signJSON(input)

	expectedSig := "KqmLSbO39/Bzb0QIYE82zqLwsA+PDzYIpIRA2sRQ4sL53+sN6/fpNSoqE7BP7vBZhG6kYdD13EIMJpvhJI+6Bw"
	if sig != expectedSig {
		t.Errorf("signJSON(simple object) signature mismatch:\n  got:      %s\n  expected: %s", sig, expectedSig)
	}
}

func TestSignJSON_SpecVector_Verify(t *testing.T) {
	privateKey, publicKey := getSpecTestSigningKey(t)
	serverName := "domain"
	keyID := "ed25519:1"

	hs := &HomeServer{
		Config:     &HSConfig{ServerName: serverName},
		privateKey: privateKey,
		keyID:      keyID,
	}
	fed := NewFederationService(hs)

	// Sign and verify round-trip
	input := map[string]any{
		"one": float64(1),
		"two": "Two",
	}
	sig := fed.signJSON(input)

	sigBytes, err := base64.RawStdEncoding.DecodeString(sig)
	if err != nil {
		t.Fatalf("failed to decode signature: %v", err)
	}

	// The canonical JSON of the input (without signatures/unsigned) should verify
	canonical, err := CanonicalJSON(input)
	if err != nil {
		t.Fatalf("failed to compute canonical JSON: %v", err)
	}

	if !ed25519.Verify(publicKey, canonical, sigBytes) {
		t.Error("signature verification failed for round-trip test")
	}
}

// ===== Matrix Spec Event Signing Test Vectors =====
// https://spec.matrix.org/v1.13/appendices/#cryptographic-test-vectors

// computeContentHash computes the content hash of a Matrix event per spec.
// 1. Remove unsigned, signatures, hashes
// 2. Canonical JSON encode
// 3. SHA-256 hash
// 4. Unpadded base64.
func computeContentHash(event map[string]any) (string, error) {
	// Make a copy
	hashInput := make(map[string]any)
	maps.Copy(hashInput, event)

	delete(hashInput, "unsigned")
	delete(hashInput, "signatures")
	delete(hashInput, "hashes")

	canonical, err := CanonicalJSON(hashInput)
	if err != nil {
		return "", err
	}

	hash := sha256.Sum256(canonical)

	return base64.RawStdEncoding.EncodeToString(hash[:]), nil
}

// redactEvent applies the redaction algorithm (simplified for test vectors).
// For the test vectors, the redaction keeps all top-level keys needed for signing.
func redactEvent(event map[string]any) map[string]any {
	// Keys always kept after redaction (per spec)
	alwaysKept := map[string]bool{
		"event_id":         true, //nolint:goconst
		"type":             true, //nolint:goconst
		"room_id":          true, //nolint:goconst
		"sender":           true, //nolint:goconst
		"state_key":        true, //nolint:goconst
		"content":          true, //nolint:goconst
		"hashes":           true,
		"signatures":       true,
		"depth":            true, //nolint:goconst
		"prev_events":      true, //nolint:goconst
		"auth_events":      true, //nolint:goconst
		"origin":           true, //nolint:goconst
		"origin_server_ts": true, //nolint:goconst
		"membership":       true, //nolint:goconst
	}

	redacted := make(map[string]any)

	for k, v := range event {
		if alwaysKept[k] {
			redacted[k] = v
		}
	}

	// Redact content based on event type
	eventType, _ := redacted["type"].(string)
	switch eventType {
	case "m.room.member": //nolint:goconst
		if content, ok := redacted["content"].(map[string]any); ok {
			newContent := make(map[string]any)
			if m, ok := content["membership"]; ok {
				newContent["membership"] = m
			}

			redacted["content"] = newContent
		}
	case "m.room.create": //nolint:goconst
		if content, ok := redacted["content"].(map[string]any); ok {
			newContent := make(map[string]any)
			if c, ok := content["creator"]; ok {
				newContent["creator"] = c
			}

			redacted["content"] = newContent
		}
	case "m.room.join_rules": //nolint:goconst
		if content, ok := redacted["content"].(map[string]any); ok {
			newContent := make(map[string]any)
			if j, ok := content["join_rule"]; ok {
				newContent["join_rule"] = j
			}

			redacted["content"] = newContent
		}
	case "m.room.power_levels": //nolint:goconst
		// Keep specific keys
		if content, ok := redacted["content"].(map[string]any); ok {
			kept := map[string]bool{
				"ban": true, "events": true, "events_default": true, //nolint:goconst
				"kick": true, "redact": true, "state_default": true, //nolint:goconst
				"users": true, "users_default": true, //nolint:goconst
			}
			newContent := make(map[string]any)

			for k, v := range content {
				if kept[k] {
					newContent[k] = v
				}
			}

			redacted["content"] = newContent
		}
	case "m.room.history_visibility":
		if content, ok := redacted["content"].(map[string]any); ok {
			newContent := make(map[string]any)
			if h, ok := content["history_visibility"]; ok {
				newContent["history_visibility"] = h
			}

			redacted["content"] = newContent
		}
	default:
		// For unknown types, redact content to empty
		redacted["content"] = map[string]any{}
	}

	return redacted
}

func TestEventSigning_SpecVector_MinimalEvent(t *testing.T) {
	privateKey, _ := getSpecTestSigningKey(t)
	serverName := "domain"
	keyID := "ed25519:1"

	hs := &HomeServer{
		Config:     &HSConfig{ServerName: serverName},
		privateKey: privateKey,
		keyID:      keyID,
	}
	fed := NewFederationService(hs)

	// Spec test vector 1: minimal event
	event := map[string]any{
		"room_id":          "!x:domain",
		"sender":           "@a:domain",
		"origin":           "domain",
		"origin_server_ts": float64(1000000),
		"signatures":       map[string]any{},
		"hashes":           map[string]any{},
		"type":             "X",
		"content":          map[string]any{},
		"prev_events":      []any{},
		"auth_events":      []any{},
		"depth":            float64(3),
		"unsigned": map[string]any{
			"age_ts": float64(1000000),
		},
	}

	// Step 1: Compute content hash
	contentHash, err := computeContentHash(event)
	if err != nil {
		t.Fatalf("failed to compute content hash: %v", err)
	}

	expectedHash := "5jM4wQpv6lnBo7CLIghJuHdW+s2CMBJPUOGOC89ncos"
	if contentHash != expectedHash {
		t.Errorf("content hash mismatch:\n  got:      %s\n  expected: %s", contentHash, expectedHash)
	}

	// Step 2: Add hashes to event
	event["hashes"] = map[string]any{
		"sha256": contentHash,
	}

	// Step 3: Redact event for signing
	redacted := redactEvent(event)

	// Step 4: Sign the redacted event
	sig := fed.signJSON(redacted)

	expectedSig := "KxwGjPSDEtvnFgU00fwFz+l6d2pJM6XBIaMEn81SXPTRl16AqLAYqfIReFGZlHi5KLjAWbOoMszkwsQma+lYAg"
	if sig != expectedSig {
		t.Errorf("event signature mismatch:\n  got:      %s\n  expected: %s", sig, expectedSig)
	}
}

func TestEventSigning_SpecVector_MessageEvent(t *testing.T) {
	privateKey, _ := getSpecTestSigningKey(t)
	serverName := "domain"
	keyID := "ed25519:1"

	hs := &HomeServer{
		Config:     &HSConfig{ServerName: serverName},
		privateKey: privateKey,
		keyID:      keyID,
	}
	fed := NewFederationService(hs)

	// Spec test vector 2: message event (older format with event_id, no prev_events/auth_events/depth)
	event := map[string]any{
		"content": map[string]any{
			"body": "Here is the message content", //nolint:goconst
		},
		"event_id":         "$0:domain",
		"origin":           "domain",
		"origin_server_ts": float64(1000000),
		"type":             "m.room.message",
		"room_id":          "!r:domain",
		"sender":           "@u:domain",
		"signatures":       map[string]any{},
		"unsigned": map[string]any{
			"age_ts": float64(1000000),
		},
	}

	// Step 1: Compute content hash
	contentHash, err := computeContentHash(event)
	if err != nil {
		t.Fatalf("failed to compute content hash: %v", err)
	}

	expectedHash := "onLKD1bGljeBWQhWZ1kaP9SorVmRQNdN5aM2JYU2n/g"
	if contentHash != expectedHash {
		t.Errorf("content hash mismatch:\n  got:      %s\n  expected: %s", contentHash, expectedHash)
	}

	// Step 2: Add hashes
	event["hashes"] = map[string]any{
		"sha256": contentHash,
	}

	// Step 3: Redact event for signing (m.room.message: content is redacted to empty)
	redacted := redactEvent(event)

	// Step 4: Sign
	sig := fed.signJSON(redacted)

	expectedSig := "Wm+VzmOUOz08Ds+0NTWb1d4CZrVsJSikkeRxh6aCcUwu6pNC78FunoD7KNWzqFn241eYHYMGCA5McEiVPdhzBA"
	if sig != expectedSig {
		t.Errorf("event signature mismatch:\n  got:      %s\n  expected: %s", sig, expectedSig)
	}
}

// ===== Additional Canonical JSON Edge Cases =====

func TestCanonicalJSON_IntegerValues(t *testing.T) {
	// Ensure float64 values that are integers are rendered without decimal point
	tests := []struct {
		name     string
		input    map[string]any
		expected string
	}{
		{
			name:     "integer 0",
			input:    map[string]any{"v": float64(0)},
			expected: `{"v":0}`,
		},
		{
			name:     "integer 1",
			input:    map[string]any{"v": float64(1)},
			expected: `{"v":1}`,
		},
		{
			name:     "large integer",
			input:    map[string]any{"v": float64(9007199254740991)}, // 2^53 - 1
			expected: `{"v":9007199254740991}`,
		},
		{
			name:     "negative integer",
			input:    map[string]any{"v": float64(-42)},
			expected: `{"v":-42}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := CanonicalJSON(tt.input)
			if err != nil {
				t.Fatalf("CanonicalJSON returned error: %v", err)
			}

			if string(result) != tt.expected {
				t.Errorf("got %s, expected %s", string(result), tt.expected)
			}
		})
	}
}

func TestCanonicalJSON_BoolAndNull(t *testing.T) {
	input := map[string]any{
		"z": true,
		"a": false,
		"m": nil,
	}

	result, err := CanonicalJSON(input)
	if err != nil {
		t.Fatalf("CanonicalJSON returned error: %v", err)
	}

	expected := `{"a":false,"m":null,"z":true}`
	if string(result) != expected {
		t.Errorf("got %s, expected %s", string(result), expected)
	}
}

func TestCanonicalJSON_NestedArrays(t *testing.T) {
	input := map[string]any{
		"a": []any{float64(3), float64(1), float64(2)},
	}

	result, err := CanonicalJSON(input)
	if err != nil {
		t.Fatalf("CanonicalJSON returned error: %v", err)
	}
	// Arrays preserve order (no sorting)
	expected := `{"a":[3,1,2]}`
	if string(result) != expected {
		t.Errorf("got %s, expected %s", string(result), expected)
	}
}
