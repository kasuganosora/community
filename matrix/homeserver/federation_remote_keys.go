package homeserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
)

// QueryRemoteKeyBundle preserves the remote public cross-signing material while
// restricting the response to the users/devices the client actually requested.
func (c *FederationClient) QueryRemoteKeyBundle(server string, requested map[string][]string) (*QueryKeysResponse, error) {
	response, raw, err := c.doFederationRequest(http.MethodPost, c.federationBaseURL(server)+"/_matrix/federation/v1/user/keys/query", server, map[string]any{"device_keys": requested})
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, &federationHTTPError{Status: response.StatusCode, Message: "remote key query failed"}
	}
	var remote QueryKeysResponse
	if err = json.Unmarshal(raw, &remote); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	result := &QueryKeysResponse{DeviceKeys: map[string]map[string]*DeviceKeys{}, MasterKeys: map[string]*CrossSigningKey{}, SelfSigningKeys: map[string]*CrossSigningKey{}}
	for user, devices := range remote.DeviceKeys {
		ids, ok := requested[user]
		if !ok {
			continue
		}
		result.DeviceKeys[user] = map[string]*DeviceKeys{}
		for device, key := range devices {
			if key == nil || key.UserID != user || key.DeviceID != device {
				return nil, fmt.Errorf("remote device key identity mismatch")
			}
			if len(ids) == 0 || slices.Contains(ids, device) {
				result.DeviceKeys[user][device] = key
			}
		}
	}
	for user := range requested {
		if key := remote.MasterKeys[user]; key != nil {
			if key.UserID != user || !slices.Contains(key.Usage, "master") {
				return nil, fmt.Errorf("remote master key identity mismatch")
			}
			result.MasterKeys[user] = key
		}
		if key := remote.SelfSigningKeys[user]; key != nil {
			if key.UserID != user || !slices.Contains(key.Usage, "self_signing") {
				return nil, fmt.Errorf("remote self signing key identity mismatch")
			}
			result.SelfSigningKeys[user] = key
		}
	}
	// user_signing keys are private to a user's own server and never forwarded.
	return result, nil
}
func (c *FederationClient) ClaimRemoteKeys(server string, requested map[string]map[string]string) (*ClaimKeysResponse, error) {
	response, raw, err := c.doFederationRequest(http.MethodPost, c.federationBaseURL(server)+"/_matrix/federation/v1/user/keys/claim", server, map[string]any{"one_time_keys": requested})
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, &federationHTTPError{Status: response.StatusCode, Message: "remote key claim failed"}
	}
	var remote ClaimKeysResponse
	if err = json.Unmarshal(raw, &remote); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	result := &ClaimKeysResponse{OneTimeKeys: map[string]map[string]map[string]json.RawMessage{}}
	for user, devices := range remote.OneTimeKeys {
		requestedDevices, ok := requested[user]
		if !ok {
			continue
		}
		for device, keys := range devices {
			algorithm, ok := requestedDevices[device]
			if !ok {
				continue
			}
			if len(keys) > 1 {
				return nil, fmt.Errorf("remote returned more than one one-time key")
			}
			for keyID, key := range keys {
				if !strings.HasPrefix(keyID, algorithm+":") {
					return nil, fmt.Errorf("remote one-time key algorithm mismatch")
				}
				if result.OneTimeKeys[user] == nil {
					result.OneTimeKeys[user] = map[string]map[string]json.RawMessage{}
				}
				result.OneTimeKeys[user][device] = map[string]json.RawMessage{keyID: key}
			}
		}
	}
	return result, nil
}
