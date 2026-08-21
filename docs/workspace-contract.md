# Server-owned workspace and workflow contract

V0 keeps one authoritative editable workspace in the runtime database. The
workspace contains shortcut drafts, folders, editor layouts, and reusable
service presentation data. `GET /v1/workspace` loads it and `PUT /v1/workspace`
updates it with compare-and-swap `revision` semantics. A stale revision returns
HTTP `409`; the server never overwrites a newer workspace implicitly.

Executable data inside every workspace entry is decoded as the Go
`definition.WorkflowDefinition`. Layout and service presentation extensions are
isolated from that executable definition and never enter the compiler directly.
The server exposes the versioned structural contract at
`GET /v1/schema/workflow-definition`; semantic validation remains authoritative
in the server compiler.

The Web repository generates its server wire types from this Go-owned schema.
`pnpm audit:contract` fails when the generated TypeScript contract is stale.
The editor may keep a more permissive in-progress model so an incomplete draft
can be displayed, but validation, runs, version creation, and publication cross
the generated wire contract and are strictly decoded again by the server.

Browser LocalStorage is not authoritative. It is retained only as a verified
mirror after a successful server save and as an input to the one-time V0
migration. When a server has no workspace and the browser contains an existing
V0 workspace, editing remains locked until the user explicitly imports it. A
server load, validation, revision conflict, or save failure is surfaced and
never falls back to browser-only persistence.
