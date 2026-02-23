package homeserver

import (
	"bytes"
	"encoding/json"
	"math"
	"sort"
	"strconv"
)

// CanonicalJSON 将 JSON 对象转换为规范格式
// 遵循 Matrix 规范中的 JSON 规范化规则
func CanonicalJSON(v interface{}) ([]byte, error) {
	switch val := v.(type) {
	case map[string]interface{}:
		return canonicalJSONObject(val)
	case []interface{}:
		return canonicalJSONArray(val)
	case float64:
		// Matrix spec: canonical JSON must represent integer-valued numbers without decimal point
		// Go's json.Unmarshal parses all JSON numbers as float64 into interface{}
		if val == math.Trunc(val) && !math.IsInf(val, 0) && !math.IsNaN(val) {
			return []byte(strconv.FormatInt(int64(val), 10)), nil
		}
		return json.Marshal(val)
	default:
		return json.Marshal(val)
	}
}

// canonicalJSONObject 规范化 JSON 对象
func canonicalJSONObject(obj map[string]interface{}) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')

	// 按键排序
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for i, key := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}

		// 写入键
		keyBytes, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		buf.Write(keyBytes)
		buf.WriteByte(':')

		// 递归处理值
		valBytes, err := CanonicalJSON(obj[key])
		if err != nil {
			return nil, err
		}
		buf.Write(valBytes)
	}

	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// canonicalJSONArray 规范化 JSON 数组
func canonicalJSONArray(arr []interface{}) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('[')

	for i, val := range arr {
		if i > 0 {
			buf.WriteByte(',')
		}

		valBytes, err := CanonicalJSON(val)
		if err != nil {
			return nil, err
		}
		buf.Write(valBytes)
	}

	buf.WriteByte(']')
	return buf.Bytes(), nil
}

// EscapeJSONString 转义 JSON 字符串中的特殊字符
// 确保符合 Matrix 规范
func EscapeJSONString(s string) string {
	var buf bytes.Buffer
	for _, r := range s {
		switch r {
		case '"':
			buf.WriteString(`\"`)
		case '\\':
			buf.WriteString(`\\`)
		case '\b':
			buf.WriteString(`\b`)
		case '\f':
			buf.WriteString(`\f`)
		case '\n':
			buf.WriteString(`\n`)
		case '\r':
			buf.WriteString(`\r`)
		case '\t':
			buf.WriteString(`\t`)
		default:
			if r < 0x20 {
				// 控制字符使用 Unicode 转义
				buf.WriteString(`\u`)
				buf.WriteString(formatUnicode(uint16(r)))
			} else if r < 0x10000 {
				buf.WriteRune(r)
			} else {
				// 辅助平面的字符
				r1, r2 := utf16Encode(r)
				buf.WriteString(`\u`)
				buf.WriteString(formatUnicode(r1))
				buf.WriteString(`\u`)
				buf.WriteString(formatUnicode(r2))
			}
		}
	}
	return buf.String()
}

// formatUnicode 将 uint16 格式化为 4 位十六进制字符串
func formatUnicode(u uint16) string {
	hex := []byte{'0', '1', '2', '3', '4', '5', '6', '7', '8', '9', 'a', 'b', 'c', 'd', 'e', 'f'}
	result := make([]byte, 4)
	result[0] = hex[(u>>12)&0xf]
	result[1] = hex[(u>>8)&0xf]
	result[2] = hex[(u>>4)&0xf]
	result[3] = hex[u&0xf]
	return string(result)
}

// utf16Encode 将 Unicode 码点编码为 UTF-16 代理对
func utf16Encode(r rune) (uint16, uint16) {
	u := uint32(r) - 0x10000
	return uint16((u >> 10) + 0xD800), uint16((u & 0x3FF) + 0xDC00)
}

// SortJSONKeys 返回排序后的 JSON 对象键
func SortJSONKeys(obj map[string]interface{}) []string {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
