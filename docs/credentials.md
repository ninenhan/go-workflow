# Runtime credentials

Workflow definitions reference credentials by environment-style names such as
`OPENAI_API_KEY`. Secret values are resolved by the worker at execution time and
are never added to workflow definitions, execution tasks, node results, run
variables, or credential API responses.

## Control-plane API

Credential requests use `X-Workflow-Credential-Scope`. A server configured with
`DefaultCredentialScope` accepts only that scope; public workflow routes always
use the server-bound scope and never trust a caller-provided override.

- `GET /v1/credentials` lists names and timestamps only.
- `PUT /v1/credentials` atomically creates or rotates multiple values using
  `{"credentials":{"NAME":"value"}}`.
- `PUT /v1/credentials/{name}` creates or rotates one value.
- `DELETE /v1/credentials/{name}` removes one value.

Names, scopes, batch sizes, values, JSON fields, trailing documents, and request
body sizes are validated before the store is changed.

## Worker resolution

`credential.Store` is also a `credential.Resolver`. The embedded worker receives
the configured resolver directly and resolves a name only inside the current
execution context. It does not mutate process-wide environment variables.

Standalone workers use `credential.EnvironmentResolver` unless another resolver
is configured. Remote deployments can provide a KMS, Vault, or platform secret
manager implementation without changing workflow definitions or Unit code.

`workflow-server` uses the AES-256-GCM `FileStore`. By default it writes an
encrypted payload and a separate 256-bit key below
`.go-workflow-data/credentials/`; set `WORKFLOW_DATA_DIR` to move the runtime
data root. The directory is restricted to mode `0700`, while both files use
`0600`. Writes are authenticated and atomically replaced, and the server refuses
to start when a key, payload, permission, or format check fails.

The local key protects secrets at rest but lives on the same host as the
ciphertext. Production deployments that require stronger key isolation should
provide a KMS, Vault, or platform secret-manager `credential.Store`.
