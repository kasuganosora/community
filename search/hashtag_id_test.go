package search

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHashtagDocumentIDIsAlwaysAValidMeilisearchID(t *testing.T) {
	valid := regexp.MustCompile(`^[A-Za-z0-9_-]{1,511}$`)
	assert.Equal(t, "AirAlert", HashtagDocumentID("AirAlert"), "ASCII names keep their existing ids")
	for _, name := range []string{"你好", "café", "a·b", "AirAlert"} {
		id := HashtagDocumentID(name)
		assert.Regexp(t, valid, id, name)
	}
	assert.Equal(t, HashtagDocumentID("你好"), HashtagDocumentID("你好"))
	assert.NotEqual(t, HashtagDocumentID("你好"), HashtagDocumentID("您好"))
}
