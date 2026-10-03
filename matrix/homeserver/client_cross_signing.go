package homeserver

// Client cross-signing stores public keys only. It deliberately does not use
// CrossSigningManager.GenerateCrossSigningKeys or any server-held private key.
import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type signingUpload struct {
	Master map[string]any `json:"master_key"`
	Self   map[string]any `json:"self_signing_key"`
	User   map[string]any `json:"user_signing_key"`
	Auth   *signingAuth   `json:"auth"`
}
type signingAuth struct {
	Type       string `json:"type"`
	Session    string `json:"session"`
	Password   string `json:"password"`
	User       string `json:"user"`
	Identifier struct {
		Type string `json:"type"`
		User string `json:"user"`
	} `json:"identifier"`
}
type signingChallenge struct {
	User, Device string
	Hash         [32]byte
	Expires      time.Time
}

var signingChallenges = struct {
	sync.Mutex
	Sessions map[string]signingChallenge
}{Sessions: make(map[string]signingChallenge)}

func signedJSON(object map[string]any) ([]byte, error) {
	copy := make(map[string]any, len(object))
	for k, v := range object {
		if k != "signatures" && k != "unsigned" {
			copy[k] = v
		}
	}
	return CanonicalJSON(copy)
}
func decodeSigningPublic(key map[string]any, user, usage string) (string, ed25519.PublicKey, error) {
	if key["user_id"] != user {
		return "", nil, errors.New("key user_id does not match authenticated user")
	}
	raw, err := json.Marshal(key)
	if err != nil {
		return "", nil, err
	}
	var parsed CrossSigningKey
	if err = json.Unmarshal(raw, &parsed); err != nil {
		return "", nil, err
	}
	if len(parsed.Usage) != 1 || parsed.Usage[0] != usage || len(parsed.Keys) != 1 {
		return "", nil, errors.New("invalid cross-signing usage or keys")
	}
	for id, encoded := range parsed.Keys {
		value, err := base64.RawStdEncoding.DecodeString(encoded)
		if err != nil || len(value) != ed25519.PublicKeySize || id != "ed25519:"+encoded {
			return "", nil, errors.New("invalid Ed25519 cross-signing public key")
		}
		return id, ed25519.PublicKey(value), nil
	}
	return "", nil, errors.New("missing public key")
}
func verifyObjectSignature(object map[string]any, user, keyID string, public ed25519.PublicKey) bool {
	raw, err := json.Marshal(object["signatures"])
	if err != nil {
		return false
	}
	var signatures map[string]map[string]string
	if json.Unmarshal(raw, &signatures) != nil {
		return false
	}
	signature, err := base64.RawStdEncoding.DecodeString(signatures[user][keyID])
	if err != nil || len(signature) != ed25519.SignatureSize {
		return false
	}
	canonical, err := signedJSON(object)
	return err == nil && ed25519.Verify(public, canonical, signature)
}
func loadClientSigningKey(conn *gorm.DB, user, usage string) (*MatrixCrossSigningKey, map[string]any, error) {
	record := &MatrixCrossSigningKey{}
	if err := conn.Where("user_id = ? AND key_type = ? AND is_active = ?", user, usage, true).Order("id DESC").First(record).Error; err != nil {
		return nil, nil, err
	}
	var object map[string]any
	err := json.Unmarshal([]byte(record.KeyData), &object)
	if err != nil {
		return nil, nil, err
	}
	object["user_id"] = user
	return record, object, err
}
func signingUIA(c *gin.Context, request *signingUpload) bool {
	return signingUIAWithVerifier(c, request, GetHomeServer().Auth.VerifyUserPassword)
}

func signingUIAWithVerifier(c *gin.Context, request *signingUpload, verifyPassword func(string, string) bool) bool {
	user, device := getUserID(c), c.GetString("device_id")
	payload := *request
	payload.Auth = nil
	raw, _ := json.Marshal(payload)
	hash := sha256.Sum256(raw)
	now := time.Now()
	signingChallenges.Lock()
	for id, entry := range signingChallenges.Sessions {
		if now.After(entry.Expires) {
			delete(signingChallenges.Sessions, id)
		}
	}
	session := ""
	valid := false
	if request.Auth != nil {
		session = request.Auth.Session
		entry, ok := signingChallenges.Sessions[session]
		valid = ok && entry.User == user && entry.Device == device && entry.Hash == hash
	}
	if !valid {
		session = uuid.NewString()
		signingChallenges.Sessions[session] = signingChallenge{User: user, Device: device, Hash: hash, Expires: now.Add(10 * time.Minute)}
	}
	signingChallenges.Unlock()
	if valid && request.Auth.Type == "m.login.password" && (request.Auth.Identifier.Type == "" || request.Auth.Identifier.Type == "m.id.user") {
		identifier := request.Auth.Identifier.User
		if identifier == "" {
			identifier = request.Auth.User
		}
		local, _, _ := ParseMatrixUserID(user)
		if (identifier == "" || identifier == user || identifier == local) && verifyPassword(user, request.Auth.Password) {
			signingChallenges.Lock()
			// Password verification runs outside the lock. Recheck and consume
			// atomically: another request may have used this session meanwhile.
			entry, exists := signingChallenges.Sessions[session]
			if exists && entry.User == user && entry.Device == device && entry.Hash == hash && time.Now().Before(entry.Expires) {
				delete(signingChallenges.Sessions, session)
				signingChallenges.Unlock()
				return true
			}
			session = uuid.NewString()
			signingChallenges.Sessions[session] = signingChallenge{User: user, Device: device, Hash: hash, Expires: time.Now().Add(10 * time.Minute)}
			signingChallenges.Unlock()
		}
	}
	c.JSON(http.StatusUnauthorized, gin.H{"flows": []gin.H{{"stages": []string{"m.login.password"}}}, "params": gin.H{}, "session": session})
	return false
}
func handleDeviceSigningUpload(c *gin.Context) {
	var request signingUpload
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(400, gin.H{"errcode": "M_BAD_JSON", "error": "Invalid JSON"})
		return
	}
	user := getUserID(c)
	if user == "" {
		c.JSON(401, gin.H{"errcode": "M_MISSING_TOKEN"})
		return
	}
	if request.Master == nil && request.Self == nil && request.User == nil {
		c.JSON(400, gin.H{"errcode": "M_MISSING_PARAM", "error": "No cross-signing keys supplied"})
		return
	}
	if !signingUIA(c, &request) {
		return
	}
	err := storeClientSigningKeys(db.GetDB(), user, &request)
	if err != nil {
		c.JSON(400, gin.H{"errcode": "M_INVALID_PARAM", "error": err.Error()})
		return
	}
	if hs := GetHomeServer(); hs.DeviceList != nil {
		_ = hs.DeviceList.RecordDeviceChange(user, c.GetString("device_id"), "changed")
	}
	c.JSON(200, gin.H{})
}
func storeClientSigningKeys(conn *gorm.DB, user string, request *signingUpload) error {
	return conn.Transaction(func(tx *gorm.DB) error {
		// Serialize rotations by account, including uploads from different devices.
		local, _, ok := ParseMatrixUserID(user)
		if !ok {
			return errors.New("invalid user ID")
		}
		var account models.Account
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("username = ? AND domain IS NULL", local).First(&account).Error; err != nil {
			return err
		}
		master := request.Master
		if master == nil {
			_, existing, err := loadClientSigningKey(tx, user, "master")
			if err != nil {
				return errors.New("master key required")
			}
			master = existing
		}
		masterID, masterPublic, err := decodeSigningPublic(master, user, "master")
		if err != nil {
			return err
		}
		for usage, key := range map[string]map[string]any{"self_signing": request.Self, "user_signing": request.User} {
			if key == nil {
				continue
			}
			if _, _, err := decodeSigningPublic(key, user, usage); err != nil {
				return err
			}
			if !verifyObjectSignature(key, user, masterID, masterPublic) {
				return fmt.Errorf("invalid master signature on %s key", usage)
			}
		}
		// A changed master invalidates its old subordinate keys.
		if request.Master != nil {
			_, old, loadErr := loadClientSigningKey(tx, user, "master")
			if loadErr != nil && !errors.Is(loadErr, gorm.ErrRecordNotFound) {
				return loadErr
			}
			oldID, _, _ := decodeSigningPublic(old, user, "master")
			if oldID != masterID {
				if err := tx.Model(&MatrixCrossSigningKey{}).Where("user_id = ? AND is_active = ?", user, true).Update("is_active", false).Error; err != nil {
					return err
				}
			}
		}
		for usage, key := range map[string]map[string]any{"master": request.Master, "self_signing": request.Self, "user_signing": request.User} {
			if key == nil {
				continue
			}
			encoded, err := json.Marshal(key)
			if err != nil {
				return err
			}
			if err := tx.Model(&MatrixCrossSigningKey{}).Where("user_id = ? AND key_type = ? AND is_active = ?", user, usage, true).Update("is_active", false).Error; err != nil {
				return err
			}
			if err := tx.Create(&MatrixCrossSigningKey{UserID: user, KeyType: usage, KeyData: string(encoded), IsActive: true}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
func appendClientSigningKeys(response *QueryKeysResponse, users map[string][]string, requester string) error {
	response.MasterKeys = make(map[string]*CrossSigningKey)
	response.SelfSigningKeys = make(map[string]*CrossSigningKey)
	response.UserSigningKeys = make(map[string]*CrossSigningKey)
	for user := range users {
		for _, usage := range []string{"master", "self_signing", "user_signing"} {
			if usage == "user_signing" && user != requester {
				continue
			}
			_, object, err := loadClientSigningKey(db.GetDB(), user, usage)
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			raw, _ := json.Marshal(object)
			var key CrossSigningKey
			if err := json.Unmarshal(raw, &key); err != nil {
				return err
			}
			switch usage {
			case "master":
				response.MasterKeys[user] = &key
			case "self_signing":
				response.SelfSigningKeys[user] = &key
			case "user_signing":
				response.UserSigningKeys[user] = &key
			}
		}
	}
	return nil
}

// uploadObjectSignatures accepts signatures only for the exact already-stored
// public object, and only from an authenticated user's known signing key.
func uploadObjectSignatures(conn *gorm.DB, caller, target, objectID string, object map[string]any) error {
	return conn.Transaction(func(tx *gorm.DB) error {
		var record MatrixDeviceKey
		var signingRecord *MatrixCrossSigningKey
		var stored map[string]any
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ? AND device_id = ?", target, objectID).First(&record).Error
		isDevice := err == nil
		if isDevice {
			if caller != target {
				return errors.New("cannot sign another user's device")
			}
			if err = json.Unmarshal([]byte(record.DeviceKey), &stored); err != nil {
				return err
			}
		} else {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			signingRecord, stored, err = loadClientSigningKey(tx.Clauses(clause.Locking{Strength: "UPDATE"}), target, "master")
			if err != nil {
				return err
			}
			keyID, _, err := decodeSigningPublic(stored, target, "master")
			if err != nil || strings.TrimPrefix(keyID, "ed25519:") != objectID {
				return errors.New("unknown signing key")
			}
		}
		incoming, err := signedJSON(object)
		if err != nil {
			return err
		}
		existing, err := signedJSON(stored)
		if err != nil {
			return err
		}
		if string(incoming) != string(existing) {
			return errors.New("signed object does not match stored key")
		}
		raw, _ := json.Marshal(object["signatures"])
		var submitted map[string]map[string]string
		if json.Unmarshal(raw, &submitted) != nil || len(submitted[caller]) == 0 {
			return errors.New("missing signature")
		}
		accepted := make(map[string]string)
		for keyID, signature := range submitted[caller] {
			var public ed25519.PublicKey
			usage := "user_signing"
			if isDevice {
				usage = "self_signing"
			}
			if caller == target {
				var signer MatrixDeviceKey
				if err := tx.Where("user_id = ? AND device_id = ?", caller, strings.TrimPrefix(keyID, "ed25519:")).First(&signer).Error; err == nil {
					var device DeviceKeys
					_ = json.Unmarshal([]byte(signer.DeviceKey), &device)
					bytes, _ := base64.RawStdEncoding.DecodeString(device.Keys[keyID])
					if len(bytes) == ed25519.PublicKeySize {
						public = bytes
					}
				}
			}
			if caller == target && !isDevice {
				usage = "master"
			}
			if len(public) == 0 {
				_, key, err := loadClientSigningKey(tx, caller, usage)
				if err == nil {
					knownID, known, err := decodeSigningPublic(key, caller, usage)
					if err == nil && knownID == keyID {
						public = known
					}
				}
			}
			if len(public) != ed25519.PublicKeySize || !verifyObjectSignature(object, caller, keyID, public) {
				return errors.New("invalid signature")
			}
			accepted[keyID] = signature
		}
		raw, _ = json.Marshal(stored["signatures"])
		var signatures map[string]map[string]string
		_ = json.Unmarshal(raw, &signatures)
		if signatures == nil {
			signatures = make(map[string]map[string]string)
		}
		if signatures[caller] == nil {
			signatures[caller] = make(map[string]string)
		}
		for id, sig := range accepted {
			signatures[caller][id] = sig
		}
		stored["signatures"] = signatures
		encoded, err := json.Marshal(stored)
		if err != nil {
			return err
		}
		if isDevice {
			return tx.Model(&record).Update("device_key", string(encoded)).Error
		}
		return tx.Model(signingRecord).Update("key_data", string(encoded)).Error
	})
}
func handleClientSignaturesUpload(c *gin.Context) {
	var request map[string]map[string]map[string]any
	if c.ShouldBindJSON(&request) != nil {
		c.JSON(400, gin.H{"errcode": "M_BAD_JSON"})
		return
	}
	failures := make(map[string]map[string]gin.H)
	for user, objects := range request {
		for id, object := range objects {
			if err := uploadObjectSignatures(db.GetDB(), getUserID(c), user, id, object); err != nil {
				if failures[user] == nil {
					failures[user] = make(map[string]gin.H)
				}
				failures[user][id] = gin.H{"errcode": "M_INVALID_SIGNATURE", "error": err.Error()}
			}
		}
	}
	c.JSON(200, gin.H{"failures": failures})
}
