# go-workflow V0

This package runs the PC Web interface, workflow API, embedded worker, encrypted
credentials, and SQLite state from one native process.

## Run

```bash
./run.sh
```

On Windows:

```bat
run.cmd
```

Open `http://127.0.0.1:55080/`.

The default data directory is:

- macOS: `~/Library/Application Support/go-workflow`
- Linux: `${XDG_DATA_HOME:-~/.local/share}/go-workflow`
- Windows: `%LOCALAPPDATA%\go-workflow`

Set `WORKFLOW_DATA_DIR` or `WORKFLOW_ADDR` before the launcher to override
either value. Unix packages enforce private directory and database modes.
Windows packages use the current user's inherited LocalAppData ACL.

`RELEASE.json` identifies the version, platform, source revision, dirty source
state, build time, and whether release verification was native or static.
`SHA256SUMS` covers every shipped file except itself. `RUNTIME_UNITS.json` is
the deterministic list of units registered by the packaged runtime and is
compared with the binary during target-native verification.

This V0 package targets desktop browsers. Mobile layout is intentionally outside
its release scope.
