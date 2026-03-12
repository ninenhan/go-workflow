# go-workflow

`go-workflow` 是一个面向编排与调度的 Go 工作流引擎，当前按三段式架构组织：

1. `core`
   共享定义、执行计划、运行时、执行器抽象、worker 协议。
2. `scheduler`
   控制面，负责 workflow/version 管理、编译、调度、状态机、运行时存储、发布入口。
3. `worker`
   数据面，负责真正执行节点任务；可以内嵌到调度器，也可以独立部署。

项目当前已经支持：

- 嵌入式单进程运行
- 独立 worker 注册/心跳/远程执行
- `unit` / `local_go` / `http` / `queue` / `remote` / `container` executor
- workflow/version 管理 API
- run 查询、事件、快照
- publish route / HTTP trigger
- pause / resume / cancel
- 分支条件、节点级 loop、显式 back-edge
- 内存 store 和 Gorm store

## 架构

```mermaid
flowchart LR
    A["Workflow Definition"] --> B["Compiler / ExecutionPlan"]
    B --> C["Scheduler"]
    C --> D["Embedded Worker"]
    C --> E["Remote Worker"]
    D --> F["Executors / Units"]
    E --> F
    C --> G["Runtime Store"]
    H["HTTP API / Publish Route / Trigger"] --> C
```

## 安装

```bash
go get github.com/ninenhan/go-workflow
```

## 最小可运行示例

下面这个示例以嵌入式 worker 方式运行，不需要额外进程：

```go
package main

import (
	"context"
	"fmt"

	"github.com/ninenhan/go-workflow/core/definition"
	"github.com/ninenhan/go-workflow/scheduler"
	"github.com/ninenhan/go-workflow/worker"
	workerunit "github.com/ninenhan/go-workflow/worker/unit"
)

type GreetingUnit struct {
	workerunit.Unit
}

func (u *GreetingUnit) GetUnitName() string { return "GreetingUnit" }
func (u *GreetingUnit) GetUnitMeta() *workerunit.Unit { return &u.Unit }

func (u *GreetingUnit) Execute(ctx context.Context, state workerunit.ContextMap, self *workerunit.Node) (*workerunit.ExecutionResult, error) {
	name := "world"
	if prev := state["prepare"]; prev != nil {
		if text, ok := prev.Data.(string); ok && text != "" {
			name = text
		}
	}
	return &workerunit.ExecutionResult{
		NodeName: u.GetUnitName(),
		Data: map[string]any{
			"message": "hello, " + name,
		},
	}, nil
}

func main() {
	workerSvc, err := worker.NewService(worker.Options{
		Enabled:          true,
		RegisterBuiltins: true,
	})
	if err != nil {
		panic(err)
	}

	workerSvc.UnitRegistry().RegisterUnitFactory("GreetingUnit", func() workerunit.ExecutableUnit {
		unit := &GreetingUnit{}
		unit.UnitName = unit.GetUnitName()
		return unit
	})

	svc, err := scheduler.NewService(scheduler.Options{
		EnableEmbeddedWorker: true,
		EmbeddedWorker:       workerSvc,
	})
	if err != nil {
		panic(err)
	}

	run, err := svc.RunDefinition(context.Background(), &definition.WorkflowDefinition{
		ID:         "wf-quickstart",
		Name:       "quickstart",
		EntryNodes: []string{"prepare"},
		Nodes: []definition.Node{
			{
				ID:       "prepare",
				Name:     "prepare",
				Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "RemarkUnit"},
				Input:    "go-workflow",
			},
			{
				ID:       "greet",
				Name:     "greet",
				Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "GreetingUnit"},
			},
		},
		Edges: []definition.Edge{
			{From: "prepare", To: "greet"},
		},
	}, nil)
	if err != nil {
		panic(err)
	}

	fmt.Println("run status:", run.Status)
	fmt.Printf("greet result: %#v\n", run.Context.NodeResults["greet"])
}
```

输出会类似：

```text
run status: success
greet result: map[string]interface {}{"message":"hello, go-workflow"}
```

## 运行模式

### 1. 嵌入式模式

适合本地开发、单机部署、功能验证。

```go
svc, err := scheduler.NewService(scheduler.Options{
	EnableEmbeddedWorker: true,
})
```

### 2. 纯调度器模式

调度器只负责编排与状态机，执行全部下发到独立 worker。

```go
svc, err := scheduler.NewService(scheduler.Options{
	EnableEmbeddedWorker: false,
})
```

独立 worker 暴露自己的执行接口，并向 scheduler 注册：

```go
import (
	"context"
	"net/http"

	"github.com/ninenhan/go-workflow/core"
	"github.com/ninenhan/go-workflow/worker"
)

workerSvc, err := worker.NewService(worker.Options{
	Enabled:          true,
	RegisterBuiltins: true,
})
if err != nil {
	panic(err)
}

go http.ListenAndServe(":9090", workerSvc.Handler())

descriptor := workerSvc.Descriptor(core.WorkerDescriptor{
	ID:            "worker-1",
	Name:          "default-worker",
	Endpoint:      "http://127.0.0.1:9090",
	Tenant:        "default",
	Labels:        map[string]string{"pool": "default"},
	Weight:        1,
	MaxConcurrent: 32,
})

go func() {
	if err := workerSvc.MaintainRegistration(context.Background(), worker.RegistrationOptions{
		SchedulerEndpoint: "http://127.0.0.1:8080",
		Descriptor:        descriptor,
	}); err != nil {
		panic(err)
	}
}()
```

## HTTP API

scheduler 自带 HTTP 控制面：

- `POST /v1/workflows`
- `GET /v1/workflows`
- `POST /v1/workflows/{workflow_id}/versions`
- `POST /v1/workflow-versions/{version_id}/publish`
- `POST /v1/workflow-versions/{version_id}/runs`
- `GET /v1/runs`
- `GET /v1/runs/{id}`
- `GET /v1/runs/{id}/events`
- `GET /v1/runs/{id}/snapshots`
- `POST /v1/runs/{id}/pause`
- `POST /v1/runs/{id}/resume`
- `POST /v1/runs/{id}/cancel`
- `GET /v1/workers`
- `POST /v1/workers/register`
- `POST /v1/workers/heartbeat`

还支持：

- `PublishConfig.Route` 发布为业务 API
- `TriggerHTTP` 直接触发工作流

## 一个简单的 workflow version 发布流程

创建 workflow：

```bash
curl -X POST http://127.0.0.1:8080/v1/workflows \
  -H 'Content-Type: application/json' \
  -d '{
    "id": "wf-doc-demo",
    "name": "doc demo"
  }'
```

创建 version：

```bash
curl -X POST http://127.0.0.1:8080/v1/workflows/wf-doc-demo/versions \
  -H 'Content-Type: application/json' \
  -d '{
    "version": {
      "id": "wf-doc-demo:v1",
      "workflow_id": "wf-doc-demo",
      "version": 1,
      "status": "draft",
      "definition": {
        "id": "wf-doc-demo",
        "name": "doc demo",
        "entry_nodes": ["n1"],
        "publish_config": {
          "enabled": true,
          "route": "/api/doc-demo",
          "method": "POST"
        },
        "nodes": [
          {
            "id": "n1",
            "name": "echo",
            "executor": {
              "type": "unit",
              "ref": "LogUnit"
            },
            "input": "hello from published workflow"
          }
        ]
      }
    }
  }'
```

发布 version：

```bash
curl -X POST http://127.0.0.1:8080/v1/workflow-versions/wf-doc-demo:v1/publish
```

调用发布路由：

```bash
curl -X POST http://127.0.0.1:8080/api/doc-demo \
  -H 'Content-Type: application/json' \
  -d '{"hello":"world"}'
```

## LLMUnit 示例

内置 `LLMUnit` 走 `unit` executor，要求环境变量里有 `OPENAI_API_KEY`：

```json
{
  "id": "llm",
  "name": "ask-model",
  "executor": {
    "type": "unit",
    "ref": "LLMUnit"
  },
  "input": "用一句话介绍 go-workflow",
  "params": {
    "model": "gpt-4o-mini",
    "system": "你是一个简洁的技术助手",
    "stream": false,
    "timeout_ms": 30000
  }
}
```

## 节点输入绑定

当前支持显式声明“这个节点的输入来自哪些直接前驱节点的 output”。

规则：

1. `edge` / `depends_on` 决定节点什么时候可以执行。
2. `input_spec` 决定真正传给 executor 的 `task.Input` 怎么组装。
3. `source=node` 时，`input_spec.bindings[].from` 必须是当前节点的直接前驱，否则编译失败。
4. 不配置 `input_spec` 时，行为保持旧语义，仍然直接使用节点自己的 `input`。
5. `default` 可以为缺失输入提供兜底值。
6. `transform` 可以对绑定结果做轻量表达式变换。
7. 现在还支持多来源绑定：`node / var / request / run`。

示例：

```json
{
  "id": "summary",
  "name": "summary",
  "executor": {
    "type": "unit",
    "ref": "LogUnit"
  },
  "input": {
    "kind": "summary"
  },
  "input_spec": {
    "mode": "object",
    "bindings": [
      { "from": "user", "path": "name", "as": "user", "required": true },
      { "from": "score", "path": "value", "as": "score", "required": true }
    ]
  }
}
```

这会把最终输入组装成：

```json
{
  "kind": "summary",
  "user": "...",
  "score": 98
}
```

其中：

1. `source=node` 时，`from` 表示前驱节点 ID
2. `source=var` 时，`from` 表示工作流运行变量名
3. `source=request` 时，默认从 `run.Context.Variables["request"]` 取值
4. `source=run` 时，可读取 `run_id/workflow_id/workflow_version_id/variables/node_results` 等运行态字段

也支持：

```json
{
  "input_spec": {
    "mode": "object",
    "bindings": [
      { "from": "user", "path": "name", "as": "name", "required": true, "transform": "Value + \"-vip\"" },
      { "from": "user", "path": "title", "as": "title", "default": "guest" }
    ]
  }
}
```

## 文档

- [Quickstart](/Users/freddon/Lab/go_modules/github.com/go-workflow/docs/quickstart.md)
- [Three Part Architecture](/Users/freddon/Lab/go_modules/github.com/go-workflow/docs/three-part-architecture.md)
- [Control Plane API](/Users/freddon/Lab/go_modules/github.com/go-workflow/docs/control-plane-api.md)
- [Worker Runtime](/Users/freddon/Lab/go_modules/github.com/go-workflow/docs/worker-runtime.md)
- [Runtime Control](/Users/freddon/Lab/go_modules/github.com/go-workflow/docs/runtime-control.md)
- [Runtime Store](/Users/freddon/Lab/go_modules/github.com/go-workflow/docs/runtime-store.md)

## 当前边界

当前已经具备生产级骨架，但有几个边界要明确：

1. `scheduler` 是控制面，不建议承载复杂执行实现。
2. 标准单元注册建议放到 worker。
3. `queue` 当前已经可用，但内置 broker 主要用于单机/验证场景。
4. graph loop 是受控循环，不是任意无约束环图。
