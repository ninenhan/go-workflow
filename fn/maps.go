package fn

import (
	"fmt"
	"iter"
)

func StreamMapDistinct[T any, R comparable](in []T, fn func(T) R) []R {
	seen := make(map[R]struct{}, len(in))
	out := make([]R, 0, len(in))
	for _, v := range in {
		key := fn(v)
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			out = append(out, key)
		}
	}
	return out
}

func MapNotNull[T any, R any](in []T, f func(T) *R) []R {
	out := make([]R, 0, len(in))
	for _, v := range in {
		if r := f(v); r != nil {
			out = append(out, *r)
		}
	}
	return out
}

// FlatMap 把每个元素映射成一个 slice，然后展开合并成一个 slice。
func FlatMap[T any, R any](in []T, f func(T) []R) []R {
	out := make([]R, 0)
	for _, v := range in {
		rs := f(v)
		if len(rs) > 0 {
			out = append(out, rs...)
		}
	}
	return out
}

func CleanMap(m map[string]any) map[string]any {
	out := make(map[string]any)
	for k, v := range m {
		if v == nil {
			continue
		}
		switch vv := v.(type) {
		case string:
			if vv == "" {
				continue
			}
		case []any:
			if len(vv) == 0 {
				continue
			}
		case map[string]any:
			if len(vv) == 0 {
				continue
			}
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func GroupBy[T any, K comparable](items []T, keyFn func(T) K) map[K][]T {
	groups := make(map[K][]T, len(items))
	for _, v := range items {
		k := keyFn(v)
		groups[k] = append(groups[k], v)
	}
	return groups
}

func SeqToSlice[V any](seq iter.Seq[V]) []V {
	s := make([]V, 0)
	for v := range seq {
		s = append(s, v)
	}
	return s
}

func ReadStringField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	case fmt.Stringer:
		return t.String()
	default:
		return fmt.Sprint(t)
	}
}
