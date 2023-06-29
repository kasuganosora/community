package utils

import (
	"fmt"
	"strconv"
)

func ToInt(v any) int {
	switch v.(type) {
	case int:
		return v.(int)
	case int64:
		return int(v.(int64))
	case string:
		str := v.(string)
		if str == "" {
			return 0
		}

		ret, err := strconv.Atoi(str)
		if err != nil {
			return 0
		}

		return ret

	case float64:
		return int(v.(float64))
	case float32:
		return int(v.(float32))
	case bool:
		if v.(bool) {
			return 1
		}
		return 0
	default:
		return 0
	}
}

func ToInt64(v any) int64 {
	switch v.(type) {
	case int:
		return int64(v.(int))
	case int64:
		return v.(int64)
	case string:
		str := v.(string)
		if str == "" {
			return 0
		}

		ret, err := strconv.ParseInt(str, 10, 64)
		if err != nil {
			return 0
		}

		return ret

	case float64:
		return int64(v.(float64))
	case float32:
		return int64(v.(float32))
	case bool:
		if v.(bool) {
			return 1
		}
		return 0
	default:
		return 0
	}
}

func ToFloat64(v any) float64 {
	switch v.(type) {
	case int:
		return float64(v.(int))
	case int64:
		return float64(v.(int64))
	case string:
		str := v.(string)
		if str == "" {
			return 0
		}

		ret, err := strconv.ParseFloat(str, 64)
		if err != nil {
			return 0
		}

		return ret

	case float64:
		return v.(float64)
	case float32:
		return float64(v.(float32))
	case bool:
		if v.(bool) {
			return 1
		}
		return 0
	default:
		return 0
	}
}

func ToBool(v any) bool {
	switch v.(type) {
	case int:
		return v.(int) != 0
	case int64:
		return v.(int64) != 0
	case string:
		str := v.(string)
		if str == "" {
			return false
		}

		ret, err := strconv.ParseBool(str)
		if err != nil {
			return false
		}

		return ret

	case float64:
		return v.(float64) != 0
	case float32:
		return v.(float32) != 0
	case bool:
		return v.(bool)
	default:
		return false
	}
}

func ToString(v any) string {
	switch v.(type) {
	case int:
		return strconv.Itoa(v.(int))
	case int64:
		return strconv.FormatInt(v.(int64), 10)
	case string:
		return v.(string)
	case float64:
		return strconv.FormatFloat(v.(float64), 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(v.(float32)), 'f', -1, 32)
	case bool:
		return strconv.FormatBool(v.(bool))
	default:
		return fmt.Sprintf("%v", v)
	}
}
