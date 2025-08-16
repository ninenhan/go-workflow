package fn

import (
	"reflect"
	"time"
)

func Ptr[T any](v T) *T {
	return &v
}

func normalizeValue(v reflect.Value) interface{} {
	if !v.IsValid() {
		return ""
	}
	// 解引用
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return ""
		}
		return normalizeValue(v.Elem())
	}

	// 时间处理
	if v.Type() == reflect.TypeOf(time.Time{}) {
		t := v.Interface().(time.Time)
		if t.IsZero() {
			return ""
		}
		return t.Format("2006-01-02 15:04:05")
	}

	switch v.Kind() {
	case reflect.Float32, reflect.Float64,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return NumericStringify(v.Interface())
	default:
		return v.Interface()
	}
}

func DeRef(row []interface{}) []interface{} {
	out := make([]interface{}, 0, len(row))
	for _, v := range row {
		out = append(out, normalizeValue(reflect.ValueOf(v)))
	}
	return out
}
