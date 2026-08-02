# Automations

Published workflows can run from structured time automations without exposing a
cron expression to end users. Only enabled `cron` triggers on the active
published version are scheduled.

## Trigger schema

Each trigger requires a unique `id`, `type: "cron"`, `enabled: true`, and one of
these strict configurations:

```json
{"frequency":"daily","time":"09:00","timezone":"Asia/Taipei"}
{"frequency":"weekdays","time":"09:00","timezone":"Asia/Taipei"}
{"frequency":"weekly","time":"09:00","weekdays":[1,3,5],"timezone":"Asia/Taipei"}
{"frequency":"interval","interval_minutes":15,"timezone":"Asia/Taipei"}
```

Weekly values use `1` through `7` for Monday through Sunday. Supported interval
values are `5`, `15`, `30`, `60`, `180`, `360`, and `720` minutes. Time zones
must be valid IANA names. Invalid or incomplete configurations fail validation
instead of falling back to another schedule.

Wall-clock schedules follow their selected time zone. A local time skipped by a
daylight-saving transition is omitted; it is not moved to a different hour.

## Lifecycle

`workflow-server` reconciles schedules from active published versions at
startup, after publication, and periodically. Draft edits do not affect running
automations until they are published. `GET /v1/automations` returns current
schedule state, including the next occurrence, latest run, and latest error.

The SQLite automation store persists schedule progress and uses leased claims.
Run IDs are deterministically derived from the schedule and occurrence time, so
recovering the same occurrence after a crash does not create a second run. A
triggered run receives this reserved input:

```json
{
  "_automation": {
    "trigger_id": "morning",
    "scheduled_at": "2026-07-25T01:00:00Z"
  }
}
```

Call `StartAutomations` when embedding `scheduler.Service`, and call
`Service.Shutdown` during process shutdown. `cmd/workflow-server` performs both.
