package utils

import (
	"crypto/rand"
	"encoding/hex"
	"math/big"
)

// GetRandomString 获取随机字符串（使用 crypto/rand 确保安全性）
func GetRandomString(l int) string {
	chars := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	charsLen := int64(len(chars))
	result := make([]byte, l)
	for i := 0; i < l; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(charsLen))
		if err != nil {
			// 如果 crypto/rand 失败，回退到确定性的值（不应该发生）
			result[i] = chars[i%len(chars)]
			continue
		}
		result[i] = chars[n.Int64()]
	}

	return string(result)
}

// GetRandomHex 生成随机十六进制字符串（用于 token、secret 等）
func GetRandomHex(length int) string {
	bytes := make([]byte, length/2+1)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)[:length]
}

// FilterDuplicateString 过滤掉重复的字符串
func FilterDuplicateString(strs []string) []string {
	checkMap := make(map[string]bool)
	ret := make([]string, 0, len(strs))
	for _, str := range strs {
		if _, ok := checkMap[str]; ok {
			continue
		}
		checkMap[str] = true
		ret = append(ret, str)
	}

	return ret
}

func FilterDuplicateAndEmptyString(strs []string) []string {
	checkMap := make(map[string]bool)
	ret := make([]string, 0, len(strs))
	for _, str := range strs {
		if str == "" {
			continue
		}
		if _, ok := checkMap[str]; ok {
			continue
		}
		checkMap[str] = true
		ret = append(ret, str)
	}

	return ret
}
