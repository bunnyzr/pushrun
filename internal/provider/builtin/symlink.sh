#!/bin/bash
# pushrun builtin "symlink" install script: replaces the target dir with a
# symlink to the source param. Runs with the contract env (see
# docs/provider-contract.md).
set -euo pipefail

: "${PUSHRUN_PROVIDER_TARGET_DIR:?target dir is required}"
: "${PUSHRUN_PROVIDER_PARAM_SOURCE:?source param is required}"

rm -rf "$PUSHRUN_PROVIDER_TARGET_DIR"
mkdir -p "$(dirname "$PUSHRUN_PROVIDER_TARGET_DIR")"
ln -s "$PUSHRUN_PROVIDER_PARAM_SOURCE" "$PUSHRUN_PROVIDER_TARGET_DIR"
