# Workflow Engine Refactor

## Layers

1. `core/definition`:
   - `Workflow`, `WorkflowVersion`, `WorkflowDefinition`, `Node`, `Edge`, `Trigger`, `PublishConfig`
   - static model only
2. `core/planning`:
   - compile `WorkflowVersion` -> `ExecutionPlan`
   - produce entry nodes, adjacency/dependency map, topological order, branch metadata, node policy snapshot
3. `core/runtime` (package `wfruntime`):
   - `WorkflowRun`, `NodeRun`, `RunContext`, `RunSnapshot`, `RunEvent`
   - run state machine and run persistence abstractions
4. `core/executor`:
   - unified `Executor` interface and registry
   - includes HTTP / LocalGo and stubs for Python/Node/Remote/Container
5. `core/runner`:
   - `Dispatcher` and `Scheduler`
   - scheduler only depends on `planning`, `runtime`, `executor`
6. `worker/unit`:
   - worker-side unit SDK
   - standard unit registry and `executor.TypeUnit` bridge

## Final runtime split

1. `scheduler`
   - orchestration entrypoint
   - compiles definitions, runs scheduler, manages worker discovery
2. `worker`
   - embedded or standalone data plane
   - owns built-in executors and unit execution
3. `units`
   - built-in standard units, registered into `worker/unit.DefaultRegistry`

## Notes

- The old root-package workflow runtime has been removed.
- `WhileUnit` was removed with the legacy runtime because its nested subworkflow semantics depended on the old engine.
- New work should target `core`, `scheduler`, `worker`, and `worker/unit` only.
