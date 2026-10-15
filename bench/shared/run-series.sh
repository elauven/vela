#!/bin/sh
# Runs the whole measurement series of task 058 on one host and records CPU
# steal time next to it (vmstat every 5 s): constant load, saturation
# curves, spike, soak, browser and build metrics. Results land in
# results/series-*.md and results/*.json. Run it with nohup on a server:
#
#   nohup shared/run-series.sh 1,2 > results/series.log 2>&1 &
#
# RUNNER_ARGS is passed to every runner call, for example a core layout:
#
#   RUNNER_ARGS='--cores-db 0,1 --cores-app 1=2 --cores-k6 3' \
#     nohup shared/run-series.sh 1 > results/series.log 2>&1 &
set -u
cd "$(dirname "$0")/.."
CPUS=${1:-1,2}
SOAK_CPUS=${SOAK_CPUS:-1}
SOAK_DURATION=${SOAK_DURATION:-30m}
RUNNER_ARGS=${RUNNER_ARGS:-}
mkdir -p results
vmstat -n 5 > results/series-vmstat.log &
VMSTAT=$!
run() {
  name=$1
  shift
  echo "== $name $(date -u +%H:%M:%S)"
  # shellcheck disable=SC2086 # RUNNER_ARGS is a list of options
  node shared/runner.mjs "$@" $RUNNER_ARGS > "results/series-$name.md" 2> "results/series-$name.err"
  echo "   exit $?"
}
run constant --cpus "$CPUS"
run step --profile step --cpus "$CPUS" --scenarios render-200,issue,board,api
run spike --profile spike --cpus "$CPUS" --scenarios render-200,issue
run soak --profile soak --duration "$SOAK_DURATION" --cpus "$SOAK_CPUS" --scenarios journey
run browser --mode browser --cpus 1
run build --mode build
kill "$VMSTAT"
echo "== done $(date -u +%H:%M:%S)"
