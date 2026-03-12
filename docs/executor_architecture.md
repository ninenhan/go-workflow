# Unified Executor Architecture

## Key contracts

- `executor.ExecuteTask`: scheduler -> executor input envelope
- `executor.ExecuteResult`: executor -> scheduler normalized result
- `executor.Executor`: sync execution contract
- `executor.AsyncExecutor`: optional poll/cancel contract for queue/remote worker modes
- `executor.Dispatcher`: routes `ExecuteTask` to concrete executor
- `runner.NodeDispatcher`: picks runnable nodes from dependency graph
- `runner.ResultReporter`: reports normalized execution results
- `runner.HeartbeatReporter`: reports async execution heartbeats

## Responsibility split

- Scheduler (`runner.DefaultScheduler`)
  - dependency-driven node scheduling
  - state transitions and retries
  - timeout/backoff orchestration
  - async polling lifecycle
- Executors (`executor/*`)
  - protocol/runtime specific execution (http/script/local/remote/container)
  - convert backend response to normalized `ExecuteResult`
  - optional remote poll/cancel behavior
- Standard units (`worker/unit.Executor`)
  - load unit implementations from the worker-side unit registry
  - hydrate unit config from `ExecuteTask.Params`
  - keep scheduler isolated from unit implementation code

## Extension points

- Queue/worker mode:
  1. executor returns `StatusAccepted` + `ExternalTaskID`
  2. scheduler calls `AsyncExecutor.Poll`
  3. heartbeat emitted via `HeartbeatReporter`
- Remote worker:
  - implement `RemoteExecutor` with real submit/poll/cancel transport
- Container job:
  - implement `ContainerExecutor` with K8s/job API integration
