package homeserver

import (
	"encoding/json"
	"testing"
)

func TestDeviceKeysSerialization(t *testing.T) {
	dk := &DeviceKeys{
		UserID:     "@test:example.com",
		DeviceID:   "DEVICE123",
		Algorithms: []string{"ed25519", "curve25519"},
		Keys: map[string]string{
			"ed25519:DEVICE123": "public_key_here",
		},
		Signatures: map[string]map[string]string{
			"@test:example.com": {
				"ed25519:DEVICE123": "signature_here",
			},
		},
	}
	
	// 序列化
	data, err := json.Marshal(dk)
	if err != nil {
		t.Fatalf("Failed to marshal DeviceKeys: %v", err)
	}
	
	// 反序列化
	var dk2 DeviceKeys
	if err := json.Unmarshal(data, &dk2); err != nil {
		t.Fatalf("Failed to unmarshal DeviceKeys: %v", err)
	}
	
	// 验证
	if dk2.UserID != dk.UserID {
		t.Errorf("UserID mismatch: %s != %s", dk2.UserID, dk.UserID)
	}
	
	if dk2.DeviceID != dk.DeviceID {
		t.Errorf("DeviceID mismatch: %s != %s", dk2.DeviceID, dk.DeviceID)
	}
}

func TestParseKeyID(t *testing.T) {
	tests := []struct {
		input         string
		expectedAlgo  string
		expectedKeyID string
	}{
		{"ed25519:AAAAFw", "ed25519", "AAAAFw"},
		{"curve25519:DEVICE123", "curve25519", "DEVICE123"},
		{"signed_curve25519:abc123", "signed_curve25519", "abc123"},
		{"plainkey", "plainkey", ""},
	}
	
	for _, test := range tests {
		algo, keyID := parseKeyID(test.input)
		if algo != test.expectedAlgo || keyID != test.expectedKeyID {
			t.Errorf("parseKeyID(%s) = (%s, %s), expected (%s, %s)",
				test.input, algo, keyID, test.expectedAlgo, test.expectedKeyID)
		}
	}
}

func TestUploadKeysRequestSerialization(t *testing.T) {
	req := UploadKeysRequest{
		DeviceKeys: &DeviceKeys{
			UserID:   "@test:example.com",
			DeviceID: "DEVICE123",
			Keys: map[string]string{
				"ed25519:DEVICE123": "public_key",
			},
		},
		OneTimeKeys: map[string]string{
			"signed_curve25519:AAAA": "otk_public_key",
		},
	}
	
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal request: %v", err)
	}
	
	var req2 UploadKeysRequest
	if err := json.Unmarshal(data, &req2); err != nil {
		t.Fatalf("Failed to unmarshal request: %v", err)
	}
	
	if req2.DeviceKeys == nil {
		t.Fatal("DeviceKeys should not be nil")
	}
	
	if req2.DeviceKeys.UserID != req.DeviceKeys.UserID {
		t.Errorf("UserID mismatch")
	}
}

func TestQueryKeysRequestSerialization(t *testing.T) {
	req := QueryKeysRequest{
		DeviceKeys: map[string][]string{
			"@test1:example.com": {}, // 空数组表示查询所有设备
			"@test2:example.com": {"DEVICE1", "DEVICE2"},
		},
		Timeout: 10000,
		Token:   "sometoken",
	}
	
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal request: %v", err)
	}
	
	var req2 QueryKeysRequest
	if err := json.Unmarshal(data, &req2); err != nil {
		t.Fatalf("Failed to unmarshal request: %v", err)
	}
	
	if len(req2.DeviceKeys) != 2 {
		t.Errorf("Expected 2 users, got %d", len(req2.DeviceKeys))
	}
}

func TestClaimKeysRequestSerialization(t *testing.T) {
	req := ClaimKeysRequest{
		OneTimeKeys: map[string]map[string]string{
			"@test1:example.com": {
				"DEVICE1": "signed_curve25519",
			},
		},
		Timeout: 10000,
	}
	
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal request: %v", err)
	}
	
	var req2 ClaimKeysRequest
	if err := json.Unmarshal(data, &req2); err != nil {
		t.Fatalf("Failed to unmarshal request: %v", err)
	}
	
	if len(req2.OneTimeKeys) != 1 {
		t.Errorf("Expected 1 user, got %d", len(req2.OneTimeKeys))
	}
}

func TestOlmAccount(t *testing.T) {
	// 测试 Olm 账户创建
	account, err := NewOlmAccount()
	if err != nil {
		t.Fatalf("NewOlmAccount failed: %v", err)
	}
	
	if account == nil {
		t.Fatal("Account should not be nil")
	}
	
	if account.IdentityKey == nil {
		t.Fatal("Identity key should not be nil")
	}
}

func TestOlmOneTimeKeys(t *testing.T) {
	account, err := NewOlmAccount()
	if err != nil {
		t.Fatalf("NewOlmAccount failed: %v", err)
	}
	
	// 生成一次性密钥
	err = account.GenerateOneTimeKeys(5)
	if err != nil {
		t.Fatalf("GenerateOneTimeKeys failed: %v", err)
	}
	
	if len(account.OneTimeKeys) != 5 {
		t.Errorf("Expected 5 OTKs, got %d", len(account.OneTimeKeys))
	}
	
	// 测试获取未发布的密钥
	unpublished := account.GetUnpublishedOneTimeKeys()
	if len(unpublished) != 5 {
		t.Errorf("Expected 5 unpublished OTKs, got %d", len(unpublished))
	}
}

func TestBackfillRequest(t *testing.T) {
	// 测试 Backfill 请求结构
	req := GetMissingEventsRequest{
		EarliestEvents: []string{"$event1", "$event2"},
		LatestEvents:   []string{"$event3"},
		Limit:          50,
		MinDepth:       10,
	}
	
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal request: %v", err)
	}
	
	var req2 GetMissingEventsRequest
	if err := json.Unmarshal(data, &req2); err != nil {
		t.Fatalf("Failed to unmarshal request: %v", err)
	}
	
	if req2.Limit != req.Limit {
		t.Errorf("Limit mismatch: %d != %d", req2.Limit, req.Limit)
	}
	
	if req2.MinDepth != req.MinDepth {
		t.Errorf("MinDepth mismatch: %d != %d", req2.MinDepth, req.MinDepth)
	}
}
