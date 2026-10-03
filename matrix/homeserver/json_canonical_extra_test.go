package homeserver

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===== EscapeJSONString tests =====

func TestEscapeJSONString_SpecialChars(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty", "", ""},
		{"plain", "hello", "hello"},
		{"double quote", `"`, `\"`},
		{"backslash", `\`, `\\`},
		{"newline", "\n", `\n`},
		{"tab", "\t", `\t`},
		{"carriage return", "\r", `\r`},
		{"backspace", "\b", `\b`},
		{"form feed", "\f", `\f`},
		{"control char", string(rune(0x01)), `\u0001`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := EscapeJSONString(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestEscapeJSONString_UTF8(t *testing.T) {
	// Non-ASCII UTF-8 should pass through unchanged
	result := EscapeJSONString("日本語")
	assert.Equal(t, "日本語", result)
}

func TestEscapeJSONString_AstralPlane(t *testing.T) {
	// Canonical JSON keeps supplementary plane characters as UTF-8.
	result := EscapeJSONString("🎉") // U+1F389
	assert.Equal(t, "🎉", result)
}

// ===== SortJSONKeys tests =====

func TestSortJSONKeys(t *testing.T) {
	obj := map[string]any{
		"c": 3,
		"a": 1,
		"b": 2,
	}
	keys := SortJSONKeys(obj)
	assert.Equal(t, []string{"a", "b", "c"}, keys)
}

func TestSortJSONKeys_Empty(t *testing.T) {
	keys := SortJSONKeys(map[string]any{})
	assert.Empty(t, keys)
}

func TestSortJSONKeys_Single(t *testing.T) {
	keys := SortJSONKeys(map[string]any{"only": 1})
	assert.Equal(t, []string{"only"}, keys)
}

// ===== formatUnicode tests =====

func TestFormatUnicode(t *testing.T) {
	tests := []struct {
		name     string
		input    uint16
		expected string
	}{
		{"zero", 0x0000, "0000"},
		{"one", 0x0001, "0001"},
		{"ten", 0x000a, "000a"},
		{"max", 0xffff, "ffff"},
		{"newline", 0x000a, "000a"},
		{"form feed", 0x000c, "000c"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatUnicode(tt.input)
			assert.Equal(t, tt.expected, result)
			assert.Equal(t, 4, len(result), "should always be 4 hex chars")
		})
	}
}

// ===== utf16Encode tests =====

func TestUtf16Encode(t *testing.T) {
	// U+1F389 🎉 → D83C DF89
	hi, lo := utf16Encode(0x1F389)
	assert.Equal(t, uint16(0xD83C), hi)
	assert.Equal(t, uint16(0xDF89), lo)
}

func TestUtf16Encode_MinAstral(t *testing.T) {
	// U+10000 → D800 DC00
	hi, lo := utf16Encode(0x10000)
	assert.Equal(t, uint16(0xD800), hi)
	assert.Equal(t, uint16(0xDC00), lo)
}

func TestUtf16Encode_MaxAstral(t *testing.T) {
	// U+10FFFF → DBFF DFFF
	hi, lo := utf16Encode(0x10FFFF)
	assert.Equal(t, uint16(0xDBFF), hi)
	assert.Equal(t, uint16(0xDFFF), lo)
}

// ===== CanonicalJSON additional edge cases =====

func TestCanonicalJSON_PrimitiveTypes(t *testing.T) {
	// String
	result, err := CanonicalJSON("hello")
	require.NoError(t, err)
	assert.Equal(t, `"hello"`, string(result))

	// Bool
	result, err = CanonicalJSON(true)
	require.NoError(t, err)
	assert.Equal(t, "true", string(result))

	result, err = CanonicalJSON(false)
	require.NoError(t, err)
	assert.Equal(t, "false", string(result))

	// nil
	result, err = CanonicalJSON(nil)
	require.NoError(t, err)
	assert.Equal(t, "null", string(result))
}

func TestCanonicalJSON_IntegerNoDecimal(t *testing.T) {
	// Matrix spec: integer-valued floats should not have decimal point
	result, err := CanonicalJSON(float64(42))
	require.NoError(t, err)
	assert.Equal(t, "42", string(result))
	assert.NotContains(t, string(result), ".")
}

func TestCanonicalJSON_FloatKeepsDecimal(t *testing.T) {
	result, err := CanonicalJSON(float64(3.14))
	require.NoError(t, err)
	// Should be a float representation
	b, _ := json.Marshal(3.14)
	assert.Equal(t, string(b), string(result))
}

func TestCanonicalJSON_NestedArray(t *testing.T) {
	input := []any{float64(3), float64(1), float64(2)}
	result, err := CanonicalJSON(input)
	require.NoError(t, err)
	// Arrays preserve order (no sorting)
	assert.Equal(t, "[3,1,2]", string(result))
}

func TestCanonicalJSON_EmptyArray(t *testing.T) {
	input := []any{}
	result, err := CanonicalJSON(input)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(result))
}

func TestCanonicalJSON_EmptyMap(t *testing.T) {
	input := map[string]any{}
	result, err := CanonicalJSON(input)
	require.NoError(t, err)
	assert.Equal(t, "{}", string(result))
}

func TestCanonicalJSON_LargeInteger(t *testing.T) {
	// Large integers should remain as integers (no scientific notation)
	result, err := CanonicalJSON(float64(10000000000))
	require.NoError(t, err)
	assert.Equal(t, "10000000000", string(result))
}

func TestCanonicalJSON_NestedMapInArray(t *testing.T) {
	input := []any{
		map[string]any{"b": float64(2), "a": float64(1)},
	}
	result, err := CanonicalJSON(input)
	require.NoError(t, err)
	assert.Equal(t, `[{"a":1,"b":2}]`, string(result))
}
