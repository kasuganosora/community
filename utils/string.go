package utils

import (
	"crypto/rand"
	"encoding/hex"
	mrand "math/rand"
)

// GetRandomString 获取随机字符串
func GetRandomString(l int) string {
	chars := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	charsLen := len(chars)
	var result []rune
	for i := 0; i < l; i++ {
		randInt := mrand.Int63n(int64(charsLen))
		result = append(result, rune(chars[randInt]))
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
