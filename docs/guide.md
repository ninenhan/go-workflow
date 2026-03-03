# Workflow Composer Guide

## Overview
This is a composable workflow engine that supports:
1. Load workflow definitions from JSON
2. Run DAGs with concurrency, conditional edges, and join policies
3. Persist execution state
4. Stream events via SSE
5. Extend with new units/skills

Key files:
1. `composer_engine.go` runtime engine
2. `composer_definition.go` JSON definition and validation
3. `core_registry.go` unit registry
4. `server/http_server.go` HTTP/SSE API
5. `store/gorm_state.go` persistent state store
6. `schema/workflow.schema.json` JSON schema

## Quick Start
```go
engine := workflow.NewEngine()

// Choose a state store
mem := store.NewMemoryStateStore()
// Or
db, _ := gorm.Open(sqlite.Open("wf.db"), &gorm.Config{})
gormStore, _ := store.NewGormStateStore(db)

// HTTP entry
api := server.NewAPIServer(engine, mem)
http.ListenAndServe(":8080", api.Handler())
```

## Workflow JSON Definition
`edges` supports both array and object formats.

```json
{
  "id": "wf-demo",
  "start": ["input"],
  "nodes": {
    "input": {
      "unit": "HttpUnit",
      "input": { "data": { "url": "https://example.com" } }
    },
    "judge": {
      "unit": "ScriptUnit"
    },
    "final": {
      "unit": "LogUnit",
      "options": { "timeout": "2s", "retries": 2 }
    }
  },
  "edges": [
    { "from": "input", "to": "judge" },
    { "from": "judge", "to": "final", "when": "Result.Data == \"ok\"" }
  ]
}
```

## Key Fields
1. `nodes` is required, node IDs are keys
2. `unit` or `unit_id` is required per node
3. `input.data` can be any JSON
4. `input.slottable` supports template rendering via `{{node.field}}`
5. `options.join` supports `any` or `all`
6. `options.continue_on_error` allows failures to continue

## Conditional Edges
`when` uses expr expressions. Environment:
1. `State`: `map[string]*ExecutionResult`
2. `Result`: current node result
3. `Node`: current node ID

Examples:
```
Result.Data == "tools"
State["a"].Data != nil
```

## While / Break / Continue
Looping is provided via `WhileUnit`, which runs an embedded sub-graph as the loop body.

Example:
```json
{
  "nodes": {
    "loop": {
      "unit": "WhileUnit",
      "params": {
        "condition": "Iter < 3 && State[\"fetch\"].Data != nil",
        "max": 10,
        "body": {
          "start": ["fetch"],
          "nodes": {
            "fetch": { "unit": "HttpUnit" },
            "log": { "unit": "LogUnit" }
          },
          "edges": [
            { "from": "fetch", "to": "log" }
          ]
        }
      }
    }
  }
}
```

`condition` supports `Iter` and `State` (outputs from the previous iteration).  
The loop body also gets a synthetic `loop` seed node with `loop.iter` available for templates.

Control units inside the body:
1. `BreakUnit` -> exit the loop
2. `ContinueUnit` -> next iteration (always from body start)

## Output and Result
Node output is stored in `ExecutionResult`:
1. `Data` any structure
2. `Stream` indicates streaming
3. `Raw` raw response

## Event Stream (SSE)
Event types:
1. `run_started`
2. `run_finished`
3. `node_started`
4. `node_finished`
5. `node_failed`
6. `node_skipped`
7. `edge_error`

## HTTP API
```bash
# Start a run
curl -X POST http://localhost:8080/v1/runs \
  -H "Content-Type: application/json" \
  -d '{"definition":{...}}'

# Get state
curl http://localhost:8080/v1/runs/{run_id}

# Subscribe events
curl http://localhost:8080/v1/runs/{run_id}/events
```

## Register Units
Use factory registration:
```go
workflow.RegisterUnitFactory("MyUnit", func() workflow.ExecutableUnit {
	u := &MyUnit{}
	u.UnitName = u.GetUnitName()
	return u
})
```

## Tests
Existing cases:
1. Create wf -> run -> assert result
2. Load wf from JSON -> run -> assert result + state load

Run:
```bash
GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go test ./...
```

## Resume
You can resume a persisted run by `run_id`:

```go
store := store.NewMemoryStateStore()
engine := workflow.NewEngine()
engine.Store = store

// first run
_, _ = engine.Run(ctx, def, &workflow.RunOptions{RunID: "run-1", Store: store})

// resume
state, err := engine.Resume(ctx, def, "run-1", &workflow.RunOptions{Store: store})
```

Resume uses the stored node results to re-evaluate edges and only executes pending nodes.

## JSON Schema
File: `schema/workflow.schema.json`
Used for strict UI validation.
