package core

import (
	"encoding/json"
	"fmt"
)

type DictValueType string

const (
	String      DictValueType = "STRING"
	StringList  DictValueType = "STRING_LIST"
	Number      DictValueType = "NUMBER"
	NumberTuple DictValueType = "NUMBER_TUPLE"
	Single      DictValueType = "SINGLE"
)

// Operator 枚举
type Operator struct {
	Value     string        `json:"value"`
	Desc      string        `json:"desc"`
	ValueType DictValueType `json:"value_type"`
	Disabled  bool          `json:"disabled"`
	Order     int           `json:"order"`
}

// 预定义的 Operator 实例
var (
	LIKE       = Operator{"LIKE", "文本包含", String, false, 10}
	IN_LIKE    = Operator{"IN_LIKE", "文本包含", StringList, false, 20}
	IN         = Operator{"IN", "IN", StringList, false, 30}
	NOT_IN     = Operator{"NOT_IN", "非IN", StringList, false, 30}
	EQ         = Operator{"EQ", "数值等于", Number, false, 100}
	NE         = Operator{"NE", "数值不等于", Number, false, 200}
	GT         = Operator{"GT", "数值大于", Number, false, 300}
	GTE        = Operator{"GTE", "数值大于等于", Number, false, 400}
	LT         = Operator{"LT", "数值小于", Number, false, 500}
	LTE        = Operator{"LTE", "数值小于等于", Number, false, 600}
	EXISTS     = Operator{"EXISTS", "存在", Single, false, 0}
	NON_EXISTS = Operator{"NON_EXISTS", "不存在", Single, false, 1}
	BETWEEN    = Operator{"BETWEEN", "数值介于", NumberTuple, false, 700}
)

type OperatorStr = string

// Joiner 枚举
type Joiner string

const (
	AND Joiner = "AND"
	OR  Joiner = "OR"
	NOT Joiner = "NOT"
)

// Condition 结构体
type Condition struct {
	Key       string      `json:"key,omitempty"`    // 条件键
	Operator  OperatorStr `json:"operator"`         // 操作符
	Value     interface{} `json:"value,omitempty"`  // 值
	Label     string      `json:"label,omitempty"`  // 标签
	Script    string      `json:"script,omitempty"` // 脚本
	JointNext Joiner      `json:"joint_next,omitempty"`
	Children  []Condition `json:"children,omitempty"`
}

// NewCondition 创建一个新的 Condition
func NewCondition(key string, operator OperatorStr, value interface{}) *Condition {
	return &Condition{
		Key:      key,
		Operator: operator,
		Value:    value,
	}
}

// MarshalJSON 自定义 JSON 序列化
func (c *Condition) MarshalJSON() ([]byte, error) {
	type Alias Condition
	return json.Marshal(&struct {
		*Alias
	}{
		Alias: (*Alias)(c),
	})
}

// SortRO 结构体
type SortRO struct {
	Column string         `json:"column"`
	Desc   bool           `json:"desc"`   // 默认为 false
	Format FormatFunction `json:"format"` // 格式函数
}

// SortEnum 枚举，用于表示排序方向
type SortEnum string

const (
	ASC  SortEnum = "ASC"
	DESC SortEnum = "DESC"
)

// FormatFunction 枚举，用于表示不同格式类型
type FormatFunction string

const (
	BOOL     FormatFunction = "bool"
	DATE     FormatFunction = "date"
	TIME     FormatFunction = "time"
	DATETIME FormatFunction = "datetime"
	INT      FormatFunction = "int"
	NUMBER   FormatFunction = "number"
	DECIMAL  FormatFunction = "decimal"
	STRING   FormatFunction = "string"
)

// NewSortRO 创建一个新的 SortRO
func NewSortRO(column string, desc bool, format FormatFunction) *SortRO {
	return &SortRO{
		Column: column,
		Desc:   desc,
		Format: format,
	}
}

// String 方法用于打印 SortRO 对象的字符串
func (s *SortRO) String() string {
	return fmt.Sprintf("SortRO{column: %s, desc: %t, format: %s}", s.Column, s.Desc, s.Format)
}
