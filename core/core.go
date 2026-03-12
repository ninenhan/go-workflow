package core

import (
	"github.com/ninenhan/go-workflow/core/definition"
	"github.com/ninenhan/go-workflow/core/executor"
	"github.com/ninenhan/go-workflow/core/planning"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
	"github.com/ninenhan/go-workflow/core/workerproto"
	"gorm.io/gorm"
)

// Definition layer aliases exposed as a stable shared package.
type Workflow = definition.Workflow
type WorkflowVersion = definition.WorkflowVersion
type WorkflowDefinition = definition.WorkflowDefinition
type Node = definition.Node
type Edge = definition.Edge
type Trigger = definition.Trigger
type PublishConfig = definition.PublishConfig
type ExecutorSpec = definition.ExecutorSpec
type InputSpec = definition.InputSpec
type InputBinding = definition.InputBinding
type InputMode = definition.InputMode
type InputSource = definition.InputSource

// Planning layer aliases.
type ExecutionPlan = planning.ExecutionPlan
type PlanNode = planning.PlanNode
type Compiler = planning.Compiler

// Runtime layer aliases.
type WorkflowRun = wfruntime.WorkflowRun
type NodeRun = wfruntime.NodeRun
type RunContext = wfruntime.RunContext
type RunSnapshot = wfruntime.RunSnapshot
type RunEvent = wfruntime.RunEvent
type RuntimeStore = wfruntime.Store

// Executor layer aliases.
type Executor = executor.Executor
type AsyncExecutor = executor.AsyncExecutor
type ExecuteTask = executor.ExecuteTask
type ExecuteResult = executor.ExecuteResult
type ExecutorRegistry = executor.Registry
type ExecutorDispatcher = executor.Dispatcher

// Worker protocol aliases.
type WorkerDescriptor = workerproto.WorkerDescriptor
type WorkerStatus = workerproto.Status

func NewCompiler() Compiler {
	return planning.NewCompiler()
}

func NewMemoryStore() RuntimeStore {
	return wfruntime.NewMemoryStore()
}

func NewGormStore(db *gorm.DB) (RuntimeStore, error) {
	return wfruntime.NewGormStore(db)
}

func NewExecutorRegistry() *ExecutorRegistry {
	return executor.NewRegistry()
}
