# 在 Go 应用中嵌入 go-workflow

本文介绍如何把 `go-workflow` 当作普通 Go 依赖使用，而不是启动仓库自带的
`workflow-server`。示例包含自定义执行单元、工作流创建与发布、同步/异步执行、
SQLite 持久化、凭据和生命周期配置。

完整可运行代码位于
[`examples/embed-persistent/main.go`](../examples/embed-persistent/main.go)。

从仓库根目录运行：

```bash
go run ./examples/embed-persistent
```

输出类似：

```text
run=Frbu1IkV status=success result=hello, go-workflow
```

示例会把数据库和加密凭据文件创建在 `var/go-workflow`。

## 1. 引入依赖

```bash
go get github.com/ninenhan/go-workflow
```

宿主应用通常只需要直接使用这些包：

```go
import (
    "github.com/ninenhan/go-workflow/core/definition"
    wfruntime "github.com/ninenhan/go-workflow/core/runtime"
    "github.com/ninenhan/go-workflow/scheduler"
    "github.com/ninenhan/go-workflow/worker"
    workerunit "github.com/ninenhan/go-workflow/worker/unit"
)
```

职责边界如下：

- `definition`：工作流、节点、边和版本定义。
- `worker/unit`：业务执行单元 SDK。
- `worker`：执行节点，可内嵌或独立部署。
- `scheduler`：编译定义、调度节点和管理运行状态。
- `runtime`：运行记录、状态、事件和持久化接口。

## 2. 定义并注册自己的 Unit

Unit 适合订单处理、发消息、调用内部服务等业务级节点。每次执行都会通过 factory
创建新实例，因此不要依赖跨运行的实例字段保存状态。

```go
type GreetingUnit struct {
    workerunit.Unit
    Prefix string `json:"prefix"`
}

func (u *GreetingUnit) GetUnitName() string { return "GreetingUnit" }
func (u *GreetingUnit) GetUnitMeta() *workerunit.Unit { return &u.Unit }

func (u *GreetingUnit) Execute(
    ctx context.Context,
    state workerunit.ContextMap,
    self *workerunit.Node,
) (*workerunit.ExecutionResult, error) {
    if self == nil || self.Input == nil {
        return nil, errors.New("GreetingUnit: input is required")
    }
    return workerunit.SimpleResult(u.Prefix + fmt.Sprint(self.Input.Data)), nil
}
```

创建 worker 并注册：

```go
workerService, err := worker.NewService(worker.Options{
    Enabled:          true,
    RegisterBuiltins: true,
})
if err != nil {
    return err
}

if err := workerService.UnitRegistry().RegisterUnitFactory(
    "GreetingUnit",
    func() workerunit.ExecutableUnit { return &GreetingUnit{} },
); err != nil {
    return err
}
```

注册名就是节点的 `executor.ref`。重复注册会返回错误；只有明确需要热替换时才调用
`RegisterOrReplace`。

`Params` 会在执行前通过 JSON 映射到 Unit 的公开字段，因此节点里的
`{"prefix":"hello, "}` 会写入 `GreetingUnit.Prefix`。

## 3. 最小内存配置

不传 Store 和 Repository 时，scheduler 默认使用内存实现：

```go
service, err := scheduler.NewService(scheduler.Options{
    EnableEmbeddedWorker: true,
    EmbeddedWorker:       workerService,
})
```

这种模式不创建文件，适合测试、命令行工具和短生命周期任务。进程退出后以下内容都会丢失：

- 工作流及版本；
- 运行记录、事件和快照；
- 自动化调度状态；
- 内存凭据。

## 4. SQLite 持久化配置

项目提供了已经配置好的 SQLite 聚合入口。它会创建表、启用 WAL、执行完整性检查，
并把定义、工作区、运行记录和自动化状态放进同一个数据库。

```go
dataDirectory := filepath.Join("var", "go-workflow")
database, err := localdb.Open(filepath.Join(dataDirectory, "workflow.db"))
if err != nil {
    return err
}

credentials, err := credential.OpenFileStore(
    filepath.Join(dataDirectory, "credentials"),
)
if err != nil {
    return err
}

workerService, err := worker.NewService(worker.Options{
    Enabled:            true,
    RegisterBuiltins:   true,
    CredentialResolver: credentials,
})
if err != nil {
    return err
}

service, err := scheduler.NewService(scheduler.Options{
    EnableEmbeddedWorker:   true,
    EmbeddedWorker:         workerService,
    Store:                  database.Runtime,
    Definitions:            database.Definitions,
    Workspace:              database.Workspace,
    Automations:            database.Automations,
    Credentials:            credentials,
    DefaultCredentialScope: "my-app",
})
```

注意：

- 数据目录在 Unix 上必须禁止 group/world 访问；新目录会以 `0700` 创建。
- 数据库和凭据文件会以 `0600` 创建。
- `localdb.Open` 使用独占锁，适用于单进程嵌入式部署；多实例部署应提供共享数据库适配器。
- 凭据使用独立的 AES-256-GCM 加密文件，不会写进工作流定义或运行结果。
- `DefaultCredentialScope` 会用于 HTTP 发布入口和自动化运行。直接调用 `RunVersion`
  时，如节点需要凭据，应在传入的 `WorkflowRun.CredentialScope` 中显式设置 scope。

## 5. 创建、保存并发布工作流

```go
const workflowID = "embedded-greeting"

if err := service.SaveWorkflow(ctx, &definition.Workflow{
    ID:   workflowID,
    Name: "embedded greeting",
}); err != nil {
    return err
}

version, err := service.CreateVersion(ctx, workflowID, &definition.WorkflowDefinition{
    ID:         workflowID,
    Name:       "embedded greeting",
    EntryNodes: []string{"name"},
    Nodes: []definition.Node{
        {
            ID:       "name",
            Executor: definition.ExecutorSpec{
                Type: definition.ExecutorTypeUnit,
                Ref:  "RemarkUnit",
            },
            Input: "go-workflow",
        },
        {
            ID: "greeting",
            Executor: definition.ExecutorSpec{
                Type: definition.ExecutorTypeUnit,
                Ref:  "GreetingUnit",
            },
            InputSpec: &definition.InputSpec{
                Mode: definition.InputModeReplace,
                Bindings: []definition.InputBinding{
                    {
                        Source:   definition.InputSourceNode,
                        From:     "name",
                        Required: true,
                    },
                },
            },
            Params: map[string]any{"prefix": "hello, "},
        },
    },
    Edges: []definition.Edge{{From: "name", To: "greeting"}},
})
if err != nil {
    return err
}

published, err := service.PublishVersion(ctx, version.ID)
if err != nil {
    return err
}
```

`CreateVersion` 创建不可变的定义快照。`PublishVersion` 会先经过同一个编译器校验，
并把该版本设为 workflow 的活动版本。创建新版本不会修改已经保存的旧版本。

如果只是执行一次临时工作流，可以跳过保存和发布：

```go
result, err := service.RunDefinition(ctx, workflowDefinition, nil)
```

临时定义的运行记录仍会写入配置的 Runtime Store，但定义本身不会进入 Definition Repository。

## 6. 同步和异步运行

同步执行会等待工作流结束：

```go
result, err := service.RunVersion(ctx, published, &wfruntime.WorkflowRun{
    CredentialScope: "my-app",
})
if err != nil {
    return err
}
fmt.Println(result.Status)
fmt.Println(result.Context.NodeResults["greeting"])
```

异步执行会先持久化 pending run，然后立即返回 run ID：

```go
accepted, err := service.StartVersion(ctx, published, &wfruntime.WorkflowRun{
    CredentialScope: "my-app",
})
if err != nil {
    return err
}

current, err := service.LoadRun(ctx, accepted.ID)
```

宿主应用可以通过这些接口查询运行信息：

```go
run, err := service.LoadRun(ctx, runID)
events, err := service.RunEvents(ctx, runID)
snapshots, err := service.RunSnapshots(ctx, runID)
runs, err := service.ListRuns(ctx)
```

暂停、恢复和取消分别使用 `PauseRun`、`ResumeRun` 和 `CancelRun`。

## 7. 自动化和 HTTP API（可选）

工作流包含 cron trigger，并且配置了持久化 `Automations` 时，还需要启动调度循环：

```go
if err := service.StartAutomations(ctx, time.Second); err != nil {
    return err
}
```

如果宿主应用希望同时暴露管理和运行 API，可以直接挂载 scheduler handler：

```go
server := &http.Server{
    Addr:    ":8080",
    Handler: service.Handler(),
}
```

纯库调用不需要启动 HTTP Server。

## 8. 生命周期

宿主应用退出前应先停止 scheduler，再关闭数据库：

```go
shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

if err := service.Shutdown(shutdownContext); err != nil {
    return err
}
if err := database.Close(); err != nil {
    return err
}
```

`Shutdown` 会停止自动化循环、取消仍在执行的后台 run，并等待运行状态完成收敛。

## 9. 推荐配置

| 场景 | Worker | Definition/Runtime Store | Credentials |
|---|---|---|---|
| 单元测试 | embedded | memory | memory/environment |
| CLI、一次性任务 | embedded | memory 或 SQLite | file/environment |
| 单机服务 | embedded | SQLite | encrypted file store |
| 多实例服务 | remote worker | 共享持久化适配器 | 集中式 resolver/store |

应用只应在启动阶段完成 Unit/Executor 注册。运行期间需要热替换时，必须自行处理正在执行的
旧任务和新版本定义之间的兼容关系。
