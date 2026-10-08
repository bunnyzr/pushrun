# pushrun design

This document is the lasting design reference for contributors: what pushrun
is, the domain model, and the contracts that hold the pieces together.
Implementation details live in the companion documents:

- [architecture.md](architecture.md) — how the daemon is built (git serving,
  hooks, the run engine's mechanics, logs, repository layout)
- [provider-contract.md](provider-contract.md) — the provider authoring
  contract (lifecycle phases, environment, parameter schema)
- [api.md](api.md) — the REST API, SSE streams, and the git endpoint
- [deployment.md](deployment.md) — installing and operating the daemon

## Purpose

pushrun solves one problem: some code can only build and run on a specific
machine (an intranet dev box, a build host with a special toolchain, a machine
with licensed software). pushrun lets you write code locally, `git push` it to
the machine where pushrun runs, and have that machine assemble, build, start,
and serve the program — with build output streaming back into your terminal
and logs available over the CLI, the API, and the web console.

The target machine is the git server. A git hook on that machine triggers the
build-and-run pipeline. There is no central SaaS: every deployment is
self-contained on one trusted host.

## Non-goals

- Not a CI system. No DAG pipelines, no artifact registry, no build grid.
- Not a PaaS. No multi-host scheduling, no containers managed by pushrun
  itself. Docker may later become an *execution backend* without changing the
  domain model.
- No anonymous multi-tenant hosting. The API executes trusted local commands;
  deployments must be authenticated (built-in token auth is on by default).

## Domain model

```text
Git Repo -> Git Worktree

Project
  -> Project Tree  (Nodes + Mounts, content supplied by Providers)
  -> CI Pipeline   (linear Steps, run after assembly)
  -> Project Instance (an actual running copy, explicitly named)
  -> Run (the historical record of one execution)
```

### Git Repo / Git Worktree

A Git Repo identifies a remote code source by its **repo identity**,
`host/path` (e.g. `github.com/acme/hello-web`): full URLs are normalized by
dropping scheme and userinfo (the host keeps its port), scp-like
`user@host:path` syntax normalizes to `host/path`, a trailing `.git` is
dropped, and a local absolute path keeps its first segment as the host. The
scp form has no port notation: a colon followed by digits and a slash is
read as the port of the canonical `host:port/path` identity, not as scp
syntax. The
identity keys the daemon's bare repos, the push URLs
(`/git/<host>/<path>.git`), and the matching of a push to projects. A Git
Worktree is a local checkout managed by pushrun. Users never maintain
worktrees; they configure git mounts in the Project Tree.

### Project

A Project is a complete build-and-run target. It contains exactly two
definitions: a **Project Tree** and a **CI Pipeline**. A Project may designate
one **primary git node**, used for default push matching and display naming:

- Single-repo Project: display name derives from repo host + path.
- Multi-repo Project: display name is user-provided.

### Project Tree, Node, Mount

The Project Tree defines the initial directory layout of the Project. A Node
is a position in the tree; nothing more. A Mount declares where a Node's
content comes from (a Provider plus parameters).

- A Node without a Mount is a plain directory level.
- A Node with a Mount is a complete content boundary: it cannot have children.
- Mounting means *exact replacement* of the target node: replace it if it
  exists, create parents if it does not, then let the source decide whether to
  copy, symlink, extract, or generate.
- One Git Repo may be mounted at multiple Nodes.

### Provider

A Provider describes where a Mount's content comes from and how it is brought
to life. A Provider has up to two lifecycle phases:

| Phase | Required | Semantics |
|---|---|---|
| `warmup` | optional | Build a reusable **shared artifact** (download, extract, compile, patch). Runs once per parameter set; the result is shared across Instances. |
| `install` | required | Place content into one Instance's target Node (copy, symlink, generate, initialize). Runs on every assembly. |

Providers are data — a YAML descriptor plus Bash scripts — replaceable without
recompiling the daemon. Scripts are plain Bash; success/failure is the exit
code; context arrives via environment variables; parameters are self-declared
in the YAML (the planned web console's editor UI will be generated from the
declaration). `git` and
`symlink` are platform built-in Providers; everything else (language runtimes,
dictionaries, shared tools) is an ordinary external Provider. The full
contract is [provider-contract.md](provider-contract.md).

### Warmup semantics (`EnsureWarmed`)

A Run never fails merely because a Provider is cold. The Run chain always
calls `EnsureWarmed(provider)` for every Provider referenced by the Project
Tree:

- The shared artifact directory is keyed by a fingerprint of
  `provider id + warmup-scoped parameters`. A `.pushrun-ready` marker inside
  the directory records the fingerprint.
- Marker present and fingerprint matches → skip (the normal, zero-cost path).
- Marker missing or fingerprint stale → run the `warmup` script, then write
  the marker. A failed warmup never leaves a ready marker behind.
- Warmup failure stops the Run before any install, reporting the provider and
  phase.
- Warmup is serialized per (provider, fingerprint) with a file lock: two
  concurrent Runs share one warmup, never corrupt the artifact dir.
- The manual warmup endpoints (single Provider or all Providers of a Project)
  call the exact same `EnsureWarmed`; they exist to shorten the first Run and
  to validate configuration. There is no behavioral fork between manual and
  automatic warmup.

### CI Pipeline

A linear list of Steps executed after the Instance is assembled. Steps cover
the project's own build/test/configure/start needs (compiling, config
generation, port rewriting, service start, health checks). Content from other
repos, runtimes, and dictionaries is a Provider concern and must not appear as
implicit pipeline dependencies.

Step fields: `name`, `run` (shell command), `timeout` (seconds, default 300),
`background` (bool; at most one, must be last; becomes a supervised daemon
process group), `health` (optional check: `tcp` | `http` | `command`).

### Project Instance

An Instance is a named, isolated running copy of a Project. It owns: its
directory, port allocation, process, state, build log, and business logs.

- Instances are **explicitly named**; branch and Instance are independent
  dimensions. The default Instance is named `default`.
- Multiple Instances of one Project run in parallel (different branches,
  different experiments).
- Runs of the same Instance are serialized (file lock); different Instances
  run concurrently.
- An Instance is auto-created on first reference (e.g. `--instance foo` on a
  push or a Run API call); naming stays explicit, creation needs no separate
  step.

### Run

A Run is one execution attempt against an Instance. It records the triggering
commit, a pipeline snapshot, per-step results, errors, and output. Re-running
the same commit is a first-class operation (`rerun`) — no empty commits
required; CLI and API share the same Run semantics.

## The run chain

```text
push or manual trigger
  -> resolve Project + Instance
  -> EnsureWarmed(all referenced Providers)
  -> assemble Instance directory per Project Tree
  -> install the triggering Git Worktree at the matching Node
  -> execute CI Pipeline Steps in order
  -> health check
  -> update Instance state, ports, supervised process
  -> build output and business logs stay available
```

Failure semantics: report the failing step or provider directly. No staging
directories, no implicit content rollback, no complex state machine. pushrun
only replaces Nodes it owns; files generated by the pipeline are never
silently cleaned. If a new Run fails, the Instance shows the failure and the
real process state — never stale success. Whether the old service stops before
build, and when it restarts, is the pipeline's business; pushrun only manages
processes it started and recorded. The engine mechanics (locking, checkout,
port pool, crash recovery) are covered in
[architecture.md](architecture.md#the-run-engine).

## Triggering a run

A run is triggered by pushing `HEAD` to `refs/pushrun/for/<branch>` of
`<server>/git/<host>/<path>.git`, or by an explicit API/CLI action
(`start`, `rerun`, ...). **Any git node of a project can trigger it** — the
triggering repo does not have to be the primary one.

Push-to-project matching keys on the repo identity in the push URL:

- The repo is the primary git node of exactly one project and no other
  project mounts it → the push triggers that project.
- The repo appears only as a non-primary git node → the push is rejected at
  pre-receive with `project_required:<candidates>`; re-push with
  `git pushrun --project <name>` (an `X-PushRun-Project` header) naming one
  of the candidates.
- Several projects mount the repo with a primary among them → rejected with
  `ambiguous_project:<candidates>`; again, `--project` disambiguates.
- No project mounts the repo → `unknown_repo:<identity>`.
- An explicit project that does not mount the pushed repo →
  `project_does_not_contain_repo:<project>`.

Within the run, the **triggering node** (the git node whose repo was pushed)
receives exactly the pushed commit. Every other git node installs its
**snapshot**: the `refs/pushrun/for/<branch>` ref of the daemon's bare repo
for that identity. A snapshot only moves when someone pushes that branch
through pushrun — a run triggered by repo A never silently pulls newer
content of repo B. A repo nobody has pushed yet is seeded once: the snapshot
ref is fetched from the mount's `repo` source (a full URL, a local path, or
a `host/path` shorthand expanded with the server's `git.scheme`).

Pipeline steps receive the trigger context as environment variables, in
addition to the base `CI_*` set (see [File formats](#file-formats)):

| Variable | Meaning |
|----------|---------|
| `CI_WORKSPACE` | the instance root directory |
| `CI_TRIGGER_PROJECT_DIR` | absolute path of the triggering git node |
| `CI_TRIGGER_PROJECT_NAME` | path part of the triggering repo identity |
| `CI_PROJECT_DIR` | absolute path of the primary git node |
| `CI_PROJECT_NAME` | path part of the primary repo identity |

For runs not triggered by a push (pure API/CLI actions) the primary git
node plays the trigger role, so the `CI_TRIGGER_*` pair names it; both pairs
are omitted only when the project has no corresponding git node.

## Authentication model

pushrun is single-tenant: one deployment, one token, no user accounts.
Multi-person collaboration is a social convention over explicitly named
Instances (conventionally branch names), not a permission system — everyone
who holds the token is trusted.

- A single token authenticates everything: API and SSE
  (`Authorization: Bearer <token>`), the client scripts, and git pushes (HTTP
  Basic with the token as password, or a Bearer header — the client uses the
  latter so no credential-helper setup is required).
- The token exists because pushrun is a remote command-execution endpoint:
  even on a trusted network, an unauthenticated port that runs arbitrary
  shell commands is unacceptable. It is generated on first boot, stored in
  the data root (0600), and printed once to the console; `pushrun token show`
  recovers it on the server host. The user touches the token exactly once, at
  client install.
- Auth can be explicitly disabled for deployments behind an authenticating
  reverse proxy; it is never off by default.
- The hook-internal endpoint is separate: loopback origin plus a **persistent**
  hook secret stored in the data root (0600) and injected into hooks at
  install time. A daemon restart must not invalidate installed hooks — a
  per-boot secret would break every push after the first restart.

## The client

The client is a **thin POSIX shell script rendered and served by the server**
(`/install.sh`, `/client.sh`). It is glue: `git push` with extra headers plus
a few HTTP convenience calls. All complexity lives server-side.

- `install.sh` installs the `git pushrun` git alias, caches the client script
  at `~/.local/share/pushrun/client.sh`, and stores the server and token in
  `~/.config/pushrun/config` (0600).
- The client carries its version and re-downloads itself only when the server
  version changed — no per-run remote code execution, no per-run download
  latency.
- For pushes, the client injects the token through git's environment-based
  config (`GIT_CONFIG_COUNT` / `GIT_CONFIG_KEY_n` / `GIT_CONFIG_VALUE_n`, git
  ≥ 2.31) as an `Authorization: Bearer` extraHeader scoped to the pushrun
  host: the token never lands in `.git/config`, never appears in `ps`, and is
  never sent to other remotes.
- Subcommands: `run` (default), `sync`, `test`, `build`, `start`, `stop`,
  `restart`, `status`, `rerun`, `logs ls|tree|fetch [-f]`, `doctor`,
  `update`.
- The push URL derives from the local repo's origin identity: the client
  normalizes `remote.origin.url` (URL or scp-like syntax) to `host/path` and
  pushes to `<server>/git/<host>/<path>.git`. Which project a command acts
  on is resolved the same way the server matches a push: `--project NAME`
  (or `PUSHRUN_PROJECT`) asserts an explicit project, otherwise the client
  asks `GET /api/v1/resolve?repo=<host/path>`. Matching errors
  (`project_required`, `ambiguous_project`) come back with an actionable
  hint to re-run with `--project`.
- Before pushing, the client compares remote refs; an unchanged commit routes
  to `rerun` instead of re-uploading code.

## Web console

The daemon embeds and serves a browser console at `/`. It is an admin
console, not a marketing site: three pages — Projects, Instances,
Providers — plus a token prompt when auth is enabled. In-app documentation
is deliberately omitted; the README and `docs/` serve that role.

**Stack and build.** The console is a Vite + React + TypeScript SPA in
`web/`, styled with Tailwind and shadcn/ui, using TanStack Query for server
state and react-i18next for bilingual (English/Chinese) text — the UI
language follows the browser with a manual toggle. `npm run build` emits
`web/dist/`, which is committed to the repo: `go install` embeds the real
console without npm available. CI rebuilds it and fails on a stale dist.

**Routing and caching.** Browser-history routing; the static handler falls
back to `index.html` for unknown non-API paths so deep links survive a
reload. `index.html` is served no-cache; hashed assets under `/assets/` are
immutable.

**Auth.** With auth enabled the console prompts for the token once, keeps it
in localStorage, and sends `Authorization: Bearer` on every call; a 401
clears it and re-prompts. `GET /api/v1/version` is exempt from auth and
reports `{version, auth: {enabled}}` so the console knows whether to prompt.
SSE streams are read with `fetch` + `ReadableStream` — EventSource cannot
set headers, and the token never appears in a URL.

**Projects page.** A three-column editor: project list, editable tree, node
inspector. Tree nodes are renamed inline and added without dialogs, can be
dragged to move, and a node carrying a mount cannot take children. The
inspector's parameter form is generated from the selected provider's
declared params — nothing is hard-coded per provider; required params block
saving. Pipeline steps (command, timeout, the final background-service step)
are edited on the same page. Saving is explicit: on success the page
re-renders from the server's project, on failure the draft survives with the
error located. Switching projects with unsaved changes asks once, then drops
every piece of selection, detail, and in-flight state.

**Instances page.** One row per instance: project, branch, commit, status,
port, access URL, last run result, and the actions run/stop/restart/
rerun/delete. The log area combines the build log (streams live over SSE)
with browsable business-log files; lines and files can be copied or
downloaded. Switching instances cancels in-flight requests and clears the
previous instance's paths, text, and filters; an instance without logs shows
an explicit empty state.

**Providers page.** External providers only — git and symlink are platform
capabilities, selectable in the node editor but not managed here. A provider
renders as a lifecycle card: warmup status (cold / warming / warm; a failed
warmup is reported by the warmup call itself), declared params, scripts
view/edit, warmup and re-warmup actions, and referencing projects (the API
refuses to delete a referenced provider and names the referrers).

**Warmup observability.** `EnsureWarmed` writes a `warmup.json` next to the
ready marker (fingerprint, warmed_at, params with secrets redacted) and the
executor tracks in-flight warmups. `GET /api/v1/providers` carries a
`warmups` array per provider; `GET /api/v1/projects/{name}/warmup` reports
per-mount status (cold/warming/warm) — GET reads status, POST triggers.

## Data root layout

```text
<root>/
  config.yaml               server config (http, port pool, auth)
  token                     generated token (0600)
  hooks-secret              hook shared secret (0600)
  projects/<name>.yaml      Project definitions (schema pushrun.project/v1)
  providers/<id>/           provider.yaml + scripts/ (schema pushrun.provider/v1)
  repos/<host>--<path>.git   bare repos, keyed by repo identity
  instances/<project>/<instance>/   assembled directories
  state/<project>/<instance>/{latest,current}.json
  runs/<project>/<instance>/<run-id>/   build logs, run records
  provider-data/<id>-<fingerprint>/   warmed shared artifacts (+ .pushrun-ready)
  logs/daemon.log             daemon log (JSON, rotating)
  ports/                      port leases
  locks/                      flock files
```

All definitions are editable at runtime through the API and take effect
immediately — no restarts. Definitions are re-read from disk whenever a Run
or warmup starts, so manual YAML edits also take effect on the next Run; the
API is the recommended writer (it validates before persisting). The full
annotated layout, including log locations, is in
[architecture.md](architecture.md#data-root-layout).

## File formats

### Project (`pushrun.project/v1`)

```yaml
schema: pushrun.project/v1
name: hello-web
display_name: Hello Web
tree:
  - path: app
    mount:
      provider: git
      primary: true
      params: { repo: github.com/acme/hello-web, branch: main }
  - path: runtime
    mount:
      provider: example-static-runtime
      params: { version: "1.2.3" }
pipeline:
  - name: build
    run: make -C app build
    timeout: 300
  - name: serve
    run: ./app/bin/hello --port "$CI_PORT"
    background: true
    health: { type: http, target: "http://127.0.0.1:$CI_PORT/healthz" }
```

Pipeline steps receive injected environment: `CI_PROJECT`, `CI_INSTANCE`,
`CI_USER`, `CI_BRANCH`, `CI_COMMIT`, `CI_PORT` (the leased port),
`CI_RUN_ID`, plus the workspace and git-node variables `CI_WORKSPACE`,
`CI_TRIGGER_PROJECT_DIR` / `CI_TRIGGER_PROJECT_NAME`, and
`CI_PROJECT_DIR` / `CI_PROJECT_NAME` (see
[Triggering a run](#triggering-a-run)). Project YAML references them as
ordinary shell variables.

### Provider (`pushrun.provider/v1`)

```yaml
schema: pushrun.provider/v1
id: example-static-runtime
name: Example Static Runtime
description: Downloads a runtime tarball and installs it into instances.
warmup: scripts/warmup.sh      # optional phase
install: scripts/install.sh    # required phase
parameters:
  - id: version
    label: Runtime version
    type: string
    scope: warmup
    required: true
```

The parameter declaration schema and the executor's environment contract are
specified in [provider-contract.md](provider-contract.md).

### Server config

A single YAML: `http` (bind, port), `port_pool` (range), `auth` (enabled,
token), `log` (level), `git` (scheme). Every path derives from the data
root. See [deployment.md](deployment.md#configuration).

## API surface

Everything lives on one HTTP port: the REST API under `/api/v1`, SSE log
streams, the git smart-HTTP push endpoint at `/git/<host>/<path>.git/...`,
the client scripts `/install.sh` and `/client.sh`, and the embedded web
console at `/`. The loopback-only hook endpoint
`/internal/v1/hooks/post-receive` is guarded by the hook secret. The full
reference is [api.md](api.md).

Everything static — the web placeholder page, built-in provider scripts,
client scripts — is embedded in the binary via `embed.FS`, which is why the
assets under `web/dist/` are committed to the repo: `go install` compiles
from module source with no npm available, and must still produce a
self-contained binary.

## Explicitly deferred

- **Repo-root `.pushrun.yaml` auto-provisioning** (first push creates the
  Project). Attractive for zero-to-first-run, but it creates a second source
  of truth for Project definitions, conflicting with the API-first user
  path. A push to an unknown Project instead fails with an actionable error
  explaining how to create it via the API.
- **Browser-level e2e tests for the web console** (Playwright or similar) —
  the console ships with Vitest unit/component coverage; the push-to-serve
  loop itself is covered by the Go e2e suite.
- **Docker as an execution backend** — the domain model already admits it.
- **Per-user identity, quotas, and ACLs** — pushrun is single-tenant by
  design; collaboration happens over explicitly named Instances. A shared
  deployment trusts everyone who holds the token.
- **Distributed tracing** (OpenTelemetry and friends) — pushrun is a
  single-process daemon with no cross-service spans; correlation rides on
  `run_id` and `request_id` in the daemon logs instead.
