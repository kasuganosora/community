package homeserver

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/kasuganosora/community/db"
	gmsl "github.com/matrix-org/gomatrixserverlib"
	"github.com/stretchr/testify/require"
)

func TestFederationDeviceListPreservesCompleteSignedDeviceKeys(t *testing.T) {
	hs := createTestHomeServer(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixDeviceKey{}, &DeviceListStream{}))
	public, private := ed25519Key()
	user, device := "@alice:test.local", "DEVICE"
	wire, _ := json.Marshal(&DeviceKeys{UserID: user, DeviceID: device, Signatures: map[string]map[string]string{}, Algorithms: []string{"m.olm.v1.curve25519-aes-sha2"}, Keys: map[string]string{"ed25519:" + device: base64.RawStdEncoding.EncodeToString(public), "curve25519:" + device: "curve-public"}})
	signed, err := gmsl.SignJSON(user, gmsl.KeyID("ed25519:"+device), private, wire)
	require.NoError(t, err)
	require.NoError(t, db.GetDB().Create(&MatrixDeviceKey{UserID: user, DeviceID: device, DeviceKey: string(signed)}).Error)
	require.NoError(t, db.GetDB().Create(&DeviceListStream{UserID: user, StreamID: 7}).Error)
	service := NewDeviceListService(hs)
	response, err := service.GetUserDevices(user)
	require.NoError(t, err)
	require.Len(t, response.Devices, 1)
	encoded, err := json.Marshal(response)
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, json.Unmarshal(encoded, &result))
	entry := result["devices"].([]any)[0].(map[string]any)
	key := entry["keys"].(map[string]any)
	require.Equal(t, user, key["user_id"])
	require.Equal(t, device, key["device_id"])
	require.NotNil(t, key["signatures"])
	original, err := json.Marshal(key)
	require.NoError(t, err)
	require.NoError(t, gmsl.VerifyJSON(user, gmsl.KeyID("ed25519:"+device), public, original))
	edu, err := service.buildDeviceListUpdate(user, device, 8, "changed")
	require.NoError(t, err)
	require.NotNil(t, edu.PrevID)
	original, err = json.Marshal(edu.Keys)
	require.NoError(t, err)
	require.NoError(t, gmsl.VerifyJSON(user, gmsl.KeyID("ed25519:"+device), public, original))
	removed, err := service.buildDeviceListUpdate(user, device, 9, "removed")
	require.NoError(t, err)
	require.True(t, removed.Deleted)
	require.Nil(t, removed.Keys)
}
