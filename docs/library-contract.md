# Library integration contract

This document defines the dependency and compatibility boundaries for applications
that embed `go-workflow`.

## Package boundaries

- `core/definition`, `core/planning`, `core/runtime`, and `core/executor` contain
  workflow contracts. Definitions and executor task/result shapes must not depend
  on scheduler HTTP handlers or a concrete application host.
- `worker/unit` is the SDK for business-level execution units. It does not import
  the built-in unit catalog.
- `units` owns the optional standard unit catalog. Importing it has no registration
  side effects.
- `worker` composes executor backends and an application-owned unit registry.
- `scheduler` composes planning, runtime state, dispatch, and an optional worker.

## Registration ownership

Every worker service owns its effective unit registry. When no registry is supplied,
`worker.NewService` clones legacy registrations from `worker/unit.DefaultRegistry`
once. Later mutations are isolated to that service.

New applications should register against the service instance:

```go
if err := workerSvc.UnitRegistry().RegisterUnitFactory("MyUnit", factory); err != nil {
    return err
}
```

The package-level `worker/unit.Register*`, `Find`, and `DefaultRegistry` APIs remain
available for compatibility, but are deprecated. They are not used as shared live
state by newly created services.

Registries reject duplicate names and executor types. This prevents import order or
startup order from silently changing behavior. Applications that intentionally
support hot replacement must call `RegisterOrReplace` explicitly.

## Built-in units

`worker.Options.RegisterBuiltins` preserves the existing standard worker behavior.
It atomically installs the catalog returned by `units.BuiltinRegistrations` into the
worker's registry. Tooling that only needs discovery should use `units.BuiltinNames`
instead of inspecting global runtime state.

Applications that compose a specialized worker can select catalog entries and call
`Registry.RegisterAll` once. Batch registration validates the entire set before
changing the registry.

## Compatibility policy

- Existing workflow JSON fields and executor task/result fields are evolved
  additively. Removal or semantic reuse requires a major module version.
- New execution capabilities use new executor types or optional interfaces such as
  `AsyncExecutor`; they do not widen the base `Executor` interface.
- Deprecated global unit APIs remain as a migration bridge. New code should use
  instance-owned registries.
- Registration conflicts are errors. Explicit replacement is a separate operation
  so extensions cannot accidentally shadow built-ins.
