# Runtime Control And Loop Semantics

The runtime layer now supports:

1. `pause`
   - request a running workflow to stop scheduling further work

2. `resume`
   - continue a paused workflow from persisted run state

3. `cancel`
   - stop a workflow and mark the run as cancelled

4. node-level loop policy
   - declared on `definition.Node.Loop`
   - compiled into `planning.PlanNode.Loop`
   - executed by the scheduler without using a special unit type

## Loop policy

```go
type LoopPolicy struct {
    MaxIterations int
    Condition     string
}
```

The scheduler re-executes the same node while:

- `MaxIterations` allows another pass
- `Condition` evaluates to `true`

Loop expressions run against:

- `Run.variables`
- `Run.node_results`
- `Output`
- `Iteration`
- `NodeID`
- `WorkflowID`

## Current boundary

The runtime supports controlled node loops.

It now also supports explicit graph back-edges:

- declare `definition.Edge.Kind = "back"`
- the compiler excludes back-edges from DAG topo sorting
- the scheduler evaluates back-edge conditions after the source node succeeds
- if a back-edge is activated, the runtime resets only the loop scope between
  the back-edge target and source

The planner still expects the normal edges to form a DAG. Arbitrary cyclic
graphs are not enabled; only explicit back-edges are allowed so execution stays
deterministic and resumable.
