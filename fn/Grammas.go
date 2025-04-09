package fn

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
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
