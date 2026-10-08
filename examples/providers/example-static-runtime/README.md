# example-static-runtime

A sanitized, fully offline example of a pushrun provider (schema
`pushrun.provider/v1`), demonstrating both lifecycle phases:

- **warmup** (`scripts/warmup.sh`) — builds a fake "runtime" (an executable
  shell script) into the shared cache dir. pushrun runs it once per
  (provider, warmup parameters) fingerprint and shares the result across
  instances.
- **install** (`scripts/install.sh`) — copies the warmed artifact into an
  instance's mount node on every assembly (or symlinks it when the
  install-scoped `link` parameter is `true`).

Import it with:

```sh
COPYFILE_DISABLE=1 tar -czf example-static-runtime.tar.gz -C examples/providers/example-static-runtime provider.yaml scripts
curl -H "Authorization: Bearer $TOKEN" \
  --data-binary @example-static-runtime.tar.gz \
  http://127.0.0.1:8000/api/v1/providers/import
```

See [docs/provider-contract.md](../../../docs/provider-contract.md) for the
full provider authoring contract.
