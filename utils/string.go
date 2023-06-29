package utils

import "math/rand"

// GetRandomString 获取随机字符串
func GetRandomString(l int) string {
	chars := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	charsLen := len(chars)
	var result []rune
	for i := 0; i < l; i++ {
		randInt := rand.Int63n(int64(charsLen))
		result = append(result, rune(chars[randInt]))
	}

	return string(result)
}
