package fn

import (
	"reflect"
	"strings"
)

// MakeStructExtractor 预编译一个从 struct T 按 keys 提取值的函数（按 json tag 或字段名匹配，支持匿名/嵌套）
func MakeStructExtractor[T any](keys []string) func(T) []interface{} {
	t := reflect.TypeOf((*T)(nil)).Elem()
	indexByKey := buildIndexByKey(t) // map[key][]int

	// 为 keys 预算好索引，避免每行查 map
	paths := make([][]int, len(keys))
	for i, k := range keys {
		if p, ok := indexByKey[k]; ok {
			paths[i] = p
			continue
		}
		// 找不到就留空，后面填 nil
	}

	return func(v T) []interface{} {
		rv := reflect.ValueOf(v)
		if rv.Kind() == reflect.Pointer {
			if rv.IsNil() {
				out := make([]interface{}, len(keys))
				return out
			}
			rv = rv.Elem()
		}
		out := make([]interface{}, len(keys))
		for i, path := range paths {
			if len(path) == 0 {
				out[i] = nil
				continue
			}
			fv := rv.FieldByIndex(path)
			if !fv.IsValid() || (fv.Kind() == reflect.Pointer && fv.IsNil()) {
				out[i] = nil
				continue
			}
			out[i] = fv.Interface()
		}
		return out
	}
}

// 扫描 t 的所有导出字段（含嵌套、匿名），按 json tag（无则用字段名）建立 key->索引路径
func buildIndexByKey(t reflect.Type) map[string][]int {
	m := make(map[string][]int)
	var walk func(rt reflect.Type, base []int, depth int)
	walk = func(rt reflect.Type, base []int, depth int) {
		if rt.Kind() == reflect.Pointer {
			rt = rt.Elem()
		}
		if rt.Kind() != reflect.Struct || depth > 8 { // 防御深递归
			return
		}
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			if f.PkgPath != "" { // 非导出
				continue
			}
			path := append(append([]int{}, base...), i)
			// 取 json 主名
			name := f.Name
			if tag, ok := f.Tag.Lookup("json"); ok && tag != "-" {
				parts := strings.Split(tag, ",")
				if parts[0] != "" {
					name = parts[0]
				}
			}
			// 记录（优先叶子覆盖匿名聚合）
			if _, exists := m[name]; !exists {
				m[name] = path
			}
			// 递归走嵌套/匿名
			ft := f.Type
			if ft.Kind() == reflect.Struct || (ft.Kind() == reflect.Pointer && ft.Elem().Kind() == reflect.Struct) {
				walk(ft, path, depth+1)
			}
		}
	}
	walk(t, nil, 0)
	return m
}
