#!/bin/sh
# Runs the whole measurement series of task 058 on one host and records CPU
# steal time next to it (vmstat every 5 s): constant load, saturation
# curves, spike, soak, browser and build metrics. Results land in
# results/series-*.md and results/*.json. Run it with nohup on a server:
#
#   nohup shared/run-series.sh 1,2 > results/series.log 2>&1 &
set -u
cd "$(dirname "$0")/.."
CPUS=${1:-1,2}
SOAK_CPUS=${SOAK_CPUS:-1}
mkdir -p results
vmstat -n 5 > results/series-vmstat.log &
VMSTAT=$!
run() {
  name=$1
  shift
  echo "== $name $(date -u +%H:%M:%S)"
  node shared/runner.mjs "$@" > "results/series-$name.md" 2> "results/series-$name.err"
  echo "   exit $?"
}
run constant --cpus "$CPUS"
run step --profile step --cpus "$CPUS" --scenarios render-200,issue,board,api
run spike --profile spike --cpus "$CPUS" --scenarios render-200,issue
run soak --profile soak --duration 30m --cpus "$SOAK_CPUS" --scenarios journey
run browser --mode browser --cpus 1
run build --mode build
kill "$VMSTAT"
echo "== done $(date -u +%H:%M:%S)"
