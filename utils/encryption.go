package utils

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"github.com/pkg/errors"
	"reflect"
	"strings"
)

// AesEncrypt AES加密
// iv 从Key sha256后中截取前16位
// 加密结果是base64编码
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
// 解密前需要base64解码
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

	iv := keySha1[:block.BlockSize()]

	mode := cipher.NewCBCDecrypter(block, iv)
	decrypted = make([]byte, len(encryptedData))
	mode.CryptBlocks(decrypted, encryptedData)

	decrypted = Pkcs7unpadding(decrypted)
	return
}

func Pkcs7padding(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	padtext := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(data, padtext...)
}

func Pkcs7unpadding(data []byte) []byte {
	length := len(data)
	unpadding := int(data[length-1])
	return data[:(length - unpadding)]
}

// AesEncryptStruct AES加密结构体
// 加密带有 encrypt tag 的 string 类型的字段
func AesEncryptStruct(data any, key string) (err error) {
	elm := reflect.ValueOf(data)
	if elm.Kind() == reflect.Ptr {
		elm = elm.Elem()
	}

	if elm.Kind() != reflect.Struct {
		err = errors.New("data must be a struct")
		return
	}

	eType := elm.Type()

	for i := 0; i < eType.NumField(); i++ {
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

	return
}

// AesDecryptStruct AES解密结构体
// 解密带有 encrypt tag 的 string 类型的字段
func AesDecryptStruct(data any, key string) (err error) {
	elm := reflect.ValueOf(data)
	if elm.Kind() == reflect.Ptr {
		elm = elm.Elem()
	}

	if elm.Kind() != reflect.Struct {
		err = errors.New("data must be a struct")
		return
	}

	eType := elm.Type()

	for i := 0; i < eType.NumField(); i++ {
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

	return
}

// GetEncryptKey 转换加密key
func GetEncryptKey(sourceKey string, salt string) string {
	keyMd5 := md5.Sum([]byte(sourceKey + salt))
	return strings.ToUpper(hex.EncodeToString(keyMd5[:]))
}
