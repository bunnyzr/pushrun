# Deploying pushrun

pushrun is a single static binary with two runtime dependencies: `git` and
`bash` (both checked at startup, with a clear error if missing). No
database, no other services.

## Installing the server

Two official paths:

1. **Go toolchain**: `go install github.com/bunnyzr/pushrun/cmd/pushrun@latest`
2. **Static binaries** from
   [GitHub Releases](https://github.com/bunnyzr/pushrun/releases) for
   machines without a Go toolchain.

Everything static — the web console, built-in provider scripts,
client scripts — is embedded in the binary, so both paths produce a fully
self-contained server.

## Running the daemon

```sh
pushrun serve                  # start in the foreground
pushrun serve --dev            # foreground with human-readable stderr logs
pushrun serve --root /srv/pushrun
pushrun token show             # print the auth token
pushrun version
```

`pushrun serve --dev` (or `PUSHRUN_DEV=1`) switches stderr logging from JSON
to human-readable text — handy while developing. The `logs/daemon.log` file
stays JSON either way.

Zero-config start works out of the box. The data root is resolved in order:

1. `--root DIR` flag,
2. `$PUSHRUN_ROOT`,
3. `$XDG_DATA_HOME/pushrun`,
4. `~/.local/share/pushrun`.

On first boot the daemon writes a default `config.yaml`, generates the auth
token and hook secret (both 0600), and listens on `0.0.0.0:8000`. The
generated token is printed to the console exactly once; recover it later
with `pushrun token show` on the same host.

For production, run `pushrun serve` under your process supervisor of choice
(systemd, launchd, supervisord, ...). The daemon shuts down cleanly on
SIGINT/SIGTERM, re-adopts surviving supervised processes from state on the
next boot, and reaps dead ones.

## Configuration

`<root>/config.yaml` (created with defaults on first boot; restart to apply
changes):

```yaml
http:
  bind: 0.0.0.0     # listen address
  port: 8000        # the single HTTP port (git + API + SSE + web console)
port_pool:
  from: 20000       # instance ports are leased from [from, to]
  to: 21000
auth:
  enabled: true     # never off by default; see below
  token: ""         # optional override for the generated <root>/token
log:
  level: info       # debug | info | warn | error
git:
  scheme: https     # expands host/path repo shorthands into source URLs
```

`git.scheme` only applies to the one-time seed fetch of a repo nobody has
pushed through pushrun yet: a git mount's `repo: host/path` shorthand becomes
`<scheme>://host/path` (full URLs and local absolute paths in `repo` are used
verbatim). Credentials for private remotes are the deployer's business —
configure SSH keys or a git credential helper for the user the daemon runs
as on the host, or put a full URL with embedded credentials in the project
definition.

## Authentication

pushrun executes trusted local commands, so **auth is on by default** even
on a trusted network.

- One token authenticates everything: REST API and SSE
  (`Authorization: Bearer <token>`), the client scripts, and git pushes
  (HTTP Basic with the token as password, or a Bearer header).
- The token is generated on first boot, stored 0600 at `<root>/token`, and
  can be changed by editing `config.yaml` (`auth.token`) and restarting.
- `auth.enabled: false` is supported for deployments behind an
  authenticating reverse proxy — only there.
- The hook-internal endpoint (`/internal/v1/hooks/*`) is separate: loopback
  peers only, guarded by the persistent hook secret at
  `<root>/hooks-secret` (0600). Restarting the daemon never invalidates
  installed hooks.
- Multi-person use is a social convention over explicitly named instances
  (conventionally branch names), not a permission system: everyone who holds
  the token is trusted. See [design.md](design.md#explicitly-deferred) —
  per-user identity and ACLs are deliberately out of scope.

## Ports

Two distinct port spaces:

- **The daemon port** (`http.port`, default 8000): git push endpoint, REST
  API, SSE streams, and the web console at `/`.
- **The instance port pool** (`port_pool.from`–`port_pool.to`, default
  20000–21000): leased to instances for their background services. Leases
  are persisted under `<root>/ports/` and are sticky per instance across
  restarts. A run's trailer line `CI_URL=http://<host>:<port>` points at
  the leased port; `<host>` is the host the pusher connected to, or
  `127.0.0.1` for API-triggered runs.

Size the pool to the number of instances you expect to run simultaneously;
an exhausted pool fails the run with a clear error.

## Data and logs on disk

Everything lives under the data root (full layout in
[architecture.md](architecture.md#data-root-layout)). Operationally relevant:

- `logs/daemon.log` — daemon diagnostics, JSON, rotated at 64 MiB keeping 5
  files. stderr always gets a copy.
- `runs/<project>/<instance>/` — per-run build logs and records; the last 20
  runs per instance are kept.
- `instances/<project>/<instance>/logs/service.log` — the supervised
  background process's stdout/stderr, append-only; pushrun never rotates or
  deletes business logs — retention is the pipeline's business.
- `provider-data/` — warmed provider artifacts, keyed by parameter
  fingerprint; safe to delete (they are rebuilt on demand) while the daemon
  is stopped.

Definitions (`projects/`, `providers/`) are re-read from disk at the start
of every run or warmup, so manual YAML edits take effect on the next run.
The API is the recommended writer because it validates before persisting.
