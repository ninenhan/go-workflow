# 两阶段并发 Unit Demo

这个 Demo 的外层工作流只有两个运行节点：

```text
stage-abc [A || B || C] -> stage-ef [E || F]
```

运行：

```bash
go run ./examples/two-stage-parallel
```

`ParallelGroupUnit` 从节点 `Params` 读取下面两个配置：

```go
Params: map[string]any{
    "max_concurrency": 3,
    "units": []UnitCall{
        {ID: "A", Ref: "DemoProduceUnit", Input: "result-A"},
        {ID: "B", Ref: "DemoProduceUnit", Input: "result-B"},
        {ID: "C", Ref: "DemoProduceUnit", Input: "result-C"},
    },
}
```

- `units` 决定这个大节点要执行哪些子 Unit；每项都通过应用自己的 Unit Registry 创建独立实例。
- `max_concurrency` 决定该节点的最大并发数，必须大于零。
- 输出按子 Unit ID 聚合为 `map[string]any`，不依赖不确定的完成顺序。

第一个节点完成后，scheduler 会把聚合结果放入运行上下文的 `stage-abc`。第二个节点中的
E、F 都配置 `from: stage-abc`，并通过标准 Unit `state` 读取：

```go
upstream, ok := state[u.From]
if !ok || upstream == nil {
    return nil, fmt.Errorf("upstream result %q is unavailable", u.From)
}
abcResults := upstream.Data.(map[string]any)
```

## 运行边界

子 Unit 是大节点内部的并发调用，不会分别生成 `NodeRun`。所以：

- 持久化、重试、超时、事件和运行状态以 `stage-abc`、`stage-ef` 为单位；
- 任一子 Unit 失败会使所在的大节点失败；
- 同一阶段的输入和上游结果应视为只读数据；
- 并发数应结合连接池、下游限流和内存预算配置。

如果 A、B、C、E、F 都需要独立暂停、重试、审计或恢复，应把它们定义成五个 workflow
node，而不是放进组合 Unit。这个 Demo 选择组合 Unit，正是为了满足“运行记录只保留两个大节点”
的场景，并且不修改核心调度模型的兼容边界。
