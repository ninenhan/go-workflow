package core

import (
	"context"
	"errors"
	"github.com/ninenhan/go-workflow/fn"
	"log"
	"sync"
	"time"
)

type PipelineState struct {
	CurrentStageIndex int
	// 如果Stage内部有多个Unit并行或串行执行的上下文，也需要记录当前Unit的进度
	CurrentUnitIndex int
	Status           string
	// 可以存储上一阶段的输出数据，用于从中间点恢复
	LastOutput any
}

type ExecutionState interface {
	Save(state PipelineState) error
	Load() (PipelineState, error)
}

// Pipeline 是整个 CI/CD 流水线，包含多个阶段
type Pipeline struct {
	ID                string
	stages            []*Stage
	mu                sync.Mutex
	Status            string // "pending", "running", "completed", "failed", "terminal", "retry", "resume","paused"
	StartTime         time.Time
	EndTime           time.Time
	StateStore        ExecutionState
	currentStageIndex int
	currentUnitIndex  int
	lastOutput        any
	Results           map[string]any
}

func (p *Pipeline) Run(ctx context.Context, initialInput any) (any, error) {
	input := initialInput
	for i := p.currentStageIndex; i < len(p.stages); i++ {
		//for _, stage := range p.stages {
		stage := p.stages[i]
		if fn.IsEmpty(stage.Name) {
			log.Printf("阶段 %d 名称未设置，跳过执行", i)
			break
		}
		// 在执行Stage内的Units时，也可能需要参考currentUnitIndex
		// 并在每个Unit执行完后更新currentUnitIndex
		// 当Stage完成后，将currentUnitIndex重置，currentStageIndex+1
		for p.Status == "paused" {
			p.SaveState()
			time.Sleep(80 * time.Millisecond)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}
		}
		// 检查是否暂停
		for {
			p.mu.Lock()
			if p.Status == "paused" {
				p.mu.Unlock()
				// 保存当前状态
				p.SaveState()
				time.Sleep(100 * time.Millisecond)
				continue
			}
			p.mu.Unlock()
			break
		}
		// 检查是否停止
		p.mu.Lock()
		if p.Status == "terminal" {
			p.mu.Unlock()
			p.SaveState()
			return nil, errors.New("pipeline stopped")
		}
		p.mu.Unlock()
		out, err := stage.Run(ctx, input)
		//time.Sleep(800 * time.Millisecond)
		if err != nil {
			return nil, err
		}
		input = out
		if p.Results == nil {
			p.Results = make(map[string]any)
		}
		p.Results[stage.Name] = out
	}
	return input, nil
}

func NewPipeline(StateStore ExecutionState, stages ...*Stage) *Pipeline {
	return &Pipeline{stages: stages,
		StateStore: StateStore}
}

// SaveState 在合适时机调用saveState
func (p *Pipeline) SaveState() {
	state := PipelineState{
		CurrentStageIndex: p.currentStageIndex,
		CurrentUnitIndex:  p.currentUnitIndex,
		Status:            p.Status,
		LastOutput:        p.lastOutput,
	}
	// 忽略错误处理演示
	err := p.StateStore.Save(state)
	if err != nil {
		return
	}
}

// LoadState 在Resume或启动时从store加载状态
func (p *Pipeline) LoadState() error {
	state, err := p.StateStore.Load()
	if err != nil {
		return err
	}
	p.ApplyState(state)
	return nil
}

func (p *Pipeline) ApplyState(state PipelineState) {
	p.currentStageIndex = state.CurrentStageIndex
	p.currentUnitIndex = state.CurrentUnitIndex
	p.Status = state.Status
	p.lastOutput = state.LastOutput
}

// Pause For controlling pipeline execution
func (p *Pipeline) Pause() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Status = "paused"
}
func (p *Pipeline) Resume() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Status = "resume"
}
func (p *Pipeline) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Status = "terminal"
}
