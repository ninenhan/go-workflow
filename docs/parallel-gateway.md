# Parallel Gateway

`parallel_gateway` follows the BPMN parallel-gateway token rule: it waits for
every incoming path and then activates every outgoing path. The definition may
use it as a fork, a join, or a combined join+fork.

Gateway is a control-flow node. Its JSON contains `id`, `name`,
`type: "parallel_gateway"`, and optional editor `ui` data. It cannot contain an
`executor`, input, params, dependencies, retry, loop, timeout, branch policy, or
disabled/business state. Task nodes keep the existing shape and each task still
resolves exactly one leaf executor/Unit.

```json
{
  "id": "five-join-three",
  "name": "Five then three",
  "max_concurrency": 5,
  "fail_fast": true,
  "entry_nodes": ["fork"],
  "nodes": [
    { "id": "fork", "name": "Fork", "type": "parallel_gateway" },
    { "id": "a1", "name": "A1", "type": "task", "executor": { "type": "unit", "ref": "LogUnit" } },
    { "id": "a2", "name": "A2", "type": "task", "executor": { "type": "unit", "ref": "LogUnit" } },
    { "id": "a3", "name": "A3", "type": "task", "executor": { "type": "unit", "ref": "LogUnit" } },
    { "id": "a4", "name": "A4", "type": "task", "executor": { "type": "unit", "ref": "LogUnit" } },
    { "id": "a5", "name": "A5", "type": "task", "executor": { "type": "unit", "ref": "LogUnit" } },
    { "id": "join_fork", "name": "Join and fork", "type": "parallel_gateway" },
    { "id": "b1", "name": "B1", "type": "task", "executor": { "type": "unit", "ref": "LogUnit" } },
    { "id": "b2", "name": "B2", "type": "task", "executor": { "type": "unit", "ref": "LogUnit" } },
    { "id": "b3", "name": "B3", "type": "task", "executor": { "type": "unit", "ref": "LogUnit" } }
  ],
  "edges": [
    { "from": "fork", "to": "a1" }, { "from": "fork", "to": "a2" },
    { "from": "fork", "to": "a3" }, { "from": "fork", "to": "a4" },
    { "from": "fork", "to": "a5" },
    { "from": "a1", "to": "join_fork" }, { "from": "a2", "to": "join_fork" },
    { "from": "a3", "to": "join_fork" }, { "from": "a4", "to": "join_fork" },
    { "from": "a5", "to": "join_fork" },
    { "from": "join_fork", "to": "b1" }, { "from": "join_fork", "to": "b2" },
    { "from": "join_fork", "to": "b3" }
  ]
}
```

The compiler removes both gateways and gives each `b*` task direct dependencies
on all five `a*` tasks. Consequently gateways never create `NodeRun`, SSE node
events, or business steps.

`max_concurrency` is a workflow-level hard limit from 1 to 1024. Omitted or zero
keeps historical single-task scheduling. `fail_fast: true` cancels remaining
ready and dependent tasks after a terminal task failure; false preserves the
historical behavior of finishing independent work before reporting a blocked
dependency.

Interrupted `running`/`retry` task attempts are reset to `pending` when the same
persisted run is explicitly resumed. Recovery is at-least-once, so side-effecting
executors should use the stable run ID and node ID as their idempotency key.

## Compatibility tips

- Existing definitions need no migration: missing node `type`,
  `max_concurrency`, and `fail_fast` retain V0 behavior.
- A Gateway edge must be a normal, unconditional edge. Back edges, conditions,
  and priorities are rejected instead of being silently ignored.
- `max_concurrency > 1` is currently rejected when combined with node loops,
  loop groups, or back edges because those constructs intentionally mutate a
  shared iteration scope. They continue to work unchanged at historical
  single-task concurrency.
- Go consumers still construct `Node.Executor` with the same value type. Its
  JSON tag now uses Go 1.24 `omitzero`, allowing Gateway JSON to omit the field
  without changing the public Go field type.

Semantics references: [OMG BPMN 2.0.2, section 13.4.1](https://www.omg.org/spec/BPMN/2.0.2/PDF),
[Camunda Parallel Gateway](https://docs.camunda.io/docs/next/components/modeler/bpmn/parallel-gateways/).
