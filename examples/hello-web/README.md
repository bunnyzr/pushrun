# hello-web

The pushrun demo service: a tiny Go HTTP server (stdlib `net/http` only)
with a `/healthz` endpoint. It takes the listen port as `-port`; the pushrun
pipeline in `project.yaml` passes the leased `$CI_PORT` through.

`project.yaml` is the ready-to-import pushrun project definition (schema
`pushrun.project/v1`). See the [Quickstart](../../README.md#quickstart) for
the full push-to-running flow, and
[docs/provider-contract.md](../../docs/provider-contract.md) for the provider
mounted at `runtime/`.
