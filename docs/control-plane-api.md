# Scheduler Control Plane API

The scheduler now exposes a first-class HTTP control plane through `scheduler.Service.Handler()`.

## Endpoints

1. `POST /v1/runs`
   - execute a workflow definition or workflow version
   - request body accepts:
     - `definition`
     - or `version`
     - or `version_id`
     - optional `run`

2. `GET /v1/runs`
   - list all known workflow runs

3. `GET /v1/runs/{run_id}`
   - load a single run

4. `GET /v1/runs/{run_id}/events`
   - list run events

5. `GET /v1/runs/{run_id}/stream`
   - stream run events and the terminal result as Server-Sent Events
   - supports `Last-Event-ID` for reconnecting without replaying acknowledged run events

6. `GET /v1/runs/{run_id}/snapshots`
   - list saved run snapshots

7. `POST /v1/runs/{run_id}/pause`
   - request runtime pause

8. `POST /v1/runs/{run_id}/resume`
   - resume a paused run

9. `POST /v1/runs/{run_id}/cancel`
   - request runtime cancellation

10. `GET /v1/workflows`
   - list workflows

11. `POST /v1/workflows`
   - create or update workflow metadata

12. `GET /v1/workflows/{workflow_id}`
   - load workflow metadata

13. `GET /v1/workflows/{workflow_id}/versions`
   - list versions for a workflow

14. `POST /v1/workflows/{workflow_id}/versions`
   - create or update a workflow version

15. `GET /v1/workflows/{workflow_id}/contract`
   - load the active published API contract and its synchronous, asynchronous, and SSE URLs

16. `POST /v1/workflows/{workflow_id}/invoke`
   - invoke the active published version and return its final response
   - add `?wait=false` to return `202 Accepted` with the run and stream URLs immediately

17. `GET /v1/workflow-versions/{version_id}`
   - load a workflow version

18. `POST /v1/workflow-versions/{version_id}/publish`
   - publish a version and archive the previous active version

19. `POST /v1/workflow-versions/{version_id}/runs`
   - execute a stored workflow version

20. `GET /v1/workers`
   - list registered workers

21. `POST /v1/workers/register`
   - register a worker

22. `POST /v1/workers/heartbeat`
   - update worker heartbeat

## Published invocation

The published control-plane invocation supports two execution modes with the same input validation and active version:

- synchronous HTTP waits for completion and returns the configured `result` or `run` response
- asynchronous HTTP uses `?wait=false`, returns `202 Accepted`, and includes `run_url`, `events_url`, and `stream_url`

The SSE stream emits `ready`, zero or more `run_event` messages, and exactly one terminal `result` or `error` message before closing. Published runs use the configured response mode for the terminal payload. The terminal event has the stable ID `result`; reconnecting with that `Last-Event-ID` returns `204 No Content` and stops automatic reconnection.

## Notes

- The scheduler does not own execution code.
- Unit execution is resolved in the worker through `worker/unit.Executor`.
- The same HTTP handler works for both:
  - scheduler-only deployments
  - scheduler + embedded worker deployments

## Worker routing hints

When creating a node task, the scheduler can now route remote execution using task params:

- `worker_id`
  - force dispatch to a specific worker instance
- `tenant`
  - require the worker to belong to a tenant
- `worker_labels`
  - require worker labels to match, for example:
  - `{"region":"cn","tier":"gpu"}`
- `preferred_worker_labels`
  - soft preference used during worker ranking
  - example:
  - `{"tier":"gpu"}`

The in-memory worker registry also enforces:

- `max_concurrent`
  - remote dispatch acquires a worker slot before execution
  - the slot is released when the task finishes, fails, retries, or is cancelled
- `weight`
  - higher weight wins when hard constraints are the same
- `heartbeat ttl`
  - workers without heartbeats are treated as offline during scheduling and listing

## Queue mode

The runtime now supports queue-based async dispatch:

- node executor type: `queue`
- queue name: `executor.ref` or `params.queue`
- target executor type: `params.target_executor_type`
- target executor ref: `params.target_executor_ref`

This lets the scheduler enqueue work while workers consume and execute the
actual target executor asynchronously.
