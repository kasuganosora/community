package utils

import (
	"fmt"
	"github.com/pkg/errors"
	"reflect"
	"strconv"
	"unsafe"
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

func GetStructsMap[k int | int8 | int16 | int64 | uint | uint8 | uint16 | uint32 | uint64 | float32 | float64 | string, v any](objs []v, key string) map[k]v {
	sliceType := reflect.TypeOf(objs)
	structType := sliceType.Elem()
	if structType.Kind() == reflect.Ptr {
		structType = structType.Elem()
	}

	ret := make(map[k]v)
	for _, obj := range objs {
		itemV := reflect.ValueOf(obj)
		if itemV.Kind() == reflect.Ptr {
			itemV = itemV.Elem()
		}

		fieldType, exists := structType.FieldByName(key)
		if !exists {
			panic(fmt.Sprintf("field %s not exists", key))
		}

		fieldV := itemV.Field(fieldType.Index[0])
		ret[fieldV.Interface().(k)] = obj
	}

	return ret
}

func UniqueSlice[T int | int8 | int16 | int64 | uint | uint8 | uint16 | uint32 | uint64 | float32 | float64 | string](s []T) []T {
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

func Contains[T int | int8 | int16 | int64 | uint | uint8 | uint16 | uint32 | uint64 | float32 | float64 | string](arr []T, check T) bool {
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

func GetPtr[T any](obj T) *T {
	return &obj
}

func GetExistsMap[T int | int8 | int16 | int64 | uint | uint8 | uint16 | uint32 | uint64 | float32 | float64 | string](arr []T) map[T]bool {
	ret := make(map[T]bool)
	for _, item := range arr {
		ret[item] = true
	}

	return ret
}

func IsExistsForCheckMap[T int | int8 | int16 | int64 | uint | uint8 | uint16 | uint32 | uint64 | float32 | float64 | string](m map[T]bool, key T) bool {
	exists, ok := m[key]
	if !ok {
		return false
	}
	return exists
}

func IsKeyInMap[T int | int8 | int16 | int64 | uint | uint8 | uint16 | uint32 | uint64 | float32 | float64 | string](m map[T]any, key T) bool {
	_, ok := m[key]
	return ok
}

func DeepCopy(source any, target any) (err error) {
	// 判断 target 是否为指针类型
	if reflect.TypeOf(target).Kind() != reflect.Ptr {
		err = errors.New("target must be a pointer")
		return
	}

	// 判断 source 的类型是否与 target 的类型相同
	if reflect.TypeOf(source) != reflect.TypeOf(target).Elem() {
		err = errors.New("source and target must be the same type")
		return
	}

	// 判断 source 不能为 nil
	if source == nil {
		err = errors.New("source can not be nil")
		return
	}

	// 如果 target 为 nil，则创建一个新的对象
	if reflect.ValueOf(target).IsNil() {
		reflect.ValueOf(target).Elem().Set(reflect.New(reflect.TypeOf(source).Elem()))
	}

	sourceV := reflect.ValueOf(source)
	if sourceV.Kind() == reflect.Ptr {
		sourceV = sourceV.Elem()
	}

	targetV := reflect.ValueOf(target).Elem()

	// 按类型进行深拷贝
	newValue := deepCopyValue(sourceV)
	targetV.Set(newValue)

	return
}

func deepCopyValue(v reflect.Value) (ret reflect.Value) {
	switch v.Kind() {
	case reflect.Struct:
		ret = deepCopyStruct(v)
	case reflect.Slice:
		ret = deepCopySlice(v)
	case reflect.Map:
		ret = deepCopyMap(v)
	case reflect.Int:
		ret = reflect.ValueOf(int(v.Int()))
	case reflect.Int8:
		ret = reflect.ValueOf(int8(v.Int()))
	case reflect.Int16:
		ret = reflect.ValueOf(int16(v.Int()))
	case reflect.Int32:
		ret = reflect.ValueOf(int32(v.Int()))
	case reflect.Int64:
		ret = reflect.ValueOf(int64(v.Int()))
	case reflect.Uint:
		ret = reflect.ValueOf(uint(v.Uint()))
	case reflect.Uint8:
		ret = reflect.ValueOf(uint8(v.Uint()))
	case reflect.Uint16:
		ret = reflect.ValueOf(uint16(v.Uint()))
	case reflect.Uint32:
		ret = reflect.ValueOf(uint32(v.Uint()))
	case reflect.Uint64:
		ret = reflect.ValueOf(uint64(v.Uint()))
	case reflect.Float32:
		ret = reflect.ValueOf(float32(v.Float()))
	case reflect.Float64:
		ret = reflect.ValueOf(float64(v.Float()))
	case reflect.Bool:
		ret = reflect.ValueOf(v.Bool())
	case reflect.String:
		ret = reflect.ValueOf(v.String())
	case reflect.Ptr:
		// 则创建一个新的对象
		if v.IsNil() {
			return reflect.New(nil)
		}
		ret = deepCopyValue(v.Elem())
	default:
	}
	return
}

func deepCopyMap(source reflect.Value) (ret reflect.Value) {
	ret = reflect.MakeMap(source.Type())
	keyType := source.Type().Key()
	valueType := source.Type().Elem()
	for _, key := range source.MapKeys() {
		newKey := deepCopyValue(key)
		newValue := deepCopyValue(source.MapIndex(key))
		if keyType.Kind() == reflect.Ptr {
			newKey = newKey.Addr()
		}

		if valueType.Kind() == reflect.Ptr {
			newValue = newValue.Addr()
		}
		ret.SetMapIndex(newKey, newValue)
	}
	return
}

func deepCopySlice(source reflect.Value) (ret reflect.Value) {
	ret = reflect.MakeSlice(source.Type(), source.Len(), source.Cap())
	elementType := source.Type().Elem()

	for i := 0; i < source.Len(); i++ {
		newValue := deepCopyValue(source.Index(i))
		if elementType.Kind() == reflect.Ptr {
			newValue = newValue.Addr()
		}
		ret.Index(i).Set(newValue)
	}
	return

}

func deepCopyStruct(source reflect.Value) (ret reflect.Value) {
	ret = reflect.New(source.Type()).Elem()
	sourceType := source.Type()
	for i := 0; i < sourceType.NumField(); i++ {
		field := sourceType.Field(i)
		fieldValue := source.Field(i)
		newFieldValue := deepCopyValue(fieldValue)
		retField := ret.Field(i)

		if field.IsExported() {
			if field.Type.Kind() == reflect.Ptr {
				retField.Set(reflect.New(field.Type.Elem()))
				retField.Elem().Set(newFieldValue)
			} else {
				retField.Set(newFieldValue)
			}

		} else {
			if field.Type.Kind() == reflect.Ptr {
				reflect.NewAt(field.Type, unsafe.Pointer(retField.UnsafeAddr())).Elem().Set(reflect.New(field.Type.Elem()))
				reflect.NewAt(field.Type, unsafe.Pointer(retField.UnsafeAddr())).Elem().Elem().Set(newFieldValue)
			} else {
				reflect.NewAt(field.Type, unsafe.Pointer(retField.UnsafeAddr())).
					Elem().
					Set(newFieldValue)
			}
		}
	}

	return
}

func Min[T int | int8 | int16 | int64 | uint | uint8 | uint16 | uint32 | uint64 | float32 | float64](arr []T) T {
	if len(arr) == 0 {
		return 0
	}

	min := arr[0]
	for _, v := range arr {
		if v < min {
			min = v
		}
	}
	return min
}

func Max[T int | int8 | int16 | int64 | uint | uint8 | uint16 | uint32 | uint64 | float32 | float64](arr []T) T {
	if len(arr) == 0 {
		return 0
	}

	max := arr[0]
	for _, v := range arr {
		if v > max {
			max = v
		}
	}
	return max
}

func NumberArrayConvert[A, B int | int8 | int16 | int64 | uint | uint8 | uint16 | uint32 | uint64 | float32 | float64](arr []A) []B {
	ret := make([]B, len(arr))

	for i, v := range arr {
		ret[i] = B(v)
	}
	return ret
}

func StringNumberArrayConvertUint64(arr []string) []uint64 {
	ret := make([]uint64, len(arr))

	for i, v := range arr {
		ret[i], _ = strconv.ParseUint(v, 10, 64)
	}
	return ret
}

func MapByAnyFiled[T any, FieldType int | int8 | int16 | int64 | uint | uint8 | uint16 | uint32 | uint64 | float32 | float64 | string](arr []T, getFieldVal func(T) FieldType) map[FieldType]T {
	ret := make(map[FieldType]T)

	for _, v := range arr {
		ret[getFieldVal(v)] = v
	}

	return ret
}
