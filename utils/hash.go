package utils

import (
	"encoding/hex"
	"hash"
)

type HashDriver interface {
	hash.Hash
}

func Hash[T HashDriver](h T, data any) string {
	switch data := data.(type) {
	case string:
		_, _ = h.Write([]byte(data))
	case []byte:
		_, _ = h.Write(data)
	default:
		panic("hash unsupported type")
	}

	return hex.EncodeToString(h.Sum(nil))
}
