# pushrun architecture

pushrun is a single Go binary that turns one machine into a push-to-run
target. There is no central service: each deployment is one daemon with one
data root on one trusted host. This document describes how the daemon is put
together; the terminology comes from [design.md](design.md).

## Domain model

```text
Git Repo -> Git Worktree (managed by pushrun; users never touch one)

Project
  -> Project Tree     nodes + mounts; content supplied by providers
  -> CI Pipeline      linear steps, run after assembly
  -> Project Instance a named, isolated running copy (default: "default")
  -> Run              the historical record of one execution
```

- A **Project** is a complete build-and-run target: a tree plus a pipeline,
  stored as `projects/<name>.yaml` (schema `pushrun.project/v1`).
- A **Node** is a position in the tree. A node with a **Mount** is a complete
  content boundary (no children); mounting means *exact replacement* of that
  node by the provider's install output.
- A **Provider** describes where a mount's content comes from (see
  [provider-contract.md](provider-contract.md)). `git` and `symlink` are
  built into the daemon; everything else is an external provider (YAML +
  bash scripts under `providers/<id>/`).
- An **Instance** owns its directory, port lease, supervised process, state,
  and logs. Branch and instance are independent dimensions; instances are
  auto-created on first reference.
- A **Run** is one execution attempt against an instance, recorded under
  `runs/<project>/<instance>/<run-id>/` (`build.log` + `result.json`; the
  last 20 runs per instance are kept).

## Single HTTP port

One HTTP server carries everything — no SSH, no extra protocols:

- git smart-HTTP push at `/git/<host>/<path>.git/...` (an embedded
  `git http-backend` CGI; the `host/path` prefix is the repo identity),
- the REST API under `/api/v1` (see [api.md](api.md)),
- SSE log streams,
- the client scripts `/install.sh` and `/client.sh`,
- the embedded web console at `/` (a single-page app; extensionless paths
  that match no static file are served `index.html` so deep links into the
  client-side router work).

## Git serving and hooks

- Bare repos live at `repos/<host>--<path>.git` (every `/` of the repo
  identity becomes `--`), created on demand on the first git request, with
  `http.receivepack` enabled.
- Pushes target the isolated ref namespace `refs/pushrun/for/<branch>`;
  user refs are never touched. A pre-receive hook enforces this where git
  can still reject the push (post-receive's exit status is ignored by git),
  and matches the repo identity against the registered projects (see
  [design.md](design.md#triggering-a-run)).
- Metadata rides in custom headers (`X-PushRun-User`, `X-PushRun-Action`,
  `X-PushRun-Instance`, `X-PushRun-Project`), forwarded into the hook
  environment. `X-PushRun-User` is self-asserted display metadata for run
  records, never an identity source.
- Both hooks are one-liners that exec the same binary — `pushrun hook
  pre-receive` and `pushrun hook post-receive` — with `PUSHRUN_BIN` /
  `PUSHRUN_ROOT` / `PUSHRUN_HOOK_SECRET` / `PUSHRUN_REPO` / `PUSHRUN_PORT`
  injected when they are installed. An explicit project override
  (`X-PushRun-Project`) is not baked into the hook; it arrives from the
  pusher's headers.
- The post-receive hook process forwards stdin to the daemon over loopback
  (`POST /internal/v1/hooks/post-receive`, guarded by a persistent hook
  secret stored 0600 in the data root), then relays the daemon's streamed
  response to its own stdout — so build output flows back through the git
  sideband channel into the pusher's terminal. The hook parses the trailing
  `CI_STATUS=` line to choose its own exit code. Because git ignores
  post-receive failures, the *client* additionally checks the run record via
  the API and exits non-zero unless the latest run is `SUCCESS`.

## The run engine

One push (or API action) triggers the run chain:

```text
push or manual trigger
  -> resolve project + instance (unknown project: actionable error)
  -> take the per-instance lock (contention -> CI_STATUS=BUSY)
  -> EnsureWarmed every provider referenced by the tree
  -> assemble the instance directory per the project tree
  -> install the triggering commit at the matching git node
  -> run the pipeline steps in order
  -> promote the final background step to a supervised process group
  -> health check
  -> persist the run record and instance state; emit trailers
```

Mechanics:

- **Locking**: runs of one instance serialize on a non-blocking flock at
  `locks/<len>:<project>-<len>:<instance>` (length-prefixed, so name pairs
  like `("a-b","c")` and `("a","b-c")` never collide); different instances
  run concurrently. Provider warmups serialize per (provider, fingerprint).
- **Assembly**: plain nodes are created; mount nodes are removed and
  re-installed (`git read-tree --reset -u` + `git clean -fdx` for git
  mounts). Files the pipeline generated outside mounted nodes are never
  silently cleaned.
- **Steps** run as `bash -c` in the instance directory, each in its own
  process group, with a per-step `timeout` (default 300 s) and cancellation
  that kills the whole group. Steps receive `CI_PROJECT`, `CI_INSTANCE`,
  `CI_USER`, `CI_BRANCH`, `CI_COMMIT`, `CI_RUN_ID`, and `CI_PORT` (the leased
  port) in their environment.
- **Background step**: at most one, and it must be last. It is started as a
  supervised process group; its stdout/stderr are appended to the instance's
  own business-log dir at
  `instances/<project>/<instance>/logs/service.log`. Before a new one
  starts, the previously supervised process group is killed.
- **Health checks**: `tcp`, `http`, or `command` probes (defaults: 1 s
  interval, 30 retries; `$CI_*` variables in the target are expanded). A
  failed health check kills the new process and fails the run.
- **Trailers**: run output always ends with `CI_STATUS=SUCCESS|FAILED|BUSY`,
  plus `CI_PORT=` and `CI_URL=http://<host>:<port>` when a background
  service is live. For push-triggered runs `<host>` is the host the pusher
  connected to (the Host header of the push request); API-triggered runs
  fall back to `127.0.0.1`.
- **Actions**: `run` (full chain), `sync` (warmup + assemble only), `build` /
  `test` (steps without the background service), `start` / `stop` /
  `restart` (act on the supervised process without re-running steps),
  `rerun` (re-run the instance's last recorded commit — no empty commits
  needed).
- **Failure semantics**: the failing step or provider is reported directly;
  the instance shows the failure and the real process state — never stale
  success. A previously supervised process that is still alive keeps being
  tracked across a failed run.
- **Crash recovery**: on boot the daemon re-adopts supervised processes
  recorded in `state/<project>/<instance>/current.json` and reaps dead ones.
- **Port pool**: instances lease ports from a configured range (default
  20000–21000). Leases are files under `ports/`, sticky per instance, and
  survive restarts.

## Authentication

pushrun is single-tenant: one deployment, one token, no user accounts.

- A single token authenticates the API (`Authorization: Bearer`), the client
  scripts, and git pushes (HTTP Basic with the token as password, or a
  Bearer header — the client uses the latter, injected through git's
  environment-based config so the token never lands in `.git/config`).
- The token is generated on first boot, stored 0600 at `<root>/token`, and
  printed once to the console; `pushrun token show` recovers it on the host.
  Setting `auth.token` in `config.yaml` overrides it.
- Auth can be disabled (`auth.enabled: false`) for deployments behind an
  authenticating reverse proxy; it is never off by default.
- The hook-internal endpoint requires a loopback peer plus the persistent
  hook secret — never the user token.
- Provider parameters declared `secret: true` are masked in API responses
  and redacted from build logs.

## Logs and realtime

Three kinds of logs, deliberately separate:

- **Daemon operational logs** — pushrun's own diagnostics: stderr plus
  `logs/daemon.log` (JSON, rotated at 64 MiB × 5 files).
- **Build logs** — run/step/provider output, captured per run at
  `runs/<project>/<instance>/<run-id>/build.log`, streamed to the pusher via
  sideband and to API consumers over SSE (`GET /api/v1/runs/{id}/output`).
- **Business logs** — the user's service output. The background step's
  stdout/stderr go to `instances/<project>/<instance>/logs/service.log`;
  every file under the instance directory's `logs/` subdirectory is
  browsable as a tree and followable over SSE via
  `GET /api/v1/instances/{project}/{instance}/logs/...`. pushrun stores and
  serves business logs but never parses, rotates, or deletes them.

## Data root layout

```text
<root>/
  config.yaml               server config (http, port_pool, auth, log, git)
  token                     generated auth token (0600)
  hooks-secret              hook shared secret (0600)
  projects/<name>.yaml      project definitions (pushrun.project/v1)
  providers/<id>/           provider.yaml + scripts/ (pushrun.provider/v1)
  repos/<host>--<path>.git  bare repos with pushrun hooks, keyed by repo identity
  instances/<project>/<instance>/   assembled directories
    logs/service.log          background step output (business log)
    logs/...                  other service-written business logs
  state/<project>/<instance>/{latest,current}.json
  runs/<project>/<instance>/<run-id>/{build.log,result.json}
  provider-data/<id>-<fingerprint>/ warmed shared artifacts (+ .pushrun-ready)
  logs/daemon.log           daemon log (JSON, rotating)
  ports/<port>              port leases
  locks/                    flock files (instances, provider warmups)
```

All definitions are re-read from disk whenever a run or warmup starts, so
edits take effect on the next run without a restart. The API is the
recommended writer (it validates before persisting); the embedded web
console, served at `/`, offers the same operations from a browser.

## Repository layout

```text
cmd/pushrun/        single binary: serve / hook pre-receive|post-receive / token / version
internal/
  config/           server config, data-root paths, token and hook secret
  project/          project model + storage, push-to-project matching
  provider/         provider model, parameter declarations, executor, builtins
  gitx/             git CLI wrapper: repo identities, bare repos, hooks, checkouts
  run/              run engine: warmup -> assemble -> install -> steps -> health
  instance/         instance state, port pool, process-group supervision
  server/           HTTP routes, REST API, SSE, git endpoint, hooks
  hookclient/       the `pushrun hook ...` entry points (pre-receive, post-receive)
  client/           client script templates (install.sh, client.sh)
  logging/          slog setup + size-based rotation
web/                the web console (React SPA); web/dist is embedded into the binary
examples/           runnable demo project + example provider
docs/               this documentation
```
