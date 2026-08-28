# Worker Runtime

`worker.Service` is the built-in data-plane runtime.

## Core capabilities

1. `Registry()`
   - executor registry owned by the worker

2. `UnitRegistry()`
   - standard unit registry used by `worker/unit.Executor`

3. `Handler()`
   - exposes `/v1/worker/execute`
   - exposes `/v1/worker/poll`
   - exposes `/v1/worker/cancel`

4. `MaintainPull(...)`
   - registers an outbound-only worker with `transport=pull`
   - long-polls scheduler commands without requiring a reachable worker endpoint
   - retries lost command completions without executing the same `dispatch_id` twice

5. `Descriptor(...)`
   - builds a `workerproto.WorkerDescriptor`
   - automatically includes registered executor types
   - automatically includes registered unit names

6. `MaintainRegistration(...)`
   - registers the worker with the scheduler
   - keeps sending heartbeats until the context is cancelled

7. `ConsumeQueue(...)`
   - pulls tasks from a queue broker
   - executes the target executor locally
   - acknowledges results back to the broker

## Typical standalone worker bootstrap

1. create `worker.Service`
2. expose `worker.Service.Handler()`
3. build a descriptor with `worker.Service.Descriptor(...)`
4. start `MaintainRegistration(...)` against the scheduler

For Electron, NAT, or other outbound-only deployments, omit the HTTP listener
and call `MaintainPull(...)` instead. The callback and pull modes use the same
executor registry and task/result fields.

The stable wire rules, schemas, errors, and idempotency behavior are documented
in [Worker Protocol v1](./worker-protocol-v1.md).
