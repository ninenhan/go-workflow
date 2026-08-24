# 在 Go 应用中嵌入 go-workflow

本文介绍如何把 `go-workflow` 当作普通 Go 依赖使用，而不是启动仓库自带的
`workflow-server`。示例包含自定义执行单元、工作流创建与发布、同步/异步执行、
SQLite 持久化、凭据和生命周期配置。

完整可运行代码位于
[`examples/embed-persistent/main.go`](../examples/embed-persistent/main.go)。

如果需要“一个 workflow node 内部并发多个业务 Unit”，并让下一组并发 Unit 读取前一组
的完整聚合结果，参考
[`examples/two-stage-parallel`](../examples/two-stage-parallel)：

```text
workflow node: stage-abc [A || B || C]
                         |
workflow node: stage-ef  [E || F]
```

```bash
go run ./examples/two-stage-parallel
```

这个例子通过节点 `Params.units` 确定子 Unit，通过 `Params.max_concurrency` 限制并发。
`stage-abc` 的聚合输出会进入标准 Unit `state["stage-abc"]`，因此 `stage-ef` 内并发执行
的 E、F 都能读取完整的 A/B/C 结果。子 Unit 不单独生成 `NodeRun`；详细配置和运行边界见
示例目录中的 [`README.md`](../examples/two-stage-parallel/README.md)。

从仓库根目录运行：

```bash
go run ./examples/embed-persistent
```

输出类似：

```text
run=Frbu1IkV status=success result=hello, go-workflow
```

Demo 的主流程调用统一的 `openWorkflowApplication`，没有绑定具体数据库。默认使用内存存储，
不会创建数据库：

```bash
go run ./examples/embed-persistent
```

启用本地 SQLite 持久化：

```bash
WORKFLOW_DATABASE_DRIVER=sqlite go run ./examples/embed-persistent
```

SQLite 数据库和加密凭据文件会创建在 `var/go-workflow`。切换 MySQL：

```bash
WORKFLOW_DATABASE_DRIVER=mysql \
WORKFLOW_DATABASE_DSN='user:password@tcp(127.0.0.1:3306)/workflow?charset=utf8mb4&parseTime=True&loc=UTC' \
go run ./examples/embed-persistent
```

数据库选择与 Store/Repository 装配位于
[`examples/embed-persistent/storage.go`](../examples/embed-persistent/storage.go)。

把模块作为依赖引入、执行 `go get` 或编译宿主应用，都不会运行 `examples` 中的任何
`main` 函数。Go 只会编译宿主实际 import 的 package；示例只有在显式执行
`go run ./examples/...` 或构建对应目录时才会运行。示例使用的依赖仍可能出现在本模块的
依赖图中，但不会因此初始化或启动示例业务。

## 1. 引入依赖

```bash
go get github.com/ninenhan/go-workflow
```

宿主应用通常只需要直接使用这些包：

```go
import (
	workflow "github.com/ninenhan/go-workflow"
    "github.com/ninenhan/go-workflow/core/definition"
    wfruntime "github.com/ninenhan/go-workflow/core/runtime"
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
- 根包 `workflow`：默认 ORM、完整内存模式和严格自定义装配入口。

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

通过 facade 创建 worker 并注册：

```go
app, err := workflow.OpenMemoryWithOptions(ctx, workflow.RuntimeOptions{
    Builtins: workflow.BuiltinStandard,
    ConfigureWorker: func(workerService *worker.Service) error {
        return workerService.UnitRegistry().RegisterUnitFactory(
            "GreetingUnit",
            func() workerunit.ExecutableUnit { return &GreetingUnit{} },
        )
    },
})
```

注册名就是节点的 `executor.ref`。重复注册会返回错误；只有明确需要热替换时才调用
`RegisterOrReplace`。

`Params` 会在执行前通过 JSON 映射到 Unit 的公开字段，因此节点里的
`{"prefix":"hello, "}` 会写入 `GreetingUnit.Prefix`。

## 3. 最小内存配置

显式使用包含 AutomationStore 和 CredentialStore 的完整内存实现：

```go
app, err := workflow.OpenMemory(ctx)
if err != nil {
    return err
}
defer app.Close()
service := app.Scheduler
```

这种模式不创建文件，适合测试、命令行工具和短生命周期任务。进程退出后以下内容都会丢失：

- 工作流及版本；
- 运行记录、事件和快照；
- 自动化调度状态；
- 内存凭据。

## 4. 持久化配置

持久化并不绑定 SQLite。`scheduler.Options` 接收的是 Store/Repository 接口；
SQLite 只是当前提供的一站式本地适配器。

### 4.1 SQLite 零配置适配器

根包默认 ORM 固定为单实例 SQLite。它会创建表、启用 WAL、执行完整性检查，
并把定义、工作区、运行记录和自动化状态放进同一个数据库，同时使用加密文件保存凭据。

```go
app, err := workflow.OpenDefaultWithOptions(ctx, workflow.DefaultOptions{
    DataDirectory: "var/go-workflow",
    Runtime: workflow.RuntimeOptions{
        CredentialScope: "my-app",
        Builtins:        workflow.BuiltinStandard,
    },
})
if err != nil {
    return err
}
defer app.Close()
service := app.Scheduler
```

完全使用默认配置时可以直接调用 `workflow.OpenDefault(ctx)`；默认数据目录是
`var/go-workflow`。SQLite 打开失败会直接返回错误，绝不会退回内存。

注意：

- 数据目录在 Unix 上必须禁止 group/world 访问；新目录会以 `0700` 创建。
- 数据库和凭据文件会以 `0600` 创建。
- `localdb.Open` 使用独占锁，适用于单进程嵌入式部署；多实例部署应提供共享数据库适配器。
- 凭据使用独立的 AES-256-GCM 加密文件，不会写进工作流定义或运行结果。
- `DefaultCredentialScope` 会用于 HTTP 发布入口和自动化运行。直接调用 `RunVersion`
  时，如节点需要凭据，应在传入的 `WorkflowRun.CredentialScope` 中显式设置 scope。

### 4.2 MySQL 手动装配

`localdb.Open` 包含 SQLite 专属的 WAL、文件权限、完整性检查和独占锁，因此不能拿来打开
MySQL。MySQL 应由宿主应用创建 Gorm 连接，再交给统一的 `persist/gormstore` 适配器：

```bash
go get gorm.io/driver/mysql
```

```go
import (
    "time"

    workflow "github.com/ninenhan/go-workflow"
    "github.com/ninenhan/go-workflow/core/credential"
    "github.com/ninenhan/go-workflow/persist/gormstore"
    gormmysql "gorm.io/driver/mysql"
    "gorm.io/gorm"
)

db, err := gorm.Open(gormmysql.Open(mysqlDSN), &gorm.Config{})
if err != nil {
    return err
}

sqlDB, err := db.DB()
if err != nil {
    return err
}
sqlDB.SetMaxOpenConns(32)
sqlDB.SetMaxIdleConns(8)
sqlDB.SetConnMaxLifetime(30 * time.Minute)

credentials, err := credential.OpenFileStore("var/go-workflow/credentials")
if err != nil {
    return err
}

stores, err := gormstore.New(ctx, db, gormstore.Options{
    Credentials: credentials,
    // 共享数据库默认禁止全局中断恢复，避免误杀其他实例的运行。
    Recovery: gormstore.RecoveryDisabled,
    Close:    sqlDB.Close,
})
if err != nil {
    return err
}

app, err := workflow.New(ctx, workflow.Options{
    Stores: stores,
    Runtime: workflow.RuntimeOptions{
        CredentialScope: "my-app",
        Builtins:        workflow.BuiltinStandard,
    },
})
if err != nil {
    return err
}
service := app.Scheduler
```

`Application.Close` 会先停止 scheduler，再关闭由 Stores 接管的连接池：

```go
if err := app.Close(); err != nil {
    return err
}
```

PostgreSQL 的装配方式相同，只需由宿主应用提供 `gorm.io/driver/postgres` 创建的
`*gorm.DB`。

当前支持边界：

| 后端 | 当前状态 |
|---|---|
| Memory | 完整支持并测试 |
| SQLite + `workflow.OpenDefault` | 完整集成并测试 |
| MySQL + Gorm | 可手动装配，尚未纳入正式集成测试矩阵 |
| PostgreSQL + Gorm | 可手动装配，尚未纳入正式集成测试矩阵 |
| 自定义实现 | 可实现 Store/Repository 接口接入 |

Gorm 构造器会执行 `AutoMigrate`，但“可以连接”不等于已经完成生产兼容验证。正式启用
MySQL/PostgreSQL 前，应针对目标版本验证 JSON 字段、upsert、并发版本创建、自动化任务抢占
和事务隔离行为；失败时应直接阻止服务启动，不能退回内存或 SQLite。

只有确定当前进程独占整个数据库中的 scheduler 运行时，才可以选择
`gormstore.RecoveryFailInterrupted`。共享数据库必须保持 `RecoveryDisabled`，并由带实例
ownership/lease 的恢复机制处理失联任务。

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

`Application` 统一管理 scheduler 和存储的关闭顺序。宿主应用退出前调用：

```go
shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

if err := app.Shutdown(shutdownContext); err != nil {
    return err
}
```

`Shutdown` 会停止自动化循环、取消仍在执行的后台 run，等待运行状态完成收敛，然后关闭
由 `Stores` 接管的数据库连接。若超时，存储保持打开，调用方可以使用新的 context 重试；
`Close` 是带五秒超时的便利方法，并且可以安全地重复调用。

## 9. 推荐配置

| 场景 | Worker | Definition/Runtime Store | Credentials |
|---|---|---|---|
| 单元测试 | embedded | memory | memory/environment |
| CLI、一次性任务 | embedded | memory 或 SQLite | file/environment |
| 单机服务 | embedded | SQLite | encrypted file store |
| 多实例服务 | remote worker | 共享持久化适配器 | 集中式 resolver/store |

应用只应在启动阶段完成 Unit/Executor 注册。运行期间需要热替换时，必须自行处理正在执行的
旧任务和新版本定义之间的兼容关系。
