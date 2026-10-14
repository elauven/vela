#!/bin/sh
# Builds the Vela + Go version of the benchmark tracker (bench/README.md):
# compiles the components with vela --ssr, bundles the client entries with
# esbuild, and builds the Go server into bin/server.
set -eu
cd "$(dirname "$0")"
ROOT=$(cd ../.. && pwd)
export GOWORK=off

go build -C "$ROOT" -o "$PWD/bin/vela" .
rm -rf out static/dist
bin/vela --ssr --mode=production --project src --out out --ssr-report out/report.json >/dev/null
cp client/*.js out/client/
"$ROOT/node_modules/.bin/esbuild" out/client/board.js out/client/issues.js out/client/issue.js \
  --bundle --format=esm --minify --outdir=static/dist --log-level=warning
go build -o bin/server ./server
echo "built bin/server; run: APP_DIR=$PWD bin/server"
