package utils

import (
	"fmt"
	"github.com/pkg/errors"
	"reflect"
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

func StrToAny(str string, obj any) (err error) {
	rValue := reflect.ValueOf(obj).Elem()
	rType := rValue.Type()

	switch rType.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		var val int64
		val, err = strconv.ParseInt(str, 10, 64)
		if err != nil {
			return
		}
		rValue.SetInt(val)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		var val uint64
		val, err = strconv.ParseUint(str, 10, 64)
		if err != nil {
			return
		}
		rValue.SetUint(val)
	case reflect.Float32, reflect.Float64:
		var val float64
		val, err = strconv.ParseFloat(str, 64)
		if err != nil {
			return
		}
		rValue.SetFloat(val)

	case reflect.Bool:
		var val bool
		val, err = strconv.ParseBool(str)
		if err != nil {
			return
		}
		rValue.SetBool(val)
	case reflect.String:
		rValue.SetString(str)
	default:
		err = errors.Errorf("unsupport type: %v", rType.Kind())
	}

	return
}

func AnyArrayToTypeNameMap(objs []any) map[string]any {
	ret := make(map[string]any)
	for _, obj := range objs {
		v := reflect.ValueOf(obj)
		if v.Kind() == reflect.Ptr {
			v = v.Elem()
		}

		vT := v.Type()
		if vT.Kind() != reflect.Struct {
			continue
		}

		key := vT.Name()
		ret[key] = obj
	}

	return ret
}

func MergeMap(dst, src map[string]any) (ret map[string]any) {
	if dst == nil {
		dst = make(map[string]any)
	}

	if src == nil {
		return dst
	}

	for k, v := range src {
		dst[k] = v
	}

	ret = dst
	return
}

func UniqueSlice[T int | int8 | int16 | int64 | uint | uint8 | uint16 | uint32 | uint64 | float32 | float32 | string](s []T) []T {
	result := make([]T, 0, len(s))
	checkMap := make(map[T]bool)
	for _, item := range s {
		if _, ok := checkMap[item]; !ok {
			checkMap[item] = true
			result = append(result, item)
		}
	}

	return result
}

func Contains[T int | int8 | int16 | int64 | uint | uint8 | uint16 | uint32 | uint64 | float32 | float32 | string](arr []T, check T) bool {
	if arr == nil {
		return false
	}

	for _, item := range arr {
		if item == check {
			return true
		}
	}

	return false
}
