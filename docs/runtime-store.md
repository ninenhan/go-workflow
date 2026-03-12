# Runtime Store

The runtime store is the persistence boundary for workflow execution state.

## Implementations

1. `core/runtime.MemoryStore`
   - in-memory
   - useful for tests and single-process demos

2. `core/runtime.GormStore`
   - durable store backed by GORM
   - persists:
     - workflow runs
     - run snapshots
     - run events

## Usage

Inject the store into `scheduler.Options`:

```go
db, _ := gorm.Open(sqlite.Open("workflow.db"), &gorm.Config{})
store, _ := wfruntime.NewGormStore(db)

svc, _ := scheduler.NewService(scheduler.Options{
    Store: store,
})
```

## Stored data model

`GormStore` keeps the execution model simple:

- `workflow_runs`
  - latest materialized run state
- `workflow_run_snapshots`
  - point-in-time execution snapshots
- `workflow_run_events`
  - append-only event log

Complex fields such as `node_runs`, `current_nodes`, `context`, and `payload`
are stored as JSON columns so the runtime model stays aligned with the in-memory
structures.
