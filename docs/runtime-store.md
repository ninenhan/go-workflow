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

`workflow.OpenDefault` uses `persist/defaultstore` and `persist/localdb` to place
`GormStore` and the GORM definition repository in one SQLite database at
`var/go-workflow/workflow.db`. Applications that need another location call
`workflow.OpenDefaultWithOptions`.
The database and directory use `0600` and `0700` permissions respectively.
Startup enables WAL, full synchronization, foreign keys, a bounded busy
timeout, and an integrity check. Invalid permissions, corruption, migration
failure, or unavailable storage stops startup instead of falling back to
process memory. Graceful shutdown stops accepting new background runs, cancels
active runs, waits for their terminal state to persist, and only then closes
the database. If a process exits without that path, the next startup
transaction marks interrupted runs as failed exactly once and appends a
terminal event.

## Usage

Applications normally select a complete storage bundle through the facade:

```go
app, err := workflow.OpenDefault(ctx)
```

For a caller-owned Gorm connection, use the independent adapter:

```go
stores, err := gormstore.New(ctx, db, gormstore.Options{
    Credentials: credentials,
    Recovery:    gormstore.RecoveryDisabled,
})
app, err := workflow.New(ctx, workflow.Options{Stores: stores})
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
