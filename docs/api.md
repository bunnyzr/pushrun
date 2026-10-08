# pushrun API reference

All endpoints live on the daemon's single HTTP port (default `:8000`).
Unless noted, everything under `/api/v1` requires
`Authorization: Bearer <token>` when auth is enabled (the default).

Errors have a uniform shape, and every response carries an `X-Request-Id`
header that also appears in error bodies and daemon logs:

```json
{"error": {"code": "not_found", "message": "...", "request_id": "..."}}
```

## Projects

Project bodies are the JSON rendering of the `pushrun.project/v1` YAML
(`schema`, `name`, `display_name`, `tree`, `pipeline`); `schema` may be
omitted on write. Mount parameters declared `secret` by their provider are
masked as `"***"` in all responses, and a PUT that round-trips a masked
value keeps the stored secret. A masked value that cannot be matched back to
a stored secret — because the node was renamed or moved, or the project is
new — is never dropped or persisted literally: the PUT fails with 400
`secret_params_orphaned` (plus an `orphaned_params` list of `path: param`
entries) and the secret must be re-entered.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/projects` | List all projects (`{"projects": [...]}`). |
| POST | `/api/v1/projects` | Create a project. 409 if the name exists. |
| GET | `/api/v1/projects/{name}` | Get one project. |
| PUT | `/api/v1/projects/{name}` | Replace a project definition (validated before persisting). |
| DELETE | `/api/v1/projects/{name}` | Delete the definition only; running instances are never stopped. |
| POST | `/api/v1/projects/{name}/warmup` | Warm every mount of the tree (`{"status", "results": [{"provider", "path", "status", "error?"}]}`); mounts sharing a provider and identical warmup params warm once. |
| GET | `/api/v1/projects/{name}/warmup` | Per-mount warmup status without warming anything (`{"mounts": [{"path", "provider", "status": "cold"\|"warming"\|"warm", "warmed_at"?}]}`). |
| POST | `/api/v1/projects/import` | Import a `.tar.gz` bundle containing exactly one project `.yaml` (raw body, any content type). Refuses — writing nothing — when referenced providers are missing (400 `missing_providers` with a `missing_providers` list). |
| GET | `/api/v1/projects/{name}/export` | Download the project as a `.tar.gz` bundle. |

## Providers

Create/update bodies are the `pushrun.provider/v1` descriptor plus an
optional `scripts` map of slash-separated relative paths to file contents.
The built-in `git` and `symlink` providers are always listed
(`"builtin": true`); their ids are reserved and they cannot be modified,
exported, or deleted.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/providers` | List builtins + stored providers (`{"providers": [...]}`). Each object carries a `warmups` array of shared-artifact cache entries (`{"fingerprint", "warmed_at", "params", "warming"}`; always an array, empty for the `git` builtin, whose cache is the bare repo). |
| POST | `/api/v1/providers` | Create a provider (descriptor + inline scripts). 409 if it exists. |
| GET | `/api/v1/providers/{id}` | Get one provider (same shape as a list entry, including `warmups`). Stored providers additionally carry a `scripts` map of slash-separated relative paths to file contents; scripts are text files, files larger than 1 MiB are omitted, and content that is not valid UTF-8 is returned lossily. |
| PUT | `/api/v1/providers/{id}` | Replace a provider. |
| DELETE | `/api/v1/providers/{id}` | Delete; 409 with `referenced_by` while any project mounts it. |
| POST | `/api/v1/providers/{id}/warmup` | Warm one provider; optional body `{"params": {...}}`. Responds `{"provider", "status", "error?"}`. |
| POST | `/api/v1/providers/import` | Import a `.tar.gz` bundle with `provider.yaml` at the root plus its scripts. Fully validated before anything is written. |
| GET | `/api/v1/providers/{id}/export` | Download the provider as a `.tar.gz` bundle. |

## Instances and runs

An instance is addressed as `{project}/{instance}`; it is auto-created on
first reference. Action responses are `{"run_id", "status", "port", "url"}`
with `status` ∈ `SUCCESS | BUSY` (409 `busy` when another run holds the
instance lock). A failed run does not return a `FAILED` body: the endpoint
answers 500 with error code `run_failed` instead.

> Future consideration: whether a failed run should instead return 200 with
> `status: "FAILED"` so clients can inspect a structured result without
> treating it as a transport-level error. The 500 `run_failed` contract
> above is the current behavior.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/instances` | List all instance states (`{"instances": [...]}`). |
| GET | `/api/v1/instances/{project}/{instance}` | Get one instance's state (status, commit, branch, port, URL, ...). |
| POST | `/api/v1/instances/{project}/{instance}/{action}` | `run` \| `start` \| `stop` \| `restart` \| `rerun`; optional body `{"branch", "commit", "user"}`. `rerun` re-runs the instance's last recorded commit. `start`/`stop`/`restart` act on the supervised process only: they produce no run record and respond with an empty `run_id`. Runs synchronously and survives client disconnects. |
| DELETE | `/api/v1/instances/{project}/{instance}` | Stop the process, release the port, remove the instance's directory, state, and run history. 409 while a run is in flight. |
| GET | `/api/v1/instances/{project}/{instance}/runs` | Run records, newest first (`{"runs": [...]}`). |
| GET | `/api/v1/runs/{id}` | One run record by its global id, with `project`/`instance`. |
| GET | `/api/v1/runs/{id}/output` | The run's build log as SSE: `log` events replay then follow, and a terminal `status` event (`{"status": "..."}`) closes the stream. Works for in-flight runs. |

## Business logs

Files a service writes under the instance directory's `logs/` subdirectory.
Only relative paths cross the API — never absolute server paths.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/instances/{project}/{instance}/logs/tree` | Sorted relative file list (`{"files": [...]}`). |
| GET | `/api/v1/instances/{project}/{instance}/logs/file?path=<rel>&tail_lines=N&follow=1` | Default: JSON `{"path", "content"}` (`tail_lines` keeps the last N lines). `follow=1` switches to SSE `log` events that replay (or tail) then stream until disconnect. |

## Client distribution

| Method | Path | Description |
|--------|------|-------------|
| GET | `/install.sh` | The installer, rendered with the server URL and version baked in. Bearer-authenticated. |
| GET | `/client.sh` | The `git pushrun` client script. Bearer-authenticated. |
| GET | `/api/v1/version` | `{"version": "...", "auth": {"enabled": bool}}` — the client's version handshake. Exempt from token auth so the web console can learn whether auth is enabled before it has a token. |

## Repo resolution

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/resolve?repo=<host/path>[&project=<name>]` | Which project a push of the repo identity would trigger, using the same matching as the pre-receive hook; `project` asserts an explicit project (mirrors the `X-PushRun-Project` push header). 200 `{"project": "<name>"}`; 409 with the matching code (`unknown_repo:<id>`, `project_required:<names>`, `ambiguous_project:<names>`, `project_does_not_contain_repo:<p>`, `unknown_project:<p>`) when the repo does not resolve to exactly one project; 400 for an unparseable repo reference. |

## Git endpoint

| Method | Path | Description |
|--------|------|-------------|
| GET/POST | `/git/{host}/{path}.git/...` | git smart-HTTP (embedded `git http-backend`); the `{host}/{path}` prefix is the repo identity that keys the on-demand bare repo and the push-to-project matching. Auth: HTTP Basic with the token as password, or `Authorization: Bearer`. Pushes must target `refs/pushrun/for/<branch>` (enforced by a pre-receive hook, which also rejects pushes whose repo identity does not resolve to a project). Push metadata headers: `X-PushRun-User` (display only), `X-PushRun-Action`, `X-PushRun-Instance`, `X-PushRun-Project` (explicit project, disambiguates repos mounted by several projects or mounted only as non-primary nodes). Build output streams back over the push sideband. |

## Internal

| Method | Path | Description |
|--------|------|-------------|
| POST | `/internal/v1/hooks/post-receive` | Daemon side of the git hook. Loopback peers only, guarded by the hook secret (`X-PushRun-Hook-Secret`), never the user token. Metadata in `X-PushRun-Repo` (repo identity, required) / `-Project` (explicit project, optional) / `-Branch` / `-Commit` / `-User` / `-Action` / `-Instance` / `-Host`; the response body is the streamed run output ending in a `CI_STATUS=` trailer. |

## Web console

`GET /` serves the embedded web console: a single-page app with three pages —
**Projects** (browse and edit project trees, mounts, and pipelines),
**Providers** (inspect provider state and script files, manage the warmup
lifecycle), and **Instances** (inspect instance state, trigger run actions,
and follow live build logs over SSE). When auth is enabled the console
prompts for the bearer token once and remembers it in the browser. Deep links
such as `/projects` work directly: extensionless paths that match no static
file are served the console shell (see
[architecture.md](architecture.md#single-http-port)).
