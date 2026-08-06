package utils

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5" //nolint:gosec // G501: reviewed and safe
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"github.com/pkg/errors"
	"reflect"
	"strings"
)

// AesEncrypt AES加密
// iv 从Key sha256后中截取前16位
// 加密结果是base64编码.
func AesEncrypt(data []byte, key string) (encrypted string, err error) {
	keySha1 := sha256.Sum256([]byte(key))

	block, err := aes.NewCipher(keySha1[:])
	if err != nil {
		return
	}

	data = Pkcs7padding(data, block.BlockSize())
	iv := keySha1[:block.BlockSize()]

	mode := cipher.NewCBCEncrypter(block, iv)
	encryptedData := make([]byte, len(data))
	mode.CryptBlocks(encryptedData, data)

	encrypted = base64.StdEncoding.EncodeToString(encryptedData)

	return
}

// AesDecrypt AES解密
// iv 从Key sha256后中截取前16位
// 解密前需要base64解码.
func AesDecrypt(data string, key string) (decrypted []byte, err error) {
	keySha1 := sha256.Sum256([]byte(key))

	block, err := aes.NewCipher(keySha1[:])
	if err != nil {
		return
	}

	encryptedData, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return
	}

	// 校验密文长度必须是 block size 的整数倍，否则 CryptBlocks 会 panic
	blockSize := block.BlockSize()
	if len(encryptedData) == 0 || len(encryptedData)%blockSize != 0 {
		err = errors.New("ciphertext is not a multiple of the block size")

		return
	}

	iv := keySha1[:blockSize]

	mode := cipher.NewCBCDecrypter(block, iv)
	decrypted = make([]byte, len(encryptedData))
	mode.CryptBlocks(decrypted, encryptedData)

	decrypted, err = Pkcs7unpadding(decrypted)

	return
}

func Pkcs7padding(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	padtext := bytes.Repeat([]byte{byte(padding)}, padding) //nolint:gosec // G115: reviewed and safe

	return append(data, padtext...)
}

// Pkcs7unpadding 去除 PKCS#7 填充并校验完整性。
// 若填充无效返回 error，防止填充 oracle 攻击。
func Pkcs7unpadding(data []byte) ([]byte, error) {
	length := len(data)
	if length == 0 {
		return data, nil
	}

	unpadding := int(data[length-1])
	if unpadding == 0 || unpadding > length {
		return nil, errors.New("invalid pkcs7 padding length")
	}

	// 校验末尾 unpadding 个字节全部等于 unpadding，防止填充 oracle 攻击
	padStart := length - unpadding
	for i := padStart; i < length; i++ {
		if data[i] != byte(unpadding) {
			return nil, errors.New("invalid pkcs7 padding bytes")
		}
	}

	return data[:padStart], nil
}

// AesEncryptStruct AES加密结构体
// 加密带有 encrypt tag 的 string 类型的字段.
func AesEncryptStruct(data any, key string) (err error) {
	elm := reflect.ValueOf(data)
	if elm.Kind() == reflect.Pointer {
		elm = elm.Elem()
	}

	if elm.Kind() != reflect.Struct {
		err = errors.New("data must be a struct")

		return err
	}

	eType := elm.Type()

	for i := range eType.NumField() {
		field := eType.Field(i)
		if field.Type.Kind() != reflect.String {
			continue
		}

		if field.Tag.Get("encrypt") != "true" {
			continue
		}

		fieldValue := elm.Field(i)
		fieldContent := fieldValue.String()

		if fieldContent == "" {
			continue
		}

		var encrypted string

		encrypted, err = AesEncrypt([]byte(fieldContent), key)
		if err != nil {
			return err
		}

		fieldValue.SetString(encrypted)
	}

	return err
}

// AesDecryptStruct AES解密结构体
// 解密带有 encrypt tag 的 string 类型的字段.
func AesDecryptStruct(data any, key string) (err error) {
	elm := reflect.ValueOf(data)
	if elm.Kind() == reflect.Pointer {
		elm = elm.Elem()
	}

	if elm.Kind() != reflect.Struct {
		err = errors.New("data must be a struct")

		return err
	}

	eType := elm.Type()

	for i := range eType.NumField() {
		field := eType.Field(i)
		if field.Type.Kind() != reflect.String {
			continue
		}

		if field.Tag.Get("encrypt") != "true" {
			continue
		}

		fieldValue := elm.Field(i)
		fieldContent := fieldValue.String()

		if fieldContent == "" {
			continue
		}

		var decrypted []byte

		decrypted, err = AesDecrypt(fieldContent, key)
		if err != nil {
			return errors.WithMessagef(err, "field %s decrypt failed", field.Name)
		}

		fieldValue.SetString(string(decrypted))
	}

	return err
}

// GetEncryptKey 转换加密key.
func GetEncryptKey(sourceKey string, salt string) string {
	keyMd5 := md5.Sum([]byte(sourceKey + salt)) //nolint:gosec // G401: reviewed and safe

	return strings.ToUpper(hex.EncodeToString(keyMd5[:]))
}
