package flow

import (
	"encoding/json"
	"fmt"
	"github.com/ninenhan/go-workflow/fn"
	"reflect"
	"strings"
)

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

type Joiner = string // AND, OR

// Condition 结构体
type Condition struct {
	Key       string      `json:"key,omitempty"`      // 条件键
	Operator  string      `json:"operator,omitempty"` // 操作符
	Value     any         `json:"value,omitempty"`    // 值
	Label     string      `json:"label,omitempty"`    // 标签
	Script    string      `json:"script,omitempty"`   // 脚本
	JointNext Joiner      `json:"joint_next,omitempty"`
	Children  []Condition `json:"children,omitempty"`
}

// IfUnit 实现 if–else 控制，根据条件选择执行 true 或 false 分支中的单元
type IfUnit struct {
	BaseUnit
	IfCondition      Condition     `json:"if_condition"` // 注意函数无法直接序列化
	ElseIfConditions []Condition   `json:"else_if,omitempty"`
	IfUnits          []PhaseUnit   `json:"if_units,omitempty"`
	ElseIfUnits      [][]PhaseUnit `json:"else_if_units,omitempty"`
	ElseUnits        []PhaseUnit   `json:"else_units,omitempty"`
}

func (t *IfUnit) GetUnitName() string {
	return reflect.TypeOf(IfUnit{}).Name()
}

func (t *IfUnit) Execute(ctx *PipelineContext, i *Input) (*Output, error) {
	return nil, nil
}

func (t *IfUnit) Next(ctx *PipelineContext, i *Input) []PhaseUnit {
	if t.IfCondition.Operator == "" {
		t.IfCondition.Operator = EQ.Value
	}
	if ConditionValidator(ctx, t.IfCondition) {
		fmt.Printf("[IfUnit:%s] 命中条件: %s \n", t.ID, t.IfCondition.Key)
		return SafeUnits(t.IfUnits)
	}
	for index, elseIfCondition := range t.ElseIfConditions {
		if ConditionValidator(ctx, elseIfCondition) {
			fmt.Printf("[IfUnit:%s] 命中条件: %s -> 分支: %d\n", t.ID, t.IfCondition.Key, index)
			if index < len(t.ElseIfUnits) {
				return SafeUnits(t.ElseIfUnits[index])
			}
		}
	}
	return SafeUnits(t.ElseUnits)
}

func (t *IfUnit) UnmarshalJSON(data []byte) error {
	// 用中间结构解出 if_units 等字段的原始 JSON
	type Alias IfUnit
	aux := &struct {
		IfUnitsRaw   []map[string]any   `json:"if_units"`
		ElseUnitsRaw []map[string]any   `json:"else_units"`
		ElseIfRaw    [][]map[string]any `json:"else_if_units"`
		*Alias
	}{
		Alias: (*Alias)(t),
	}

	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}

	// 解析 IfUnits
	units, err := ParsePhaseUnitsFromMap(aux.IfUnitsRaw)
	if err != nil {
		return fmt.Errorf("if_units 反序列化失败: %w", err)
	}
	t.IfUnits = units

	// 解析 ElseUnits
	elseUnits, err := ParsePhaseUnitsFromMap(aux.ElseUnitsRaw)
	if err != nil {
		return fmt.Errorf("else_units 反序列化失败: %w", err)
	}
	t.ElseUnits = elseUnits

	// 解析 ElseIfUnits
	for _, rawList := range aux.ElseIfRaw {
		units, err := ParsePhaseUnitsFromMap(rawList)
		if err != nil {
			return fmt.Errorf("else_if_units 反序列化失败: %w", err)
		}
		t.ElseIfUnits = append(t.ElseIfUnits, units)
	}
	return nil
}

// WhileUnit 循环单元 =====
type WhileUnit struct {
	BaseUnit
	Condition Condition   `json:"condition,omitempty"`
	Units     []PhaseUnit `json:"units,omitempty"`
}

func (t *WhileUnit) Execute(ctx *PipelineContext, i *Input) (*Output, error) {
	return nil, nil
}

func (t *WhileUnit) Next(ctx *PipelineContext, i *Input) []PhaseUnit {
	if t == nil || ctx == nil {
		return nil
	}
	if t.Condition.Operator == "" {
		t.Condition.Operator = EQ.Value
	}
	if ConditionValidator(ctx, t.Condition) {
		return SafeUnits(t.Units)
	}
	return nil
}

func (t *WhileUnit) UnmarshalJSON(data []byte) error {
	type Alias WhileUnit
	aux := &struct {
		Units []map[string]any `json:"units"`
		*Alias
	}{
		Alias: (*Alias)(t),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	t.BaseUnit = aux.BaseUnit
	t.Condition = aux.Condition
	units, err := ParsePhaseUnitsFromMap(aux.Units)
	if err != nil {
		return err
	}
	t.Units = units
	return nil
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

func ConditionValidator(ctx *PipelineContext, condition Condition) bool {
	if condition.Operator == "" {
		condition.Operator = EQ.Value
	}
	renderModel := ctx.Env
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
