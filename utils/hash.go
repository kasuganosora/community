package utils

import (
	"encoding/hex"
	"hash"
)

type HashDriver interface {
	hash.Hash
}

func Hash[T HashDriver](h T, data any) (result string) {
	// Reset at the end so the hash object can be safely reused across calls
	// (e.g. a shared sha256.New()). Without this, each call accumulates state
	// and produces a different digest for the same input.
	defer h.Reset()

	switch data := data.(type) {
	case string:
		_, _ = h.Write([]byte(data))
	case []byte:
		_, _ = h.Write(data)
	default:
		panic("hash unsupported type")
	}

	result = hex.EncodeToString(h.Sum(nil))
	return result
}
