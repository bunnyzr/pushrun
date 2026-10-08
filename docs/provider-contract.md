# The pushrun provider contract

A **provider** supplies the content of a mount node in a project tree and
brings it to life. Providers are data — a YAML descriptor plus bash
scripts — replaceable without recompiling the daemon. This document is the
authoring contract; `examples/providers/example-static-runtime/` is a
complete, offline reference implementation.

## Definition format (`pushrun.provider/v1`)

A provider lives in a directory containing `provider.yaml` and its scripts:

```yaml
schema: pushrun.provider/v1
id: example-static-runtime
name: Example Static Runtime
description: Builds a fake runtime in warmup and installs it per instance.
warmup: scripts/warmup.sh      # optional phase
install: scripts/install.sh    # required phase
parameters:
  - id: version
    label: Runtime version
    type: string
    scope: warmup
    required: true
    default: "1.0.0"
  - id: link
    label: Symlink instead of copy
    type: boolean
    scope: install
    default: "false"
```

Rules enforced at validation time:

- `id` and `install` are required; `warmup` is optional.
- Script paths must be relative, clean, and contained in the provider
  directory. The YAML declares which script serves which phase; script file
  names are not part of the contract.
- On disk a provider lives at `<root>/providers/<id>/`; scripts run with
  that directory as their working directory.
- The ids `git` and `symlink` are reserved for the platform builtins and can
  never be shadowed.

## Lifecycle phases

| Phase     | Required | Semantics |
|-----------|----------|-----------|
| `warmup`  | optional | Build a reusable **shared artifact** (download, extract, compile, patch). Runs at most once per parameter set; the result is shared across instances. |
| `install` | required | Place content into one instance's target node (copy, symlink, generate, initialize). Runs on every assembly of every instance that mounts the provider. |

Scripts are Bash, executed with `/bin/bash`. Success and failure are the
exit code; pushrun never scans script content. Script stdout/stderr are
streamed into the build log (with `secret` parameter values redacted).

### Warmup semantics (`EnsureWarmed`)

A run never fails merely because a provider is cold. The run engine calls
`EnsureWarmed` for every provider referenced by the project tree before any
install happens:

- The shared artifact directory is
  `provider-data/<id>-<fingerprint>/`, where the fingerprint is the SHA-256
  of the provider id plus its canonicalized **warmup-scoped** parameters
  (sorted `k=v` pairs). Change a warmup parameter, get a new directory.
- A `.pushrun-ready` marker inside the directory records the fingerprint.
  Marker present and matching → the warmup is skipped (the normal,
  zero-cost path).
- Marker missing or stale → the warmup script runs in a sibling staging
  directory, the marker is written on success, and the staging directory is
  atomically renamed into place. A failed warmup leaves neither marker nor
  cache directory behind.
- Warmups are serialized per (provider, fingerprint) with a file lock:
  concurrent runs share one warmup and never corrupt the artifact dir.
- A warmup failure stops the run before any install, naming the provider
  and phase.
- The manual warmup endpoints (`POST /api/v1/providers/{id}/warmup`,
  `POST /api/v1/projects/{name}/warmup`) call the exact same `EnsureWarmed`;
  there is no behavioral fork between manual and automatic warmup.
- A provider with no `warmup` script gets an empty shared artifact dir and
  its marker — its install phase still receives a valid cache dir.

### Install semantics

On every assembly, pushrun removes the mount node (exact replacement),
creates its parent directories, and runs the install script with the target
path in the environment. The script decides whether to copy, symlink,
extract, or generate. Scripts should be idempotent and treat the target
path as not yet existing.

## Environment contract

The executor passes context via environment variables:

| Variable | Warmup | Install | Meaning |
|----------|--------|---------|---------|
| `PUSHRUN_PROVIDER_CACHE_DIR` | staging dir to fill | the warmed shared artifact dir | warmup output / install input |
| `PUSHRUN_PROVIDER_TARGET_DIR` | *(empty)* | the instance's mount node path | install output |
| `PUSHRUN_PROVIDER_PHASE` | `warmup` | `install` | the running phase |
| `PUSHRUN_PROVIDER_PARAM_<ID>` | one per warmup-scoped param | one per install-scoped param | resolved parameter value, id upper-cased |

Parameter resolution: the value from the project tree's mount `params` wins;
otherwise the declared `default` is used. Missing `required` parameters fail
before any script runs.

During warmup, `PUSHRUN_PROVIDER_CACHE_DIR` points at a staging directory
that is promoted to the real cache dir only on success — write freely, a
crash leaves no half-built artifact behind.

## Parameter declaration schema

Parameters are self-declared in the provider YAML; the planned web console
will generate the editor form from this declaration, so nothing is
hard-coded per provider.

| Field      | Values | Notes |
|------------|--------|-------|
| `id`       | unique per provider | becomes `PUSHRUN_PROVIDER_PARAM_<ID>` (upper-cased) |
| `label`    | free text | shown in the UI |
| `type`     | `string` \| `number` \| `boolean` \| `select` | |
| `options`  | list of strings | required (and non-empty) for `select` params; rejected on every other type |
| `scope`    | `warmup` \| `install` | determines which phase receives it and whether it joins the warmup fingerprint |
| `required` | bool | missing values fail the phase before the script runs |
| `default`  | string | used when the mount does not supply a value |
| `secret`   | bool | masked in UI/API/logs; a secret parameter cannot have a default |

## Built-in providers

`git` and `symlink` are compiled into the daemon and behave exactly like
external providers with the same environment contract:

- **`git`** — warmup ensures the daemon's bare repo for the node's repo
  identity holds a **snapshot** of the declared branch: if
  `refs/pushrun/for/<branch>` already exists the repo is warm and left
  untouched (snapshot content only changes when someone pushes that branch
  through pushrun); only a cold repo is seeded, with a single fetch of the
  branch head from the `repo` source. Install checks out `commit` (injected
  automatically with the triggering commit when this node triggered the run)
  or, for every other node, the snapshot ref — a run triggered by repo A
  never pulls newer content of repo B. Parameters: `repo` (warmup,
  required), `branch` (warmup, required), `commit` (install; injected for
  the triggering node). The `repo` param accepts a full URL
  (`https://...`, `ssh://...`), a local absolute path, or a `host/path`
  shorthand; all forms normalize to the same `host/path` repo identity, and
  the shorthand is expanded into a source URL with the server's `git.scheme`
  config (default `https`) for the one-time seed fetch. Credentials for
  private sources are the deployer's business (SSH keys or a credential
  helper on the daemon host). Pushes to the repo identity trigger the
  project per the matching rules in
  [design.md](design.md#triggering-a-run); any git node — primary or not —
  can be the trigger.
- **`symlink`** — install links an existing local directory into the mount
  node. Parameter: `source` (install, required).

Builtins will appear in the planned web console's provider picker but cannot
be exported, deleted, or shadowed.

## Distribution

Providers move between deployments as `.tar.gz` bundles (`provider.yaml` at
the bundle root plus script files) via `GET /api/v1/providers/{id}/export`
and `POST /api/v1/providers/import`. Import validates the descriptor and the
full script set before anything is written, so a failed import never leaves
a partial provider behind.
