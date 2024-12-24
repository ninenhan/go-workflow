package core

//设计方案 - finetune with chatgpt-o1 by fred
//1.	Storyboard（蓝图）：
//•	包含多个单元（Unit）。
//•	每个单元可以有多个输入和输出通道。
//•	单元之间的连线可以形成任务（Task）。
//2.	Unit（单元）：
//•	属性：定义单元可以执行什么操作、需要的资源、能产生的结果。
//•	输入输出：每个单元可以有多个输入输出通道。
//•	任务：每个单元生成一个任务，任务在流水线中执行。
//3.	Stage（阶段）：
//•	从Storyboard中选择若干单元，组合成一个阶段。
//•	阶段之间的数据传递可以是单个单元的输出，也可以是多个单元的组合输出。
//•	阶段依赖关系：每个阶段可以依赖于前一个阶段的输出作为输入。
//4.	Pipeline（流水线）：
//•	由多个阶段组成。
//•	阶段之间可以存在依赖关系，流水线中的每个阶段可以是独立的，或者通过数据流动进行连接。
import (
	"context"
)

// -----------------------------
// 基础接口定义
// -----------------------------

// UnitTemplate 是零件模板接口，通过它可以实例化出对应的Unit。
// 不同的UnitTemplate可以定义不同的处理逻辑、输入输出类型等。
type UnitTemplate interface {
	Name() string
	Description() string
	// Instantiate 由Storyboard在构建Stage时调用，用于根据模板创建对应的Unit实例（单元）
	Instantiate() Unit
}

// -----------------------------
// Unit
// -----------------------------

// Unit 表示具体的单元，由模板实例化而来。
// 一个Unit有输入输出的Channel描述、有执行Job的逻辑等
type Unit interface {
	ID() string                                                     // Unit的ID，每次实例化可能不一样
	Run(ctx context.Context, input any, current *Unit) (any, error) // 执行单元的主要逻辑，会产出结果输出
}
type UnitRegistry interface {
	Name() string         // Unit的名称
	Order() int           // 执行顺序
	TemplateName() string // 对应的模板名称
	SetInput(any) error   // 设置输入数据
	GetOutput() any       // 获取输出数据
}

type UnitOutput struct {
	ID     string `json:"id"`
	Output any    `json:"output"`
}

// 默认的Unit实现
type DefaultUnit struct {
	id          string
	Name        string
	Description string
	input       any
	output      any
}
