package core

import (
	"context"
	"errors"
	"fmt"
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

type StageCallback func(stage *Stage, status StageStatus, self *Pipeline, err error) bool

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
	callBack          StageCallback
}

func (p *Pipeline) Run(ctx context.Context, initialInput any) (any, error) {
	return p.RunWithCallback(ctx, initialInput, nil)
}

func (p *Pipeline) RunWithCallback(ctx context.Context, initialInput any, callback StageCallback) (any, error) {
	input := initialInput
	var globalErr error
	var runningStage *Stage
	total := len(p.stages)
	for i := p.currentStageIndex; i < total; i++ {
		stage := p.stages[i]
		runningStage = stage
		if fn.IsEmpty(stage.Name) {
			log.Printf("阶段 %d 名称未设置，跳过执行", i)
			globalErr = errors.New(fmt.Sprintf("阶段 %d 名称未设置，跳过执行", i))
			break
		}
		if stage.Status == "" {
			stage.Status = StageRunning
		}
		// 在执行Stage内的Units时，也可能需要参考currentUnitIndex
		// 并在每个Unit执行完后更新currentUnitIndex
		// 当Stage完成后，将currentUnitIndex重置，currentStageIndex+1
		// 检查是否暂停
		if callback != nil {
			exit := callback(runningStage, stage.Status, p, nil)
			if exit {
				globalErr = errors.New("user Stopped")
				break
			}
		}
		for {
			p.mu.Lock()
			if p.Status == "paused" {
				p.mu.Unlock()
				// 保存当前状态
				p.SaveState()
				time.Sleep(100 * time.Millisecond)
				time.Sleep(80 * time.Millisecond)
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				default:
				}
				continue
			}
			p.mu.Unlock()
			break
		}
		// 检查是否停止
		p.mu.Lock()
		if p.Status == "terminal" {
			if callback != nil {
				globalErr = errors.New("user Stopped")
				exit := callback(runningStage, stage.Status, p, globalErr)
				if exit {
					stage.Status = StageNeedTerminate
					break
				}
			}
			p.mu.Unlock()
			p.SaveState()
			globalErr = errors.New("user Terminal")
			break
		}
		p.mu.Unlock()
		out, err := stage.Run(ctx, input)
		if err != nil {
			globalErr = err
			stage.Status = StageFailed
			if callback != nil {
				_ = callback(runningStage, stage.Status, p, globalErr)
			}
			break
		}
		input = out
		if p.Results == nil {
			p.Results = make(map[string]any)
		}
		p.Results[stage.Name] = out
		stage.Status = StageCompleted
		globalErr = nil
		if callback != nil {
			_ = callback(runningStage, stage.Status, p, nil)
		}
	}
	if callback != nil {
		_ = callback(runningStage, ALLStageCompleted, p, globalErr)
	}
	//THINK THIS 当Stage完成后，将currentUnitIndex重置，currentStageIndex+1
	return input, nil
}

func NewPipeline(StateStore ExecutionState, stages ...*Stage) *Pipeline {
	return &Pipeline{stages: stages,
		StateStore: StateStore}
}

// SaveState 在合适时机调用saveState
func (p *Pipeline) SaveState() {
	if p.StateStore == nil {
		return
	}
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
	if p.StateStore == nil {
		return nil
	}
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
