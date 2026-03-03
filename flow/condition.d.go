package flow

import (
	"github.com/ninenhan/go-workflow/fn"
	"strings"
)

type DictValueType string

const (
	String      DictValueType = "STRING"
	StringList  DictValueType = "STRING_LIST"
	Number      DictValueType = "NUMBER"
	NumberTuple DictValueType = "NUMBER_TUPLE"
	Single      DictValueType = "SINGLE"
	JSON        DictValueType = "JSON"
	JSON_ARRAY  DictValueType = "JSON_ARRAY"
)

// Operator 枚举
type Operator struct {
	Value     string        `json:"value,omitempty"`
	Desc      string        `json:"desc,omitempty"`
	ValueType DictValueType `json:"value_type,omitempty"`
	Disabled  bool          `json:"disabled,omitempty"`
	Order     int           `json:"order,omitempty"`
}

// 预定义的 Operator 实例
var (
	LIKE       = Operator{"LIKE", "文本包含", String, false, 10}
	IN_LIKE    = Operator{"IN_LIKE", "文本包含", StringList, false, 20}
	IN         = Operator{"IN", "IN", StringList, false, 30}
	NOT_IN     = Operator{"NOT_IN", "非IN", StringList, false, 30}
	SAME       = Operator{"SAME", "完全匹配", String, false, 100}
	EQ         = Operator{"EQ", "数值等于", Number, false, 150}
	NE         = Operator{"NE", "数值不等于", Number, false, 200}
	GT         = Operator{"GT", "数值大于", Number, false, 300}
	GTE        = Operator{"GTE", "数值大于等于", Number, false, 400}
	LT         = Operator{"LT", "数值小于", Number, false, 500}
	LTE        = Operator{"LTE", "数值小于等于", Number, false, 600}
	NOT_EMPTY  = Operator{"NOT_EMPTY", "存在", Single, false, 0}
	EMPTY      = Operator{"EMPTY", "不存在", Single, false, 1}
	BETWEEN    = Operator{"BETWEEN", "数值介于", NumberTuple, false, 700}
	EXISTS     = Operator{"EXISTS", "存在", Single, false, 0}
	NON_EXISTS = Operator{"NON_EXISTS", "不存在", Single, false, 1}
)

type LogicConnector int

const (
	AND LogicConnector = iota
	OR
	NOT
)

// Condition 结构体
type Condition struct {
	Key       string         `json:"key,omitempty"`      // 条件键
	Operator  string         `json:"operator,omitempty"` // 操作符
	Value     any            `json:"value,omitempty"`    // 值
	Label     string         `json:"label,omitempty"`    // 标签
	Script    string         `json:"script,omitempty"`   // 脚本
	Connector LogicConnector `json:"connector,omitempty"`
	Children  []Condition    `json:"children,omitempty"`
}

func CompareNumeric(op, ks, vs string) bool {
	kf, ok1 := fn.ToFloat64(ks)
	vf, ok2 := fn.ToFloat64(vs)
	if !ok1 || !ok2 {
		return false
	}
	switch op {
	case EQ.Value:
		return kf == vf
	case NE.Value:
		return kf != vf
	case GT.Value:
		return kf > vf
	case GTE.Value:
		return kf >= vf
	case LT.Value:
		return kf < vf
	case LTE.Value:
		return kf <= vf
	default:
		return false
	}
}

func Eval(o string, k string, v string) bool {
	switch o {
	case IN.Value:
		return fn.InSlice(k, v)
	case NOT_IN.Value:
		return !fn.InSlice(k, v)
	case IN_LIKE.Value:
		return fn.InLike(k, v)
	case EMPTY.Value:
		return fn.IsEmpty(k)
	case NOT_EMPTY.Value:
		return !fn.IsEmpty(k)
	case SAME.Value:
		return k == v
	case LIKE.Value:
		return strings.Contains(k, v)
	case EQ.Value, NE.Value, GT.Value, GTE.Value, LT.Value, LTE.Value:
		return CompareNumeric(o, k, v)
	case BETWEEN.Value:
		kf, ok1 := fn.ToFloat64(k)
		tuple := strings.Split(v, ",")
		if !ok1 || len(tuple) != 2 {
			return false
		}
		lower, ok3 := fn.ToFloat64(tuple[0])
		upper, ok4 := fn.ToFloat64(tuple[1])
		return ok3 && ok4 && kf >= lower && kf <= upper
	}
	return false
}

func ConditionValidator(renderModel map[string]any, condition Condition) bool {
	if condition.Operator == "" {
		condition.Operator = EQ.Value
	}
	k, o, v := condition.Key, condition.Operator, condition.Value
	parsed1, _ := fn.ParseTemplate(k)
	k = fn.RenderTemplateStrictly(k, parsed1, renderModel, false)
	if s, ok := v.(string); ok {
		parsed2, _ := fn.ParseTemplate(s)
		s = fn.RenderTemplateStrictly(k, parsed2, renderModel, false)
		return Eval(o, k, s)
	}
	return false
}
