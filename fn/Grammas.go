package fn

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"strings"
	"sync"
)

// Ternary 三元表达式
func Ternary[T any](cond bool, trueVal T, falseVal T) T {
	if cond {
		return trueVal
	}
	return falseVal
}

func TernaryFunc[T any](cond bool, trueVal func() T, falseVal T) T {
	if cond {
		return trueVal()
	}
	return falseVal
}

func Stringify[A any](input A) string {
	// 检查是否为 nil
	if any(input) == nil {
		return ""
	}
	// 检查是否为字符串
	if str, ok := any(input).(string); ok {
		return str
	}
	data, err := json.Marshal(input)
	if err == nil {
		return string(data)
	}
	return fmt.Sprintf("%v", input)
}

// ConvertByJSON Convert 转换任意类型 A 到类型 B
func ConvertByJSON[A any, B any](input A) (B, error) {
	var bytes = make([]byte, 0)
	// 检查是否为字符串
	if str, ok := any(input).(string); ok {
		bytes = []byte(str)
	} else {
		// 序列化 input 为 JSON
		data, err := json.Marshal(input)
		if err != nil {
			var zero B // 返回 B 类型的零值
			return zero, fmt.Errorf("序列化失败: %w", err)
		}
		bytes = data
	}
	// 反序列化为目标类型
	var result B
	err := json.Unmarshal(bytes, &result)
	if err != nil {
		return result, fmt.Errorf("反序列化失败: %w", err)
	}
	return result, nil
}

// SimplifyHeader 压缩 Header
func SimplifyHeader(header map[string][]string) map[string]string {
	result := make(map[string]string)
	// 遍历 map 中的每个 key-value 对
	for key, values := range header {
		// 将值数组用逗号连接
		result[key] = strings.Join(values, ",")
	}
	return result
}

// 计算真实数据总和
func CalculateRealSize(v interface{}) int {
	val := reflect.ValueOf(v)
	typ := val.Type()
	totalSize := 0
	// 遍历每个字段
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)

		// 获取字段类型的大小
		fieldSize := int(field.Type.Size())
		totalSize += fieldSize
	}
	return totalSize
}

func Filter[T any](endpoints []T, f func(endpoint T) bool) []T {
	// 初始容量设置为 endpoints 的长度，避免动态扩展。
	var result []T
	result = result[:0] // 预先分配并设置切片长度为0，避免内存浪费
	for _, endpoint := range endpoints {
		if f(endpoint) {
			result = append(result, endpoint)
		}
	}
	return result
}

// RegexReplace 用正则表达式将字符串 s 中所有匹配 pattern 的部分替换为 repl
func RegexReplace(s, pattern, repl string) string {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return s
	}
	return re.ReplaceAllString(s, repl)
}

func StreamMap[T any, R any](in []T, f func(T) R) []R {
	out := make([]R, len(in))
	for i, v := range in {
		out[i] = f(v)
	}
	return out
}

func CollectToMap[T any, K comparable, V any](
	in []T,
	keyOf func(T) K,
	valOf func(T) V,
) map[K]V {
	out := make(map[K]V, len(in))
	for i := range in {
		out[keyOf(in[i])] = valOf(in[i])
	}
	return out
}

func MapKeys[M ~map[K]V, K comparable, V any](m M) []string {
	rawKeys := maps.Keys(m)
	var allKeys []string
	for k := range rawKeys {
		allKeys = append(allKeys, fmt.Sprint(k))
	}
	return allKeys
}

func Find[T any](list []T, predicate func(T) bool) (T, bool) {
	var zero T
	for _, item := range list {
		if predicate(item) {
			return item, true
		}
	}
	return zero, false
}

type Pair[O any] struct {
	Out O
	Err error
}

// RunParallel 并发执行 fn，按输入顺序返回结果。
// - inputs: 要处理的输入切片
// - workers: 工作者数量（<=0 视为 1）
// - fn: 你的任务函数，形如 func(ctx, I) (O, error)
func RunParallel[I any, O any](
	ctx context.Context,
	inputs []I,
	workers int,
	rowId func(I) string,
	function func(context.Context, I) (O, error),
) (map[string]O, []error) {
	defer TimingMiddlewareLogging("👷 WORKER", "RunParallel")()

	n := len(inputs)
	if workers <= 0 {
		workers = 1
	}
	if workers > n {
		workers = n
	}

	out := make(map[string]O, n)
	errs := make([]error, n)

	type job struct {
		idx int
		key string
	}
	type res struct {
		idx int
		key string
		val O
		err error
	}

	jobs := make(chan job)
	results := make(chan res, workers) // 小缓冲减少阻塞

	var wg sync.WaitGroup
	wg.Add(workers)

	// workers
	worker := func() {
		defer wg.Done()
		for j := range jobs {
			select {
			case <-ctx.Done():
				var zero O
				results <- res{idx: j.idx, key: j.key, val: zero, err: ctx.Err()}
			default:
				v, e := function(ctx, inputs[j.idx])
				results <- res{idx: j.idx, key: j.key, val: v, err: e}
			}
		}
	}
	for i := 0; i < workers; i++ {
		go worker()
	}

	// 派发
	go func() {
		defer close(jobs)
		for i := 0; i < n; i++ {
			select {
			case <-ctx.Done():
				return
			case jobs <- job{idx: i, key: rowId(inputs[i])}:
			}
		}
	}()

	// 收集（单写者：这里安全写 map 和切片）
	go func() {
		wg.Wait()
		close(results)
	}()

	for r := range results {
		out[r.key] = r.val  // 单 goroutine 写 map -> 安全
		errs[r.idx] = r.err // 不重分配，按索引写切片本来也安全
	}

	return out, errs
}
