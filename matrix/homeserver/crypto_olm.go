package homeserver

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/awnumar/memguard"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
)

// ===== Olm 双棘轮加密实现（简化版）=====
// 注意：这是一个基于 Go 标准库的简化实现
// 对于生产环境，建议使用 mautrix/go 的 crypto 包
// 或使用纯 Go 实现的 Matrix 加密库

// OlmAccount Olm 账户.
type OlmAccount struct {
	IdentityKey *memguard.Enclave // Curve25519 身份密钥对
	SigningKey  *memguard.Enclave // Ed25519 签名密钥对
	OneTimeKeys []*OlmOneTimeKey  // 一次性密钥池
	NextKeyID   uint32            // 下一个密钥 ID
}

// OlmOneTimeKey Olm 一次性密钥.
type OlmOneTimeKey struct {
	KeyID      uint32
	PublicKey  []byte
	PrivateKey *memguard.Enclave
	Published  bool
	Used       bool
}

// OlmSession Olm 会话（双棘轮）.
type OlmSession struct {
	SessionID        string
	TheirIdentityKey []byte                       // 对方身份公钥
	TheirOTKey       []byte                       // 对方一次性公钥
	OurBaseKey       *memguard.Enclave            // 我们的基础密钥
	RootKey          *memguard.Enclave            // 根密钥
	ChainKeys        map[string]*memguard.Enclave // 发送/接收链密钥
	MessageIndex     uint32                       // 消息索引
	ReceivedCount    uint32                       // 接收计数
}

// OlmMessage Olm 加密消息.
type OlmMessage struct {
	Ciphertext string `json:"ciphertext"`
	MAC        string `json:"mac"`
	IV         string `json:"iv"`
}

// NewOlmAccount 创建新的 Olm 账户.
func NewOlmAccount() (*OlmAccount, error) {
	// 生成 Curve25519 身份密钥对
	identityPrivateKey := make([]byte, 32)
	if _, err := rand.Read(identityPrivateKey); err != nil {
		return nil, fmt.Errorf("failed to generate identity key: %w", err)
	}

	// 存储私钥（安全）
	identityKeyEnclave := memguard.NewEnclave(identityPrivateKey)
	memguard.WipeBytes(identityPrivateKey)

	return &OlmAccount{
		IdentityKey: identityKeyEnclave,
		OneTimeKeys: make([]*OlmOneTimeKey, 0),
		NextKeyID:   1,
	}, nil
}

// GenerateOneTimeKeys 生成一次性密钥.
func (acc *OlmAccount) GenerateOneTimeKeys(count int) error {
	for range count {
		// 生成 Curve25519 密钥对
		privateKey := make([]byte, 32)
		if _, err := rand.Read(privateKey); err != nil {
			return fmt.Errorf("failed to generate OTK private key: %w", err)
		}

		publicKey, err := curve25519.X25519(privateKey, curve25519.Basepoint)
		if err != nil {
			return fmt.Errorf("failed to derive OTK public key: %w", err)
		}

		otk := &OlmOneTimeKey{
			KeyID:      acc.NextKeyID,
			PublicKey:  publicKey,
			PrivateKey: memguard.NewEnclave(privateKey),
			Published:  false,
			Used:       false,
		}

		acc.OneTimeKeys = append(acc.OneTimeKeys, otk)
		acc.NextKeyID++

		memguard.WipeBytes(privateKey)
	}

	return nil
}

// GetUnpublishedOneTimeKeys 获取未发布的一次性密钥.
func (acc *OlmAccount) GetUnpublishedOneTimeKeys() map[string]string {
	keys := make(map[string]string)

	for _, otk := range acc.OneTimeKeys {
		if !otk.Published && !otk.Used {
			keyID := fmt.Sprintf("curve25519:%d", otk.KeyID)
			keys[keyID] = base64.RawStdEncoding.EncodeToString(otk.PublicKey)
		}
	}

	return keys
}

// MarkOneTimeKeysAsPublished 标记一次性密钥为已发布.
func (acc *OlmAccount) MarkOneTimeKeysAsPublished() {
	for _, otk := range acc.OneTimeKeys {
		if !otk.Used {
			otk.Published = true
		}
	}
}

// RemoveOneTimeKey 移除已使用的一次性密钥.
func (acc *OlmAccount) RemoveOneTimeKey(keyID uint32) {
	for i, otk := range acc.OneTimeKeys {
		if otk.KeyID == keyID {
			// 安全清除私钥
			if otk.PrivateKey != nil {
				buf, _ := otk.PrivateKey.Open()
				if buf != nil {
					buf.Destroy()
				}
			}
			// 从列表中移除
			acc.OneTimeKeys = append(acc.OneTimeKeys[:i], acc.OneTimeKeys[i+1:]...)

			return
		}
	}
}

// CreateOutboundSession 创建出站会话（Alice 模式）.
func (acc *OlmAccount) CreateOutboundSession(
	theirIdentityKey, theirOTKey []byte,
) (*OlmSession, error) {
	// 获取我们的身份私钥
	identityKeyBuf, err := acc.IdentityKey.Open()
	if err != nil {
		return nil, fmt.Errorf("failed to open identity key: %w", err)
	}

	defer identityKeyBuf.Destroy()

	ourIdentityPrivate := identityKeyBuf.Bytes()

	// 生成临时密钥对（Triple DH 的一部分）
	ourBasePrivate := make([]byte, 32)
	if _, err := rand.Read(ourBasePrivate); err != nil {
		return nil, fmt.Errorf("failed to generate base key: %w", err)
	}

	_, err = curve25519.X25519(ourBasePrivate, curve25519.Basepoint)
	if err != nil {
		return nil, fmt.Errorf("failed to derive base public key: %w", err)
	}

	// 执行 Triple Diffie-Hellman
	// DH1 = our_identity_private * their_ot_key
	dh1, err := curve25519.X25519(ourIdentityPrivate, theirOTKey)
	if err != nil {
		return nil, fmt.Errorf("dh1 failed: %w", err)
	}

	// DH2 = our_base_private * their_identity_key
	dh2, err := curve25519.X25519(ourBasePrivate, theirIdentityKey)
	if err != nil {
		return nil, fmt.Errorf("dh2 failed: %w", err)
	}

	// DH3 = our_base_private * their_ot_key
	dh3, err := curve25519.X25519(ourBasePrivate, theirOTKey)
	if err != nil {
		return nil, fmt.Errorf("dh3 failed: %w", err)
	}

	// 派生根密钥
	rootKey := deriveRootKey(dh1, dh2, dh3)

	// 派发送链密钥
	sendingChainKey := deriveChainKey(rootKey, 0)

	sessionID := generateSessionID()

	session := &OlmSession{
		SessionID:        sessionID,
		TheirIdentityKey: theirIdentityKey,
		TheirOTKey:       theirOTKey,
		OurBaseKey:       memguard.NewEnclave(ourBasePrivate),
		RootKey:          memguard.NewEnclave(rootKey),
		ChainKeys: map[string]*memguard.Enclave{
			"sending": sendingChainKey,
		},
		MessageIndex: 0,
	}

	memguard.WipeBytes(ourBasePrivate)
	memguard.WipeBytes(rootKey)

	return session, nil
}

// CreateInboundSession 创建入站会话（Bob 模式）.
func (acc *OlmAccount) CreateInboundSession(
	theirIdentityKey, theirBaseKey []byte,
	oneTimeKeyID uint32,
) (*OlmSession, error) {
	// 获取我们的身份私钥
	identityKeyBuf, err := acc.IdentityKey.Open()
	if err != nil {
		return nil, fmt.Errorf("failed to open identity key: %w", err)
	}

	defer identityKeyBuf.Destroy()

	ourIdentityPrivate := identityKeyBuf.Bytes()

	// 查找并使用一次性密钥
	var ourOTK *OlmOneTimeKey

	for _, otk := range acc.OneTimeKeys {
		if otk.KeyID == oneTimeKeyID && !otk.Used {
			ourOTK = otk

			break
		}
	}

	if ourOTK == nil {
		return nil, errors.New("one-time key not found or already used")
	}

	// 获取一次性私钥
	otkBuf, err := ourOTK.PrivateKey.Open()
	if err != nil {
		return nil, fmt.Errorf("failed to open OTK: %w", err)
	}

	defer otkBuf.Destroy()

	ourOTPrivate := otkBuf.Bytes()

	// 执行 Triple Diffie-Hellman
	// DH1 = our_ot_private * their_identity_key
	dh1, err := curve25519.X25519(ourOTPrivate, theirIdentityKey)
	if err != nil {
		return nil, fmt.Errorf("dh1 failed: %w", err)
	}

	// DH2 = our_identity_private * their_base_key
	dh2, err := curve25519.X25519(ourIdentityPrivate, theirBaseKey)
	if err != nil {
		return nil, fmt.Errorf("dh2 failed: %w", err)
	}

	// DH3 = our_ot_private * their_base_key
	dh3, err := curve25519.X25519(ourOTPrivate, theirBaseKey)
	if err != nil {
		return nil, fmt.Errorf("dh3 failed: %w", err)
	}

	// 派生根密钥
	rootKey := deriveRootKey(dh1, dh2, dh3)

	// 派生接收链密钥
	receivingChainKey := deriveChainKey(rootKey, 0)

	sessionID := generateSessionID()

	session := &OlmSession{
		SessionID:        sessionID,
		TheirIdentityKey: theirIdentityKey,
		RootKey:          memguard.NewEnclave(rootKey),
		ChainKeys: map[string]*memguard.Enclave{
			"receiving": receivingChainKey,
		},
		ReceivedCount: 0,
	}

	// 标记一次性密钥为已使用
	ourOTK.Used = true

	acc.RemoveOneTimeKey(oneTimeKeyID)

	memguard.WipeBytes(rootKey)

	return session, nil
}

// EncryptMessage 加密消息（使用当前发送链密钥）.
func (sess *OlmSession) EncryptMessage(plaintext []byte) (*OlmMessage, error) {
	// 获取当前发送链密钥
	chainKeyBuf, err := sess.ChainKeys["sending"].Open()
	if err != nil {
		return nil, fmt.Errorf("failed to open chain key: %w", err)
	}

	defer chainKeyBuf.Destroy()

	chainKey := chainKeyBuf.Bytes()

	// 派生消息密钥和下一个链密钥
	messageKey, nextChainKey := deriveMessageKey(chainKey, sess.MessageIndex)
	defer memguard.WipeBytes(messageKey)

	// 更新链密钥
	sess.ChainKeys["sending"] = memguard.NewEnclave(nextChainKey)
	sess.MessageIndex++

	// 生成 IV
	iv := make([]byte, 16)
	if _, err := rand.Read(iv); err != nil {
		return nil, fmt.Errorf("failed to generate IV: %w", err)
	}

	// 加密消息
	ciphertext, err := encryptAESGCM(plaintext, messageKey, iv)
	if err != nil {
		return nil, fmt.Errorf("encryption failed: %w", err)
	}

	// 计算 MAC
	mac := computeMAC(ciphertext, messageKey)

	return &OlmMessage{
		Ciphertext: base64.RawStdEncoding.EncodeToString(ciphertext),
		MAC:        base64.RawStdEncoding.EncodeToString(mac),
		IV:         base64.RawStdEncoding.EncodeToString(iv),
	}, nil
}

// DecryptMessage 解密消息.
func (sess *OlmSession) DecryptMessage(msg *OlmMessage) ([]byte, error) {
	// 获取当前接收链密钥
	chainKeyBuf, err := sess.ChainKeys["receiving"].Open()
	if err != nil {
		return nil, fmt.Errorf("failed to open chain key: %w", err)
	}

	defer chainKeyBuf.Destroy()

	chainKey := chainKeyBuf.Bytes()

	// 派生消息密钥
	messageKey, nextChainKey := deriveMessageKey(chainKey, sess.ReceivedCount)
	defer memguard.WipeBytes(messageKey)

	// 更新链密钥
	sess.ChainKeys["receiving"] = memguard.NewEnclave(nextChainKey)
	sess.ReceivedCount++

	// 解码消息
	ciphertext, err := base64.RawStdEncoding.DecodeString(msg.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("failed to decode ciphertext: %w", err)
	}

	iv, err := base64.RawStdEncoding.DecodeString(msg.IV)
	if err != nil {
		return nil, fmt.Errorf("failed to decode IV: %w", err)
	}

	expectedMAC, err := base64.RawStdEncoding.DecodeString(msg.MAC)
	if err != nil {
		return nil, fmt.Errorf("failed to decode MAC: %w", err)
	}

	// 验证 MAC
	computedMAC := computeMAC(ciphertext, messageKey)
	if !hmac.Equal(computedMAC, expectedMAC) {
		return nil, errors.New("MAC verification failed")
	}

	// 解密消息
	plaintext, err := decryptAESGCM(ciphertext, messageKey, iv)
	if err != nil {
		return nil, fmt.Errorf("decryption failed: %w", err)
	}

	return plaintext, nil
}

// ===== 密钥派生函数 =====

// deriveRootKey 派生根密钥.
func deriveRootKey(dh1, dh2, dh3 []byte) []byte {
	// 连接所有 DH 结果
	input := make([]byte, 0, len(dh1)+len(dh2)+len(dh3))
	input = append(input, dh1...)
	input = append(input, dh2...)
	input = append(input, dh3...)

	// 使用 HKDF 派生根密钥
	hkdfReader := hkdf.New(sha256.New, input, nil, []byte("OlmRootKey"))

	rootKey := make([]byte, 32)

	if _, err := hkdfReader.Read(rootKey); err != nil {
		panic(fmt.Sprintf("hkdf failed: %v", err))
	}

	return rootKey
}

// deriveChainKey 派生链密钥.
func deriveChainKey(rootKey []byte, index uint32) *memguard.Enclave {
	info := fmt.Sprintf("OlmChainKey|%d", index)
	hkdfReader := hkdf.New(sha256.New, rootKey, nil, []byte(info))

	chainKey := make([]byte, 32)

	if _, err := hkdfReader.Read(chainKey); err != nil {
		panic(fmt.Sprintf("hkdf failed: %v", err))
	}

	return memguard.NewEnclave(chainKey)
}

// deriveMessageKey 派生消息密钥.
func deriveMessageKey(chainKey []byte, index uint32) (messageKey, nextChainKey []byte) {
	// 派生消息密钥
	info := fmt.Sprintf("OlmMessageKey|%d", index)
	hkdfReader := hkdf.New(sha256.New, chainKey, nil, []byte(info))

	messageKey = make([]byte, 32)

	if _, err := hkdfReader.Read(messageKey); err != nil {
		panic(fmt.Sprintf("hkdf failed: %v", err))
	}

	// 派生下一个链密钥
	nextChainKey = make([]byte, 32)

	nextReader := hkdf.New(sha256.New, chainKey, nil, []byte("OlmNextChainKey"))

	if _, err := nextReader.Read(nextChainKey); err != nil {
		panic(fmt.Sprintf("hkdf failed: %v", err))
	}

	return
}

// ===== 加密/解密函数 =====

// encryptAESGCM 使用 AES-GCM 加密.
func encryptAESGCM(plaintext, key, iv []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	ciphertext := gcm.Seal(nil, iv, plaintext, nil)

	return ciphertext, nil
}

// decryptAESGCM 使用 AES-GCM 解密.
func decryptAESGCM(ciphertext, key, iv []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	plaintext, err := gcm.Open(nil, iv, ciphertext, nil)
	if err != nil {
		return nil, err
	}

	return plaintext, nil
}

// computeMAC 计算 MAC.
func computeMAC(data, key []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)

	return mac.Sum(nil)[:16] // 使用 128 位 MAC
}

// generateSessionID 生成会话 ID.
func generateSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)

	return base64.RawStdEncoding.EncodeToString(b)
}

// ===== Megolm 群聊加密（简化实现）=====

// MegolmSession Megolm 会话（用于群聊）.
type MegolmSession struct {
	SessionID    string
	RoomID       string
	SessionKey   *memguard.Enclave // 会话密钥
	MessageIndex uint32
	CreationTime time.Time
}

// NewMegolmSession 创建新的 Megolm 会话.
func NewMegolmSession(roomID string) (*MegolmSession, error) {
	// 生成随机会话密钥
	sessionKey := make([]byte, 32)
	if _, err := rand.Read(sessionKey); err != nil {
		return nil, fmt.Errorf("failed to generate session key: %w", err)
	}

	sessionID := generateSessionID()

	return &MegolmSession{
		SessionID:    sessionID,
		RoomID:       roomID,
		SessionKey:   memguard.NewEnclave(sessionKey),
		MessageIndex: 0,
		CreationTime: time.Now(),
	}, nil
}

// Encrypt 加密群聊消息.
func (ms *MegolmSession) Encrypt(plaintext []byte) (*OlmMessage, error) {
	// 获取会话密钥
	sessionKeyBuf, err := ms.SessionKey.Open()
	if err != nil {
		return nil, fmt.Errorf("failed to open session key: %w", err)
	}

	defer sessionKeyBuf.Destroy()

	sessionKey := sessionKeyBuf.Bytes()

	// 派生消息密钥
	messageKey := deriveMegolmMessageKey(sessionKey, ms.MessageIndex)
	defer memguard.WipeBytes(messageKey)

	ms.MessageIndex++

	// 生成 IV
	iv := make([]byte, 16)
	if _, err := rand.Read(iv); err != nil {
		return nil, fmt.Errorf("failed to generate IV: %w", err)
	}

	// 加密
	ciphertext, err := encryptAESGCM(plaintext, messageKey, iv)
	if err != nil {
		return nil, fmt.Errorf("encryption failed: %w", err)
	}

	// 计算 MAC
	mac := computeMAC(ciphertext, messageKey)

	return &OlmMessage{
		Ciphertext: base64.RawStdEncoding.EncodeToString(ciphertext),
		MAC:        base64.RawStdEncoding.EncodeToString(mac),
		IV:         base64.RawStdEncoding.EncodeToString(iv),
	}, nil
}

// deriveMegolmMessageKey 派生 Megolm 消息密钥.
func deriveMegolmMessageKey(sessionKey []byte, index uint32) []byte {
	info := fmt.Sprintf("MegolmMessageKey|%d", index)
	hkdfReader := hkdf.New(sha256.New, sessionKey, nil, []byte(info))

	messageKey := make([]byte, 32)

	if _, err := hkdfReader.Read(messageKey); err != nil {
		panic(fmt.Sprintf("hkdf failed: %v", err))
	}

	return messageKey
}

// ExportSessionKey 导出会话密钥（用于密钥共享）.
func (ms *MegolmSession) ExportSessionKey() string {
	buf, err := ms.SessionKey.Open()
	if err != nil {
		return ""
	}
	defer buf.Destroy()

	return base64.RawStdEncoding.EncodeToString(buf.Bytes())
}

// ===== Olm 账户存储 =====

// OlmAccountStore Olm 账户存储.
type OlmAccountStore struct{}

// SaveOlmAccount 保存 Olm 账户.
func (store *OlmAccountStore) SaveOlmAccount(userID, deviceID string, account *OlmAccount) error {
	// 序列化账户数据
	data := map[string]any{
		"next_key_id": account.NextKeyID,
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return err
	}

	// 保存到数据库
	// 这里应该有一个专门的表来存储 Olm 账户
	_ = jsonData

	return nil
}
