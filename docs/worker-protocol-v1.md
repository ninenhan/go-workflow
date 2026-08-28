# Worker Protocol v1

Worker Protocol v1 is the language-neutral boundary between the scheduler and
standalone workers. Callback and pull transports carry the same
`ExecuteTask`/`ExecuteResult` fields; transports do not define separate domain
models.

## Wire encoding

- JSON is encoded as UTF-8.
- Duration fields (`timeout`, `poll_interval`, `heartbeat_freq`, and
  `retry_after`) are JSON integers in nanoseconds. This preserves the existing
  Go v0 encoding.
- Timestamp fields are RFC3339Nano strings. Producers should emit UTC; consumers
  must accept any valid RFC3339 offset.
- `input`, `output`, `params`, `context`, and `metadata` are ordinary JSON values
  and are passed through without field renaming or normalization.
- Readers must ignore unknown fields. Writers must not reuse an existing field
  with a different meaning.

Every worker-protocol request sends `X-Workflow-Protocol-Version: 1`. Versionless
requests remain accepted as a v0 compatibility bridge. An explicitly unsupported
version receives HTTP 426 with `code=unsupported_protocol_version`.

A callback worker registered without `protocol_version` remains versionless in
the scheduler registry. It is treated as a legacy worker, so callback transport
failures are returned without automatically replaying operations. Only an
`Execute` sent to a worker that explicitly advertises `protocol_version=1` and
has a non-empty `dispatch_id` may be replayed after a transport failure. `Poll`
and `Cancel` are not replayed automatically.
Because pull delivery depends on v1 command and completion idempotency, a pull
worker must explicitly advertise `protocol_version=1`.

## Error boundary

HTTP non-2xx responses are protocol or transport failures and use:

```json
{"error":"executor not found","code":"executor_not_found"}
```

Executor outcomes always use HTTP 200 and `ExecuteResult`:

- `failed`: permanent execution failure
- `retryable`: scheduler may retry the node
- `accepted` / `running`: asynchronous execution is not terminal
- `succeeded`: terminal success

This prevents an SDK from guessing retry behavior from HTTP status or error text.

## Transports

`callback` is the compatibility default. The worker exposes `/execute`, `/poll`,
and `/cancel`, and the scheduler calls its `endpoint`.

`pull` is outbound-only. The worker registers with `transport=pull`, long-polls
`/v1/workers/pull`, executes the leased command, and posts the outcome to
`/v1/workers/complete`. Pulling leases rather than removes a command. If the HTTP
response is lost, the same `command_id` is delivered again after the lease.

`command_id` identifies a transport delivery. `dispatch_id` identifies one
logical node execution attempt. During a worker process lifetime, completed and
concurrent executions are deduplicated by `dispatch_id`, so callback retries and
pull redelivery do not invoke user code twice. Completion submission is
independently idempotent by `command_id`. A handler that performs external side
effects should also pass `dispatch_id` to the downstream system as its
idempotency key; no in-memory worker cache can provide exactly-once effects
across a process crash.

Go Units read this runtime identity from their existing execution context:

```go
dispatchID := workerunit.DispatchID(ctx)
```

The adapter does not add `dispatch_id` to workflow variables, Unit params, node
input, or output. A Unit that requires crash-safe external effects should reject
an empty value rather than manufacture another identity.

The canonical schema is `schema/worker-protocol.schema.json`; the HTTP operation
surface is `schema/worker-protocol.openapi.json`. Language SDKs should be tested
against the fixtures under `core/workerproto/testdata`.
