package flow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/ninenhan/go-workflow/fn"
	"log/slog"
	"reflect"
	"sync"
)

// PipelineContext 用于保存执行过程中的环境变量
type PipelineContext struct {
	Env     map[string]interface{} `json:"env,omitempty"`
	Context context.Context
}

type Input struct {
	//Template string `json:"template"`  //这是一段文字，内容为{{output}},现将{{slots}}中的内容去噪
	Data      any    `json:"data,omitempty"`      //最终输出
	DataType  string `json:"data_type,omitempty"` // plaintext, json, json_array，socket
	Slottable bool   `json:"slottable,omitempty"` // 是否是可插槽的
}
type Output struct {
	Data      any    `json:"data,omitempty"`
	DataType  string `json:"data_type,omitempty"`
	Slottable bool   `json:"slottable,omitempty"` // 是否是可插槽的
}

type IOConfig struct {
	Input        Input  `json:"input,omitempty"`
	DefaultInput Input  `json:"default_input,omitempty"`
	Output       Output `json:"output,omitempty"`
}

// PhaseUnit 定义了工作单元接口，所有单元必须实现 GetID 与 Execute 方法
type PhaseUnit interface {
	GetID() string
	PresetID()
	GetUnitName() string
	GetFlowable() bool
	GetIOConfig() *IOConfig
	Execute(ctx *PipelineContext, input *Input) (*Output, error)
	Next(ctx *PipelineContext, input *Input) []PhaseUnit
}

func SafeUnits(units []PhaseUnit) []PhaseUnit {
	if units == nil {
		return []PhaseUnit{}
	}
	return units
}

type NextResult struct {
	Units      []PhaseUnit
	ReplaceAll bool // true = 跳转控制流（如 if/while）
}

type UnitOutput struct {
	ID     string `json:"id,omitempty"`
	Output any    `json:"output,omitempty"`
}

// BaseUnit 为所有单元提供基础属性（如 ID）
type BaseUnit struct {
	ID         string    `json:"id"`
	UnitName   string    `json:"unit_name,omitempty"`
	IOConfig   *IOConfig `json:"io_config,omitempty"`
	UnFlowable bool      `json:"flowable,omitempty"` // 是否不接受外来输入
	Status     JobStatus `json:"status,omitempty"`
}

func (u *BaseUnit) GetID() string {
	return u.ID
}

func (u *BaseUnit) PresetID() {
	id, _ := fn.GenerateShortID()
	u.ID = id
}

func (u *BaseUnit) GetIOConfig() *IOConfig {
	return u.IOConfig
}
func (u *BaseUnit) GetUnitName() string {
	return u.UnitName
}

func (u *BaseUnit) GetFlowable() bool {
	return !u.UnFlowable
}

func (u *BaseUnit) Next(ctx *PipelineContext, i *Input) []PhaseUnit {
	return nil
}

type UnitRepository struct {
	mu       sync.RWMutex
	Mappings map[string]reflect.Type // 存储所有单元的映射关系
}

func (r *UnitRepository) RegisterUnit(name string, unit any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Mappings == nil {
		r.Mappings = make(map[string]reflect.Type)
	}
	r.Mappings[name] = reflect.TypeOf(unit).Elem()
}

func (r *UnitRepository) ParsePhaseUnitsFromMap(rawList []map[string]any) ([]PhaseUnit, error) {
	data, err := json.Marshal(rawList)
	if err != nil {
		return nil, err
	}
	return r.ParsePhaseUnits(data, "")
}

func (r *UnitRepository) ParsePhaseUnits(jsonData []byte, typeField string) ([]PhaseUnit, error) {
	var rawUnits []map[string]any
	if err := json.Unmarshal(jsonData, &rawUnits); err != nil {
		return nil, err
	}
	if typeField == "" {
		typeField = "unit_name"
	}
	var units []PhaseUnit
	for _, raw := range rawUnits {
		typeVal, ok := raw[typeField].(string)
		if !ok {
			return nil, fmt.Errorf("缺少 {%s} 字段", typeField)
		}

		unitType, ok := r.Mappings[typeVal]
		if !ok {
			return nil, fmt.Errorf("未知的类型: %s", typeVal)
		}

		unitPtr := reflect.New(unitType).Interface()
		unitJSON, _ := json.Marshal(raw)

		if err := json.Unmarshal(unitJSON, unitPtr); err != nil {
			return nil, err
		}

		units = append(units, unitPtr.(PhaseUnit))
	}
	return units, nil
}

// ConditionFunc 定义了一个根据上下文判断条件的函数
type ConditionFunc func(ctx *PipelineContext) bool

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

// Pipeline 由多个 PhaseUnit 组成，并持有执行上下文
type Pipeline struct {
	Units       []PhaseUnit      `json:"units,omitempty"`
	Context     *PipelineContext `json:"-"`
	Interrupted bool
	LastOutput  Output
}

func PrepareUnits(units []PhaseUnit) []PhaseUnit {
	for _, unit := range units {
		if unit.GetID() == "" {
			unit.PresetID()
		}
	}
	return units
}

func NewPipeline(units []PhaseUnit) *Pipeline {
	return &Pipeline{
		Units: PrepareUnits(units),
		Context: &PipelineContext{Env: make(map[string]interface{}),
			Context: context.Background(),
		},
	}
}

func (p *Pipeline) Interrupt() {
	p.Interrupted = true
}

func GetInput(unit PhaseUnit, env map[string]any) (*Input, error) {
	var input *Input
	ioCfg := unit.GetIOConfig()
	//暂时不控制，假定每个节点都需要去设定输入来源，不能直接去获取上一个的输入，至少需要设定下
	//if unit.GetFlowable() {
	//	if !fn.IsDataEmpty(p.LastOutput.Data) {
	//		input = &Input{
	//			Data:      p.LastOutput.Data,
	//			Slottable: p.LastOutput.Slottable,
	//			DataType:  p.LastOutput.DataType,
	//		}
	//	}
	//}

	// 获取输入
	if ioCfg != nil {
		// 示例模板文本
		var inputText = ioCfg.Input.Data
		input = &ioCfg.Input
		if fn.IsDataEmpty(inputText) {
			inputText = ioCfg.DefaultInput.Data
			input = &ioCfg.DefaultInput
		}
		str, ok := inputText.(string)
		if ok && input.Slottable {
			// 解析模板中的占位符
			parsed, err := fn.ParseTemplate(str)
			if err != nil {
				fmt.Println("解析模板出错：", err)
				return nil, errors.New(fmt.Sprintf("解析模板出错：%s", err))
			}
			if len(parsed) > 0 {
				fmt.Println("提取到的占位符：", parsed)
				// 检查模型参数是否合法（这里只是示例模型）
				// 准备渲染模板的数据（只替换部分占位符）
				renderModel := env
				if err := fn.CheckModelValid(renderModel); err != nil {
					fmt.Println("模型参数不合法：", err)
				} else {
					fmt.Println("模型参数合法")
				}
				rendered := fn.RenderTemplateStrictly(str, parsed, renderModel, false)
				fmt.Println("渲染后的模板：", rendered)
				//p.Context.Env[
				input = &Input{
					Data:      rendered,
					Slottable: true,
				}
			} else {
				input = &Input{
					Data:      inputText,
					Slottable: false,
				}
			}
		}
	} else {
		input = &Input{
			Slottable: false,
		}
	}
	return input, nil
}

func (p *Pipeline) Run1() error {
	env := p.Context.Env
	defer func() {
		fmt.Printf("Pipeline 执行结束。最后结果：%v\n", p.LastOutput)
	}()
	for _, unit := range p.Units {
		if p.Interrupted {
			fmt.Printf("Pipeline 被中断，停止执行。\n")
			break
		}

		input, err := GetInput(unit, env)
		if err != nil {
			return err
		}

		res, err := unit.Execute(p.Context, input)
		if err != nil {
			return err
		}

		// 存储输出
		if unit.GetID() != "" {
			p.Context.Env[unit.GetID()] = map[string]any{
				"output": res.Data,
			}
		}
		p.LastOutput = *res
	}
	return nil
}

func (p *Pipeline) Run() error {
	env := p.Context.Env
	queue := append([]PhaseUnit{}, p.Units...)

	for len(queue) > 0 {
		if p.Interrupted {
			fmt.Println("中断，停止执行")
			break
		}

		unit := queue[0]
		queue = queue[1:]

		input, err := GetInput(unit, env)
		if err != nil {
			return err
		}
		slog.Info("执行单元：", "单元id", unit.GetID(), "单元名称", unit.GetUnitName())
		res, err := unit.Execute(p.Context, input)
		if err != nil {
			return err
		}

		// 写入输出
		if unit.GetID() != "" && res != nil {
			env[unit.GetID()] = map[string]any{"output": res.Data}
		}

		if res != nil {
			p.LastOutput = *res
		}
		// 动态追加下一步
		next := unit.Next(p.Context, nil)
		if len(next) > 0 {
			//插入队首，支持条件跳转
			queue = append(next, queue...)
			continue // 跳过当前循环，开始新分支执行
		}
	}
	return nil
}
