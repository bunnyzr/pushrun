#!/usr/bin/env bash
# example-static-runtime install phase.
#
# Places the warmed artifact into one instance's mount node at
# $PUSHRUN_PROVIDER_TARGET_DIR. pushrun has already removed any previous
# content at the target and created its parent directories (mounting is
# exact replacement). This script runs on every assembly of every instance
# that mounts this provider.
set -euo pipefail

cache="$PUSHRUN_PROVIDER_CACHE_DIR"
target="$PUSHRUN_PROVIDER_TARGET_DIR"
link="${PUSHRUN_PROVIDER_PARAM_LINK:-false}"

echo "example-static-runtime: installing into $target (link=$link)"
if [ "$link" = "true" ]; then
	ln -s "$cache" "$target"
else
	cp -R "$cache" "$target"
	# The ready marker is pushrun-internal bookkeeping; keep it out of the
	# instance.
	rm -f "$target/.pushrun-ready"
fi
