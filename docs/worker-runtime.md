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

4. `Descriptor(...)`
   - builds a `workerproto.WorkerDescriptor`
   - automatically includes registered executor types
   - automatically includes registered unit names

5. `MaintainRegistration(...)`
   - registers the worker with the scheduler
   - keeps sending heartbeats until the context is cancelled

6. `ConsumeQueue(...)`
   - pulls tasks from a queue broker
   - executes the target executor locally
   - acknowledges results back to the broker

## Typical standalone worker bootstrap

1. create `worker.Service`
2. expose `worker.Service.Handler()`
3. build a descriptor with `worker.Service.Descriptor(...)`
4. start `MaintainRegistration(...)` against the scheduler
