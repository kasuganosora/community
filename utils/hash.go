package utils

import (
	"encoding/hex"
	"hash"
)

type HashDriver interface {
	hash.Hash
}

func Hash[T HashDriver](h T, data any) string {
	switch data.(type) {
	case string:
		h.Write([]byte(data.(string)))
	case []byte:
		h.Write(data.([]byte))
	default:
		panic("hash unsupported type")
	}
	return hex.EncodeToString(h.Sum(nil))
}
