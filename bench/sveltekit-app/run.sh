#!/bin/sh
# Builds and starts the SvelteKit version of the benchmark tracker
# (bench/README.md) on $PORT (default 8082). Uploads up to 100 MB are
# allowed (adapter-node limits request bodies to 512 KB by default).
set -eu
cd "$(dirname "$0")"
[ -d node_modules ] || npm ci
npm run build >/dev/null
exec env PORT="${PORT:-8082}" BODY_SIZE_LIMIT="${BODY_SIZE_LIMIT:-100M}" node build
