package utils

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetRandomString(t *testing.T) {
	// 测试长度
	for _, length := range []int{1, 10, 32, 64} {
		result := GetRandomString(length)
		assert.Len(t, result, length, "Length should match")
	}

	// 测试随机性
	str1 := GetRandomString(32)
	str2 := GetRandomString(32)
	assert.NotEqual(t, str1, str2, "Two random strings should be different")

	// 测试字符集
	result := GetRandomString(100)

	chars := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

	for _, c := range result {
		assert.True(t, strings.ContainsRune(chars, c), "Character should be in allowed set")
	}
}

func TestFilterDuplicateString(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name:     "empty slice",
			input:    []string{},
			expected: []string{},
		},
		{
			name:     "no duplicates",
			input:    []string{"a", "b", "c"},
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "with duplicates",
			input:    []string{"a", "b", "a", "c", "b"},
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "all same",
			input:    []string{"x", "x", "x"},
			expected: []string{"x"},
		},
		{
			name:     "preserve order",
			input:    []string{"c", "a", "b", "a", "c"},
			expected: []string{"c", "a", "b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FilterDuplicateString(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFilterDuplicateAndEmptyString(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name:     "empty slice",
			input:    []string{},
			expected: []string{},
		},
		{
			name:     "with empty strings",
			input:    []string{"a", "", "b", ""},
			expected: []string{"a", "b"},
		},
		{
			name:     "with duplicates and empty",
			input:    []string{"a", "", "b", "a", "", "c", "b"},
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "all empty",
			input:    []string{"", "", ""},
			expected: []string{},
		},
		{
			name:     "preserve order",
			input:    []string{"c", "", "a", "b", "a", "c"},
			expected: []string{"c", "a", "b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FilterDuplicateAndEmptyString(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
