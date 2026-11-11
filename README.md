# pushrun

pushrun turns one machine into a push-to-run target: write code anywhere,
`git push` it to the machine where pushrun runs, and that machine assembles,
builds, starts, and serves your program — with the build output streaming
back into your terminal. The target machine *is* the git server; a git hook
triggers the build-and-run pipeline. There is no central SaaS: every
deployment is a single self-contained daemon on one trusted host.

## Features

- **Push to deploy** — `git pushrun` pushes to an isolated ref namespace and
  streams the build log back over the git sideband channel; the push ends
  with `CI_STATUS=` and a `CI_URL=` you can open.
- **Project trees and providers** — a project is a directory tree whose nodes
  are mounted from providers (git repos, runtimes, dictionaries, tools).
  Providers are data (YAML + bash scripts), replaceable without recompiling.
- **Two-phase provider lifecycle** — a shared, fingerprinted `warmup` artifact
  built once per parameter set, plus a per-instance `install`. A run never
  fails just because a provider is cold.
- **Linear CI pipelines** — build/test/configure steps plus one supervised
  background service with TCP/HTTP/command health checks and a leased port.
- **Named parallel instances** — run several instances of one project (per
  branch, per experiment) side by side; runs of one instance are serialized.
- **Single HTTP port** — git smart-HTTP push, REST API, SSE log streams, and
  the embedded web console all ride one listener. Token auth is on by default.
- **Thin client** — the `git pushrun` client is a few hundred lines of
  auditable POSIX shell, rendered and versioned by the server itself.

## Quickstart

Everything below runs on one machine against a throwaway data root and needs
no external network. It assumes a checkout of this repository (for the
example files), Go, git, bash, curl, and tar.

### 1. Get the binary

Build from source:

```sh
git clone https://github.com/bunnyzr/pushrun.git
cd pushrun
make build && export PATH="$PWD/dist:$PATH"
```

Or install a prebuilt binary instead:

```sh
curl -fsSL https://raw.githubusercontent.com/bunnyzr/pushrun/main/install.sh | sh
```

(install.sh downloads the right release asset for your OS/arch, verifies it
against the release checksums, and drops `pushrun` into `/usr/local/bin` or
`~/.local/bin`. Prebuilt binaries are attached to each
[GitHub release](https://github.com/bunnyzr/pushrun/releases); with Go
installed, `go install github.com/bunnyzr/pushrun/cmd/pushrun@latest` works
too.)

### 2. Start the daemon

```sh
export PUSHRUN_ROOT=/tmp/pushrun-demo
pushrun serve &
```

On first boot pushrun writes a default `config.yaml`, generates an auth token
(stored 0600 in the data root, printed once), and listens on `:8000`. Runtime
dependencies are just `git` and `bash`. Recover the token any time with:

```sh
TOKEN=$(pushrun token show)
```

### 3. Install the `git pushrun` client

```sh
curl -fsSL -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:8000/install.sh | sh -s -- --token "$TOKEN"
```

This stores the server and token in `~/.config/pushrun/config` (0600), caches
the client script, and installs the `git pushrun` alias.

### 4. Import the example provider and project

The example provider builds a fake runtime locally — no downloads:

```sh
# COPYFILE_DISABLE keeps macOS bsdtar from adding ._ AppleDouble entries
COPYFILE_DISABLE=1 tar -czf /tmp/example-static-runtime.tar.gz \
  -C examples/providers/example-static-runtime provider.yaml scripts
curl -fsSL -H "Authorization: Bearer $TOKEN" \
  --data-binary @/tmp/example-static-runtime.tar.gz \
  http://127.0.0.1:8000/api/v1/providers/import
```

The example project mounts the pushed source at `app/` and the example
runtime at `runtime/`. Its git mount declares the repo identity
`localhost/hello-web` — pushes to `<server>/git/localhost/hello-web.git`
trigger it:

```sh
COPYFILE_DISABLE=1 tar -czf /tmp/hello-web-project.tar.gz \
  -C examples/hello-web project.yaml
curl -fsSL -H "Authorization: Bearer $TOKEN" \
  --data-binary @/tmp/hello-web-project.tar.gz \
  http://127.0.0.1:8000/api/v1/projects/import
```

### 5. Push the demo service

This assumes a configured git identity (`git config user.name` /
`user.email`) so the demo commit succeeds.

```sh
cp -R examples/hello-web /tmp/hello-web-src
cd /tmp/hello-web-src
git init -b main
git add -A && git commit -m "hello pushrun"
# origin names the repo identity (host/path); the client pushes to the
# daemon's /git/<host>/<path>.git endpoint, so origin itself is never
# contacted.
git remote add origin https://localhost/hello-web.git
git pushrun
```

Build output streams back through the push, ending with:

```text
CI_STATUS=SUCCESS
CI_PORT=20000
CI_URL=http://127.0.0.1:20000
```

### 6. Use the running service

```sh
curl http://127.0.0.1:20000/healthz   # ok
```

Then explore: `git pushrun status`, `git pushrun logs ls`,
`git pushrun logs fetch hello-web.log -f`, `git pushrun rerun`,
`git pushrun --instance experiment` — or open the web console at
`http://127.0.0.1:8000/`.

### Multi-repo projects

A project can mount several git repos — one primary, the others satellite.
Pushing **any** of them triggers the project: the pushed repo's node gets
exactly the pushed commit, every other git node installs its snapshot (the
branch ref last pushed through pushrun, seeded once from the repo's source
on first use). When the pushed repo is only a satellite of the project (or
is shared by several projects), the push is rejected with the candidate
project names; disambiguate with:

```sh
git pushrun --project hello-web
```

## Web console

The daemon serves an embedded web console at `/` on its HTTP port — no
separate deployment. It manages the same entities as the CLI and API:

- **Projects** — browse and edit project trees, mounts, and pipelines.
- **Providers** — inspect providers and their scripts, and manage the warmup
  lifecycle.
- **Instances** — inspect instance state, trigger run actions, and follow
  build logs live.

When auth is enabled (the default), the console prompts for the token once —
the same token `pushrun token show` prints — and remembers it in the browser.

## Documentation

- [docs/design.md](docs/design.md) — the design reference: purpose,
  non-goals, domain model, warmup semantics, auth model, file formats.
- [docs/architecture.md](docs/architecture.md) — how the daemon works: git
  serving and hooks, the run engine, auth, logs, data-root layout.
- [docs/provider-contract.md](docs/provider-contract.md) — authoring
  providers: lifecycle phases, environment contract, warmup fingerprinting.
- [docs/deployment.md](docs/deployment.md) — installing and operating the
  daemon: data root, config, token, port pool.
- [docs/api.md](docs/api.md) — REST API, SSE streams, and the git endpoint.
- [examples/](examples/) — the hello-web demo and the example provider.

## License

[MIT](LICENSE)
