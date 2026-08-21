# V0 Desktop Web

V0 uses one production process for the PC Web interface, workflow API, embedded
worker, encrypted credentials, and SQLite runtime state. Vite remains a
development-only server.

## Requirements

- Go 1.24.6 or newer in the Go 1.24 line
- Node.js 18 or newer
- pnpm 10.28.0
- Playwright's bundled Chromium, installed by the locked Web dependencies
- `go-workflow` and `go-workflow-react` checked out as sibling directories

If the Web repository is elsewhere, set `WORKFLOW_WEB_SOURCE_DIR` to its
absolute path.

## Start

From `go-workflow`:

```bash
./scripts/run-v0.sh
```

The command performs a frozen-lockfile install, builds the production Web
assets, verifies the build output, and starts `workflow-server`. Open
`http://127.0.0.1:55080/`.

The same origin serves:

- `GET /` for the Web entrypoint
- `GET /assets/*` for immutable fingerprinted assets
- `/v1/*` for the workflow control API
- user-defined published workflow routes

Static routing is intentionally narrow. A missing Web build rejects startup,
and unknown or published routes are never converted into an HTML fallback.

## Runtime data

The default runtime directory is `.go-workflow-data` and contains:

- `workflow.db` for definitions, runs, events, snapshots, and automations
- the authoritative editable workspace, including drafts, folders, layouts, and services
- `credentials/` for the encrypted credential store

The workspace and executable schema ownership rules, revision conflicts, and
one-time browser migration are documented in
[`workspace-contract.md`](workspace-contract.md).

Use `WORKFLOW_DATA_DIR` to select another private data directory and
`WORKFLOW_ADDR` to change the listen address. The server rejects an insecure
data-directory permission mode rather than falling back to memory or plaintext.

## Release gate

Run both gates while developing:

```bash
go test ./...
```

```bash
cd ../go-workflow-react
pnpm check:v0
```

The Web gate includes type checking, a production build, protocol audits, six
language checks, and PC browser E2E. Mobile layout is outside the V0 scope.
The release scripts run both gates again and refuse to produce an archive when
either one fails.

## Build a self-contained package

Build the current host package:

```bash
./scripts/build-v0-release.sh
```

Build the standard desktop set:

```bash
./scripts/build-v0-desktop-releases.sh
```

The standard set contains `darwin/arm64`, `windows/amd64`, and `linux/amd64`.
`build-v0-release.sh` also accepts explicit `os/arch` arguments or a
space-separated `V0_TARGETS` value. The production SQLite runtime is pure Go,
so every target is built with `CGO_ENABLED=0`. Existing SQLite data remains
compatible with the previous CGO-backed runtime.

A successful build creates the following under `dist/v0` for every target:

- an unpacked `go-workflow-v0-<version>-<os>-<arch>` directory
- the corresponding `.tar.gz` archive
- an archive `.sha256` file

The package includes a native server binary, production Web assets, a runtime
launcher, release metadata, the license, and checksums for every shipped file.
The packaged `run.sh` or `run.cmd` does not require Go, Node.js, or pnpm.

Every archive receives path-safety, binary format and architecture, Web asset,
release metadata, and complete SHA-256 coverage checks. A package matching the
build host additionally runs native acceptance: an isolated Playwright Chromium
session creates a shortcut through the packaged Web interface, configures actions,
executes it, and verifies browser persistence after reload. It never launches or
depends on an installed system Chrome. The gate also submits a direct
API run, requires exactly one `run_started` event, checks private data storage,
restarts the packaged server, and confirms that the successful run remains
persisted. Cross-built archives are marked `static` in `RELEASE.json`; they are
not represented as target-native tested.

## Failure diagnosis

- `required command is unavailable`: install the named Go or pnpm command.
- `workflow web source is invalid`: set `WORKFLOW_WEB_SOURCE_DIR` to the Web
  repository.
- `ERR_PNPM_OUTDATED_LOCKFILE`: update and commit the lockfile together with
  `package.json`; do not bypass the frozen install.
- `workflow web production build is incomplete`: fix the Web build error; do
  not point the server at an older `dist`.
- `configure workflow web application`: verify `WORKFLOW_WEB_DIR` contains the
  current `index.html` and `assets` directory.
- `open workflow database` or `open encrypted credential store`: fix the
  reported path, permission, integrity, or key error before retrying.
