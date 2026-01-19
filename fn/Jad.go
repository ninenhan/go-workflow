package fn

import (
	"fmt"
	"reflect"
	"strings"
)

func UniqueList(items []string) []string {
	seen := make(map[string]struct{})
	var result []string
	for _, item := range items {
		if _, exists := seen[item]; !exists {
			seen[item] = struct{}{}
			result = append(result, item)
		}
	}
	return result
}

func IsDataEmpty(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.String:
		return rv.Len() == 0
	case reflect.Slice, reflect.Array, reflect.Map:
		return rv.Len() == 0
	default:
		return false
	}
}

func ParseList(v any) []string {
	switch val := v.(type) {
	case []any:
		var out []string
		for _, item := range val {
			out = append(out, fmt.Sprint(item))
		}
		return out
	case []string:
		return val
	case string:
		if strings.TrimSpace(val) == "" {
			return nil
		}
		return strings.Split(val, ",")
	default:
		return nil
	}
}

func InSlice(target any, list any) bool {
	targetStr := fmt.Sprint(target)
	for _, item := range ParseList(list) {
		if targetStr == item {
			return true
		}
	}
	return false
}

func InLike(target any, list any) bool {
	targetStr, ok := target.(string)
	if !ok {
		return false
	}
	for _, s := range ParseList(list) {
		if strings.Contains(targetStr, s) {
			return true
		}
	}
	return false
}
