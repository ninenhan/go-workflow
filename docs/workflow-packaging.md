# Workflow Packaging

这个能力用于把已经编辑、验证完成的 workflow 绑定成一个独立的 Go 可执行文件。

## 适用场景

- workflow 已经稳定，不再依赖 scheduler HTTP API
- 希望通过命令行参数直接运行
- 想把某个 workflow 固化为单独二进制交付

## 当前支持范围

`wfpack` 目前支持这几类 executor：

- `unit`
  - 仅限当前仓库内置并已自动注册的 unit
- `http`
- `script`

以下类型不会被自动打包：

- `local_go`
  - 需要手工注册 Go 函数
- `remote`
- `queue`
- `container`
- `python`
- `node`
- 自定义 unit

如果 workflow 使用了这些能力，`wfpack check` 会直接失败。

## 命令

先检查：

```bash
go run ./cmd/wfpack check -workflow ./examples/input-sources.workflow.json
```

直接构建：

```bash
go run ./cmd/wfpack build \
  -workflow ./examples/input-sources.workflow.json \
  -output ./dist/input-sources-demo
```

只导出绑定源码：

```bash
go run ./cmd/wfpack export \
  -workflow ./examples/input-sources.workflow.json \
  -dir ./cmd/input-sources-demo
```

## 运行打包后的二进制

生成后的二进制支持这些参数：

- `--request-json`
  - 注入完整 `request` 对象
- `--request-file`
  - 从文件读取完整 `request` 对象
- `--request-body-json`
  - 只注入 `request.body`
- `--request-body-file`
  - 从文件读取 `request.body`
- `--var key=value`
  - 注入字符串变量
- `--var-json key=<json>`
  - 注入 JSON 变量
- `--node-output <node_id>`
  - 只输出某个节点结果
- `--pretty`
  - 是否格式化 JSON 输出

示例：

```bash
./dist/input-sources-demo \
  --var tenant_id=t-001 \
  --request-body-json '{"message":"hello from binary"}' \
  --node-output summary
```

上面的命令会把：

- `tenant_id` 写入 `run.Context.Variables`
- `request.body.message` 写入 request scope

因此可以直接驱动依赖 `var/request/run` 输入绑定的 workflow。
