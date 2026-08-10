# Quickstart

这份文档给你一个最短路径，把 `go-workflow` 跑起来，并理解怎么扩展。

## 1. 先理解三层职责

### `core`

共享模型和抽象：

- workflow definition
- execution plan
- runtime state
- executor interface
- worker protocol

后续如果你单独创建一个 worker 项目，优先依赖 `github.com/ninenhan/go-workflow/core`。

### `scheduler`

负责：

- workflow/version 管理
- 编译 definition 为 execution plan
- 调度、依赖判定、状态机推进
- 事件、快照、运行状态存储
- publish route / trigger / control plane API

### `worker`

负责：

- 执行节点
- 注册 executor
- 注册 unit
- 向 scheduler 注册和发送心跳

## 2. 最小 demo：嵌入式模式

先从单进程跑通。这个模式最适合本地开发。

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

	fmt.Println("run id:", run.ID)
	fmt.Println("run status:", run.Status)
	fmt.Printf("greet result: %#v\n", run.Context.NodeResults["greet"])
}
```

### 这个 demo 说明了什么

1. scheduler 可以带一个内置 worker。
2. unit 是注册在 worker 里的，不是注册在 scheduler 里的。
3. 后继节点可以通过 `state["prepare"]` 读取前序节点输出。

## 3. 如何定义一个节点

最核心的是 `definition.Node`：

```go
type Node struct {
	ID       string
	Name     string
	Executor definition.ExecutorSpec
	Input    any
	Params   map[string]any
	Retry    definition.RetryPolicy
	Loop     *definition.LoopPolicy
	Branch   *definition.BranchPolicy
}
```

`ExecutorSpec` 指定“怎么执行”：

```go
type ExecutorSpec struct {
	Type string
	Ref  string
}
```

常见值：

- `unit`: 执行一个标准单元或业务单元
- `local_go`: 执行本地注册的 Go function
- `http`: 调一个 HTTP 接口
- `queue`: 进入异步队列
- `remote`: 交给远程 worker
- `container`: 交给容器型执行器

## 4. 如何声明节点输入

现在支持 `input_spec`，用于从直接前驱节点输出里显式绑定输入。

### 设计规则

1. `edge` 和 `depends_on` 只表达依赖关系。
2. `input_spec` 表达“用哪些前驱输出作为当前节点输入”。
3. `bindings[].from` 必须是当前节点的直接前驱节点。
4. `required=true` 时，如果前驱输出或路径不存在，当前节点执行失败。
5. `default` 可在值缺失时兜底。
6. `transform` 可对绑定结果做轻量表达式变换。
7. `source` 现在支持 `node / var / request / run`。

### 支持的模式

- `replace`: 用单个绑定值直接作为 `task.Input`
- `object`: 把多个绑定值合并进对象
- `array`: 把多个绑定值追加进数组

### 支持的来源

- `node`: 前驱节点 output
- `var`: `run.Context.Variables`
- `request`: HTTP trigger / publish 注入的请求上下文
- `run`: 运行态元数据和 scope

### 示例 1：单前驱替换输入

```json
{
  "id": "consumer",
  "executor": { "type": "unit", "ref": "LogUnit" },
  "input_spec": {
    "mode": "replace",
    "bindings": [
      { "from": "producer", "path": "message", "required": true }
    ]
  }
}
```

如果 `producer` 输出：

```json
{ "message": "hello-binding" }
```

那么 `consumer` 收到的 `task.Input` 就是：

```json
"hello-binding"
```

### 示例 2：多前驱对象合并

```json
{
  "id": "summary",
  "executor": { "type": "unit", "ref": "LogUnit" },
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

最终输入：

```json
{
  "kind": "summary",
  "user": "alice",
  "score": 98
}
```

### 示例 4：全局变量、request、run scope

```json
{
  "id": "summary",
  "executor": { "type": "unit", "ref": "LogUnit" },
  "input_spec": {
    "mode": "object",
    "bindings": [
      { "source": "var", "from": "tenant_id", "as": "tenant_id", "required": true },
      { "source": "request", "from": "body.message", "as": "message", "required": true },
      { "source": "run", "from": "workflow_id", "as": "workflow_id", "required": true }
    ]
  }
}
```

### 示例 3：默认值和变换

```json
{
  "id": "profile",
  "executor": { "type": "unit", "ref": "LogUnit" },
  "input": {
    "kind": "profile"
  },
  "input_spec": {
    "mode": "object",
    "bindings": [
      { "from": "user", "path": "name", "as": "name", "required": true, "transform": "Value + \"-vip\"" },
      { "from": "user", "path": "title", "as": "title", "default": "guest" }
    ]
  }
}
```

这里的语义是：

1. `name` 从 `user.name` 取值，并拼成 `alice-vip`
2. `title` 如果不存在，则使用 `"guest"`

不配置 `input_spec` 时，节点仍然直接使用 `input` 字段，兼容旧逻辑。

## 5. 如何扩展一个自己的 unit

把 unit 放在 worker 侧。

```go
type MyUnit struct {
	workerunit.Unit
}

func (u *MyUnit) GetUnitName() string { return "MyUnit" }
func (u *MyUnit) GetUnitMeta() *workerunit.Unit { return &u.Unit }

func (u *MyUnit) Execute(ctx context.Context, state workerunit.ContextMap, self *workerunit.Node) (*workerunit.ExecutionResult, error) {
	return &workerunit.ExecutionResult{
		NodeName: u.GetUnitName(),
		Data: map[string]any{
			"input": self.Input.Data,
			"prev":  state["prepare"].Data,
		},
	}, nil
}
```

注册方式：

```go
workerSvc.UnitRegistry().RegisterUnitFactory("MyUnit", func() workerunit.ExecutableUnit {
	unit := &MyUnit{}
	unit.UnitName = unit.GetUnitName()
	return unit
})
```

然后 workflow 里这样写：

```json
{
  "id": "n2",
  "name": "my-node",
  "executor": {
    "type": "unit",
    "ref": "MyUnit"
  }
}
```

## 6. 独立 worker 模式

当你不想让 scheduler 承担执行职责时，关闭 embedded worker：

```go
svc, err := scheduler.NewService(scheduler.Options{
	EnableEmbeddedWorker: false,
})
```

然后单独启动 worker：

```go
package main

import (
	"context"
	"net/http"

	"github.com/ninenhan/go-workflow/core"
	"github.com/ninenhan/go-workflow/worker"
)

func main() {
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
		Weight:        1,
		MaxConcurrent: 32,
		Labels: map[string]string{
			"pool": "default",
			"zone": "local",
		},
	})

	if err := workerSvc.MaintainRegistration(context.Background(), worker.RegistrationOptions{
		SchedulerEndpoint: "http://127.0.0.1:8080",
		Descriptor:        descriptor,
	}); err != nil {
		panic(err)
	}
}
```

scheduler 侧启动 HTTP handler：

```go
svc, err := scheduler.NewService(scheduler.Options{
	EnableEmbeddedWorker: false,
})
if err != nil {
	panic(err)
}
http.ListenAndServe(":8080", scheduler.NewHTTPHandler(svc).Handler())
```

## 7. queue 模式

如果你想让节点先入队，再由 worker 消费：

```go
broker := executor.NewInMemoryQueueBroker()

workerSvc, _ := worker.NewService(worker.Options{
	Enabled:          true,
	RegisterBuiltins: true,
})
_ = workerSvc.RegisterExecutor(executor.NewQueueExecutor(broker))

go workerSvc.ConsumeQueue(context.Background(), broker, "default", 10*time.Millisecond)
```

workflow 节点：

```json
{
  "id": "n1",
  "name": "queued-log",
  "executor": {
    "type": "queue",
    "ref": "default"
  },
  "input": "hello-queue",
  "params": {
    "async": true,
    "poll_interval": "10ms",
    "target_executor_type": "unit",
    "target_executor_ref": "LogUnit"
  }
}
```

这里的含义是：

1. 当前节点本身用 `queue` executor。
2. 真实消费时，worker 再把任务转给 `unit/LogUnit` 执行。

## 8. workflow/version API

### 创建 workflow

```bash
curl -X POST http://127.0.0.1:8080/v1/workflows \
  -H 'Content-Type: application/json' \
  -d '{
    "id": "wf-api-demo",
    "name": "api demo"
  }'
```

### 创建 version

```bash
curl -X POST http://127.0.0.1:8080/v1/workflows/wf-api-demo/versions \
  -H 'Content-Type: application/json' \
  -d '{
    "version": {
      "id": "wf-api-demo:v1",
      "workflow_id": "wf-api-demo",
      "version": 1,
      "status": "draft",
      "definition": {
        "id": "wf-api-demo",
        "name": "api demo",
        "entry_nodes": ["n1"],
        "nodes": [
          {
            "id": "n1",
            "name": "echo",
            "executor": { "type": "unit", "ref": "LogUnit" },
            "input": "hello api"
          }
        ]
      }
    }
  }'
```

### 发布 version

```bash
curl -X POST http://127.0.0.1:8080/v1/workflow-versions/wf-api-demo:v1/publish
```

### 运行 version

```bash
curl -X POST http://127.0.0.1:8080/v1/workflow-versions/wf-api-demo:v1/runs \
  -H 'Content-Type: application/json' \
  -d '{}'
```

### 查询 run

```bash
curl http://127.0.0.1:8080/v1/runs
curl http://127.0.0.1:8080/v1/runs/<run-id>
curl http://127.0.0.1:8080/v1/runs/<run-id>/events
curl http://127.0.0.1:8080/v1/runs/<run-id>/snapshots
```

## 9. 发布为 API

你可以把已发布的 workflow 直接暴露成 HTTP 路由：

```json
{
  "publish_config": {
    "enabled": true,
    "route": "/api/doc-demo",
    "method": "POST",
    "input_mode": "body",
    "response_mode": "result",
    "timeout": 10000
  }
}
```

- `input_mode: "body"` 将 JSON 对象字段映射为同名工作流变量；`query` 映射查询参数；`request` 只保留完整请求对象。
- `response_mode: "result"` 返回本次执行命中的 `TerminalUnit`（返回结果）输出；`run` 返回完整运行详情，适合调试。
- `timeout` 使用毫秒，范围为 `0` 到 `86400000`；`0` 表示不设置发布层超时。

`result` 模式要求工作流至少包含一个 `TerminalUnit`。条件分支可以各自包含返回动作，但单次执行必须只命中一个。

发布后可以读取机器可用的服务契约。契约包含实际访问地址、版本、输入模式、六语言字段名称、必填规则、选项和请求示例：

```bash
curl http://127.0.0.1:8080/v1/workflows/wf-doc-demo/contract
```

当 `input_mode` 为 `body` 或 `query` 时，后端会使用 `metadata.run_form` 校验并规范化输入。运行表单因此同时是 Web 表单和 API 输入契约，无需为每个服务另写校验代码。

Web 编辑器的 API 测试表单通过控制面调用同一个活动版本：

```bash
curl -X POST http://127.0.0.1:8080/v1/workflows/wf-doc-demo/invoke \
  -H 'Content-Type: application/json' \
  -d '{"input":{"message":"hello"}}'
```

该接口仍执行发布输入校验、超时和响应适配，不维护独立的测试执行分支。

耗时较短的工作流可以使用同步 HTTP，一次响应直接取得最终结果。耗时较长或需要展示执行进度时，先异步启动，再连接返回的 SSE 地址：

```bash
curl -X POST 'http://127.0.0.1:8080/v1/workflows/wf-doc-demo/invoke?wait=false' \
  -H 'Content-Type: application/json' \
  -d '{"input":{"message":"hello"}}'
```

启动成功返回 `202 Accepted`：

```json
{
  "run_id": "<run-id>",
  "status": "pending",
  "source": "publish",
  "run_url": "http://127.0.0.1:8080/v1/runs/<run-id>",
  "events_url": "http://127.0.0.1:8080/v1/runs/<run-id>/events",
  "stream_url": "http://127.0.0.1:8080/v1/runs/<run-id>/stream"
}
```

使用响应中的 `stream_url` 读取进度与最终结果：

```bash
curl -N http://127.0.0.1:8080/v1/runs/<run-id>/stream
```

事件流依次包含 `ready`、若干 `run_event`，最后发送一个 `result` 或 `error` 并关闭连接。客户端断线重连时可发送 `Last-Event-ID`，避免重复接收已确认的运行事件；终态事件 ID 固定为 `result`，携带该 ID 重连会返回 `204 No Content` 并停止自动重连。

发布后直接调用：

```bash
curl -X POST http://127.0.0.1:8080/api/doc-demo \
  -H 'Content-Type: application/json' \
  -d '{"hello":"world"}'
```

## 10. HTTP Trigger

也可以通过 trigger 方式触发：

```json
{
  "triggers": [
    {
      "id": "incoming-hook",
      "type": "http",
      "enabled": true,
      "config": {
        "route": "/hooks/incoming",
        "method": "POST"
      }
    }
  ]
}
```

触发请求：

```bash
curl -X POST http://127.0.0.1:8080/hooks/incoming \
  -H 'Content-Type: application/json' \
  -d '{"event":"incoming"}'
```

请求内容会注入到运行态上下文：

```text
run.Context.Variables["request"]
```

## 11. LLMUnit

如果你已经配置好 `OPENAI_API_KEY`，可以直接用内置 `LLMUnit`：

```json
{
  "id": "llm",
  "name": "ask-model",
  "executor": {
    "type": "unit",
    "ref": "LLMUnit"
  },
  "input": "请用一句话解释 go-workflow",
  "params": {
    "model": "gpt-4o-mini",
    "system": "你是一个简洁的技术助手",
    "stream": false,
    "timeout_ms": 30000
  }
}
```

如果需要流式响应：

```json
{
  "params": {
    "model": "gpt-4o-mini",
    "stream": true
  }
}
```

`LLMUnit` 返回的数据结构里会包含：

- `text`
- `usage`
- `raw`

流式时还会包含：

- `chunks`
- `raw_events`

## 12. 运行时控制

支持：

- pause
- resume
- cancel

```bash
curl -X POST http://127.0.0.1:8080/v1/runs/<run-id>/pause
curl -X POST http://127.0.0.1:8080/v1/runs/<run-id>/resume
curl -X POST http://127.0.0.1:8080/v1/runs/<run-id>/cancel
```

## 13. 什么时候选哪种模式

### 嵌入式模式

适合：

- 本地开发
- 小规模部署
- 先快速验证 workflow 定义和 unit 行为

### 独立 worker 模式

适合：

- 调度和执行分离
- 多语言、多资源池执行
- LLM、Python、容器任务下沉到专门 worker

### queue 模式

适合：

- 明显异步的长任务
- 需要削峰、重试、隔离执行资源

## 14. 当前最重要的使用建议

1. scheduler 只做编排和控制面，不要塞业务执行代码。
2. 自定义 unit 放在 worker。
3. workflow JSON 只表达静态定义，不要把运行时状态写回 definition。
4. 如果后面要拆独立 worker 项目，优先依赖 `core`，不要直接依赖 scheduler 内部实现。
