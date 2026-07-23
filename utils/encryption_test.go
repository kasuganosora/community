package utils

import (
	"encoding/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAesEncryptAndDecrypt(t *testing.T) {
	key := "1234567890123456Abcdefgh"
	data := []byte("Hello World 中文！123 你好")

	encrypted, err := AesEncrypt(data, key)
	require.NoError(t, err)
	assert.NotEmpty(t, encrypted)
	print(encrypted + "\n")

	decrypted, err := AesDecrypt(encrypted, key)
	require.NoError(t, err)
	assert.Equal(t, data, decrypted)
}

func TestAesEncryptStructAndAesDecryptStruct(t *testing.T) {
	type TestStruct struct {
		Name       string `encrypt:"true"     json:"name"`
		NotEncrypt string `json:"not_encrypt"`
		Age        int    `json:"age"`
		Bio        string `encrypt:"true"     json:"bio"`
	}

	sourceTestObject := &TestStruct{
		Name:       "张三",
		NotEncrypt: "不加密",
		Age:        18,
		Bio:        "我是张三，今年18岁，我不加密",
	}

	key := "1234567890123456Abcdefgh"
	err := AesEncryptStruct(sourceTestObject, key)
	require.NoError(t, err)
	assert.NotEqual(t, "张三", sourceTestObject.Name)
	assert.NotEqual(t, "我是张三，今年18岁，我不加密", sourceTestObject.Bio)
	assert.Equal(t, "不加密", sourceTestObject.NotEncrypt)
	assert.Equal(t, 18, sourceTestObject.Age)

	jsonData, err := json.Marshal(sourceTestObject)
	require.NoError(t, err)
	print(string(jsonData) + "\n")

	err = AesDecryptStruct(sourceTestObject, key)
	require.NoError(t, err)
	assert.Equal(t, "张三", sourceTestObject.Name)
	assert.Equal(t, "我是张三，今年18岁，我不加密", sourceTestObject.Bio)
	assert.Equal(t, "不加密", sourceTestObject.NotEncrypt)
	assert.Equal(t, 18, sourceTestObject.Age)
}
