package unit

import (
	"fmt"
	"reflect"
)

// InputAs 将当前 Unit Node 的输入读取为指定类型 T。
// Node、Input 或 Input.Data 缺失时返回错误；
// 输入类型与 T 不一致时返回类型错误。
func InputAs[T any](node *Node) (T, error) {
	var zero T

	if node == nil {
		return zero, fmt.Errorf("unit node is nil")
	}

	if node.Input == nil || node.Input.Data == nil {
		return zero, fmt.Errorf(
			"node %q input is required",
			node.ID,
		)
	}

	value, ok := node.Input.Data.(T)
	if !ok {
		expectedType := reflect.TypeOf((*T)(nil)).Elem()

		return zero, fmt.Errorf(
			"node %q input has type %T, want %s",
			node.ID,
			node.Input.Data,
			expectedType,
		)
	}

	return value, nil
}

// InputObject 将当前 Unit Node 的输入读取为 object。
// 它对应 definition.InputModeObject 生成的 map[string]any。
//
// 输入存在但不是 object 时返回类型错误。
func InputObject(
	node *Node,
) (map[string]any, error) {
	return InputAs[map[string]any](node)
}

// InputObjectIfExists , 输入可以为空 , 其他等同 InputObject
func InputObjectIfExists(
	node *Node,
) (map[string]any, error) {
	if node == nil || node.Input == nil || node.Input.Data == nil {
		return nil, nil
	}
	return InputAs[map[string]any](node)
}
