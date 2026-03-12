# Three-Part Architecture

This repository now has three explicit architecture boundaries:

1. `core`
   - shared contracts and data structures
   - intended to be imported by scheduler and standalone worker projects
   - re-exports types from `definition`, `planning`, `runtime`, and `executor`

2. `scheduler`
   - orchestration service
   - owns compilation, scheduling, retries, runtime state, and storage
   - can run with or without an embedded worker

3. `worker`
   - embedded worker runtime
   - optional in this repository
   - can be disabled for deployments that only keep the scheduler
   - can be extracted into a standalone worker project later
   - exposes `worker/unit` as the shared unit SDK for standalone worker projects

The split is now authoritative:

- `core` is the stable import surface
- `scheduler.Service` is the control-plane entrypoint
- `worker.Service` is the data-plane entrypoint
- `worker/unit` is the standard unit SDK and registry
- `units` provides built-in units on top of `worker/unit`

Recommended deployment modes:

1. Single binary:
   - `scheduler.Service{EnableEmbeddedWorker: true}`
2. Control plane only:
   - `scheduler.Service{EnableEmbeddedWorker: false}`
3. Standalone worker project:
   - import `go-workflow/core`
   - build worker process around `worker.Service`-like registration and task handler

## Remote execution path

The repository now includes the shared protocol and basic control-plane/data-plane flow:

1. `core/workerproto`
   - shared scheduler/worker HTTP protocol types
2. `scheduler`
   - `WorkerRegistry` for worker discovery
   - `HybridDispatcher` for local/remote dispatch
   - `RemoteHTTPClient` for task submit/poll/cancel
   - `RegistryHTTPHandler` for worker register/heartbeat
3. `worker`
   - `Handler()` for `/execute`, `/poll`, `/cancel`
   - `RegistrationClient` for register/heartbeat against the scheduler

This means the architecture supports:

- scheduler only deployment
- embedded worker deployment
- standalone worker deployment
- mixed mode where scheduler prefers local or remote executors based on dispatch mode
