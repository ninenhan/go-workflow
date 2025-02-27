package fn

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// Ternary 三元表达式
func Ternary[T any](cond bool, trueVal T, falseVal T) T {
	if cond {
		return trueVal
	}
	return falseVal
}

// ConvertByJSON Convert 转换任意类型 A 到类型 B
func ConvertByJSON[A any, B any](input A) (B, error) {
	// 序列化 input 为 JSON
	data, err := json.Marshal(input)
	if err != nil {
		var zero B // 返回 B 类型的零值
		return zero, fmt.Errorf("序列化失败: %w", err)
	}
	// 反序列化为目标类型
	var result B
	err = json.Unmarshal(data, &result)
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
