# V0 hosts

V0 ships one workflow runtime through three explicit hosts. All hosts use
`runtimehost.Run`, the same scheduler, embedded worker, HTTP handlers, SQLite
stores, credential store, automation engine, and published API implementation.
Host mode changes process ownership and resource locations only.

## Mode contract

| Mode | Web UI | Listen policy | Runtime owner |
| --- | --- | --- | --- |
| `server` | required | configured by the deployment | container or server supervisor |
| `desktop` | required | explicit loopback IP only | Electron main process |
| `headless` | rejected | configured by the operator | terminal or service supervisor |

Modes are selected with `--mode` or `WORKFLOW_MODE`. The default is
`headless`; a mode never changes because a resource is missing or a port is
occupied. Invalid combinations reject startup.

Common options have equivalent environment variables:

```text
--config              WORKFLOW_CONFIG
--mode                WORKFLOW_MODE
--addr                WORKFLOW_ADDR
--data-dir            WORKFLOW_DATA_DIR
--web-dir             WORKFLOW_WEB_DIR
--embedded-worker     WORKFLOW_EMBEDDED_WORKER
--automations         WORKFLOW_AUTOMATIONS
--automation-period   WORKFLOW_AUTOMATION_PERIOD
```

Runtime settings use the deterministic precedence order built-in defaults <
`config.yml` < environment variables < explicit CLI flags. The implicit
`./config.yml` is optional; a path supplied by `--config` or `WORKFLOW_CONFIG`
must exist and be valid. Unknown YAML fields, unsupported versions, invalid
ports, and non-positive automation periods reject startup. Relative
`runtime.data_directory` paths resolve from the configuration file directory,
not the process working directory.

The shared configuration schema is:

```yaml
version: 1
runtime:
  listen_host: 127.0.0.1
  port: 55080
  data_directory: ./data
  embedded_worker: true
  automations: true
  automation_period: 1s
desktop:
  prevent_sleep: false
  launch_at_login: false
storage:
  database:
    driver: sqlite
    dsn: ""
    dsn_env: ""
redis:
  mode: disabled
  address: 127.0.0.1:6379
  username: ""
  password_env: ""
  database: 0
  tls: false
extensions: {}
```

`runtime` is consumed by server, desktop, and headless hosts. `desktop` remains
in the same file but is only acted on by Electron. Turning off the embedded
worker leaves the scheduler in remote-worker-only mode; it is not an in-memory
or no-op fallback. Turning off automations stops scheduled trigger polling while
manual and published API runs remain available.

The YAML file is user-maintained. Electron's two-column settings page edits and
preserves `storage.database`, `redis`, and namespaced `extensions` alongside the
basic runtime fields. DSN and Redis passwords should normally be referenced by
environment-variable name instead of stored directly. This V0 build supports
the `sqlite` database driver with no custom DSN and `redis.mode: disabled`.
`mysql`, `postgres`, local Redis, and remote Redis are schema-reserved adapter
choices: their settings remain intact, but this build rejects applying them with
an explicit capability error. It never silently falls back to SQLite or ignores
an enabled Redis configuration.

All modes expose `GET /healthz` and `GET /readyz` after the database,
credential store, scheduler, worker, and automation engine are ready.

## Headless CLI

Start the API, worker, automations, SQLite, and encrypted credentials without a
Web UI:

```bash
go run ./cmd/workflow-server \
  --mode=headless \
  --addr=127.0.0.1:55080 \
  --data-dir=.go-workflow-data
```

The release binary is also the headless CLI:

```bash
./bin/workflow-server --mode=headless --data-dir=/private/workflow-data
```

Or start from a configuration file:

```bash
./bin/workflow-server --mode=headless --config=/etc/go-workflow/config.yml
```

Published workflow routes and `/v1/*` use the same handlers as server and
desktop modes. Headless mode rejects `--web-dir` instead of silently serving a
different application.

## Docker server

Build the current architecture:

```bash
./scripts/build-v0-docker.sh
```

Override the target architecture and image name when required:

```bash
V0_DOCKER_ARCH=arm64 \
V0_DOCKER_TAG=registry.example.com/go-workflow:v0.0.1 \
./scripts/build-v0-docker.sh
```

Run with a persistent data volume:

```bash
docker run --rm \
  --publish 55080:55080 \
  --volume go-workflow-data:/var/lib/workflow \
  go-workflow-v0:0.0.1
```

The image runs as the unprivileged `workflow` user, includes CA certificates
and time-zone data for HTTP actions and automations, and serves the production
Web build and API from the same origin.

## Electron desktop

The desktop host lives in the Web repository. Prepare the production Web build
and native Go sidecar, then start Electron:

```bash
cd ../go-workflow-react
pnpm desktop:dev
```

Create an unpacked application or installers:

```bash
pnpm desktop:pack
pnpm desktop:dist
```

Electron creates `config.yml` in its platform-specific `userData` directory,
starts the same binary with `--mode=desktop --config=<that file>`, and waits for an authenticated
`/readyz` response before showing the window. The renderer has Node integration
disabled, context isolation and sandboxing enabled, and cannot start arbitrary
processes. A per-launch token protects readiness identity and the desktop-only
shutdown route. Electron requests graceful shutdown and waits for the Go runtime
to close its HTTP server, scheduler, and database before exiting.

Desktop mode prefers the port saved in Electron's platform-specific `userData`
directory, then checks `55080` followed by the bounded range `58080`–`58099`.
The selected port is persisted in the shared `config.yml`, keeping the Web
origin stable across normal launches. Electron only advances to another
candidate for a confirmed port conflict; other sidecar failures retain their
real diagnostic.

If every candidate is occupied, Electron remains open and shows a sandboxed
local settings window that does not depend on the Go server. The user can save
any available port from `1024` through `65535` and retry, or repeat the automatic
scan. The application never chooses an unbounded random port. An origin change
is therefore limited to an explicit user choice or a collision with the saved
port. The App settings page also edits the data directory, embedded worker,
automations, automation period, sleep prevention, and login-start behavior.
Runtime changes gracefully stop and restart the sidecar; changing the data
directory selects another runtime store and never silently migrates or merges
SQLite data. Desktop-only changes are applied without restarting the sidecar.

The settings page is available from the App toolbar, the application menu, or
`Cmd/Ctrl+,`. Its renderer remains sandboxed and can only call narrow IPC
operations for validated configuration, directory selection, and file reveal.
`prevent_sleep` uses Electron's application-suspension blocker: the display may
turn off, but the operating system does not sleep while the App is running. It
does not wake a computer that is already asleep. `launch_at_login` is applied by
packaged macOS and Windows Apps.

## Data ownership

Exactly one process owns a runtime data directory. Docker, desktop, and
headless deployments may exchange `.goworkflow` packages, but their databases
do not synchronize automatically. When a continuously running headless service
owns the database, a desktop UI must communicate with it over HTTP rather than
opening the SQLite file itself. RuntimeHost holds an operating-system file lock
for its complete lifetime; a second process receives the current owner PID and
rejects startup, while a crashed process releases the lock automatically.
