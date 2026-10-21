#!/usr/bin/env bash
# example-static-runtime warmup phase.
#
# Builds the shared artifact into $PUSHRUN_PROVIDER_CACHE_DIR. pushrun runs
# this script at most once per (provider id, warmup-scoped parameters)
# fingerprint and shares the result across all instances; a .pushrun-ready
# marker is written by pushrun itself after this script exits 0, so a failed
# warmup never leaves a "warm" cache behind.
#
# A real provider would download and extract a toolchain or runtime tarball
# here. This example stays offline: it generates a tiny fake runtime.
set -euo pipefail

version="${PUSHRUN_PROVIDER_PARAM_VERSION:?missing required parameter: version}"
cache="$PUSHRUN_PROVIDER_CACHE_DIR"

echo "example-static-runtime: building runtime $version in $cache"
mkdir -p "$cache/bin"
cat >"$cache/bin/example-runtime" <<EOF
#!/bin/sh
echo "example-static-runtime $version"
EOF
chmod +x "$cache/bin/example-runtime"
echo "$version" >"$cache/VERSION"
echo "example-static-runtime: warmup done"
