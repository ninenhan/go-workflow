# Concurrency Groups and Resource Pools

Task execution has three independent limits. The effective permission to start
a ready Task is the intersection of all configured limits:

| Control | Scope | Configuration |
| --- | --- | --- |
| `max_concurrency` | one Workflow Run | workflow definition |
| `concurrency_group` | matching Task Nodes inside one Run | Task Node |
| `resource_pool` | matching Task Nodes across Runs using one Scheduler | Task Node |

Gateway does not acquire a concurrency-group or resource-pool slot. It is
removed by the compiler before scheduling and remains responsible only for fork
and join control flow.

```json
{
  "id": "browser-workflow",
  "name": "Browser workflow",
  "max_concurrency": 8,
  "nodes": [
    {
      "id": "scrape-a",
      "name": "Scrape A",
      "type": "task",
      "executor": { "type": "unit", "ref": "BrowserUnit" },
      "concurrency_group": "browser-tasks",
      "concurrency_limit": 2,
      "resource_pool": "chromium",
      "resource_capacity": 3
    },
    {
      "id": "scrape-b",
      "name": "Scrape B",
      "type": "task",
      "executor": { "type": "unit", "ref": "BrowserUnit" },
      "concurrency_group": "browser-tasks",
      "concurrency_limit": 2,
      "resource_pool": "chromium",
      "resource_capacity": 3
    }
  ]
}
```

In this example a single Run may execute at most eight Tasks overall, at most
two `browser-tasks`, and all concurrent Runs sharing the Scheduler may hold at
most three `chromium` slots in total.

## Rules

- `concurrency_group` and `concurrency_limit` must appear together.
- `resource_pool` and `resource_capacity` must appear together.
- Limits and capacities are integers from 1 through 1024.
- Active Task Nodes with the same group must declare the same limit.
- Active Task Nodes with the same pool must declare the same capacity.
- A resource slot is released after success, failure, retry, timeout, or
  cancellation. A recovered process starts with no in-memory leases, so stale
  slots cannot survive a process restart.
- Concurrent workflow versions cannot use different capacities for the same
  active resource-pool name. Use distinct names during a capacity transition or
  wait for existing holders to finish.

Definitions that omit these four Node fields retain their existing scheduling
behavior and require no migration.
