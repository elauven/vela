#!/bin/sh
# Prepares a fresh Ubuntu server (26.04 LTS, 4 or more vCPUs) for the
# benchmark of task 058 with everything on one host (bench/README.md,
# "Server"). Run as root from a copy of the repository:
#
#   sudo bench/server-setup.sh
#
# Idempotent: a second run only updates what changed. Versions match the
# first measurements in bench/RESULTS.md.
set -eu

GO_VERSION=1.27.1
NODE_VERSION=24.21.0
K6_VERSION=v1.8.2
REPO=$(cd "$(dirname "$0")/.." && pwd)
export DEBIAN_FRONTEND=noninteractive

echo "== packages"
apt-get update -q
apt-get install -y -q build-essential ca-certificates curl git postgresql postgresql-contrib sysstat util-linux procps

echo "== Go $GO_VERSION"
if ! /usr/local/go/bin/go version 2>/dev/null | grep -q "go$GO_VERSION "; then
  rm -rf /usr/local/go
  curl -fsSL "https://go.dev/dl/go$GO_VERSION.linux-amd64.tar.gz" | tar -C /usr/local -xz
fi
export PATH=/usr/local/go/bin:$PATH

echo "== Node $NODE_VERSION"
if ! node --version 2>/dev/null | grep -q "v$NODE_VERSION"; then
  rm -rf "/usr/local/node-v$NODE_VERSION-linux-x64"
  curl -fsSL "https://nodejs.org/dist/v$NODE_VERSION/node-v$NODE_VERSION-linux-x64.tar.xz" | tar -C /usr/local -xJ
  for tool in node npm npx; do ln -sf "/usr/local/node-v$NODE_VERSION-linux-x64/bin/$tool" /usr/local/bin/$tool; done
fi

echo "== k6 $K6_VERSION"
if ! k6 version 2>/dev/null | grep -q "k6 $K6_VERSION"; then
  GOBIN=/usr/local/bin GOWORK=off go install "go.k6.io/k6@$K6_VERSION"
fi

echo "== PostgreSQL on CPU 0"
PGVER=$(ls /etc/postgresql | sort -n | tail -1)
mkdir -p "/etc/postgresql/$PGVER/main/conf.d"
cat > "/etc/postgresql/$PGVER/main/conf.d/bench.conf" <<EOF
# Benchmark of task 058: both applications use pools of 20 per process.
max_connections = 200
shared_buffers = 1GB
effective_cache_size = 4GB
EOF
mkdir -p /etc/systemd/system/postgresql@.service.d
cat > /etc/systemd/system/postgresql@.service.d/bench.conf <<EOF
[Service]
CPUAffinity=0
EOF
systemctl daemon-reload
systemctl restart "postgresql@$PGVER-main"
su postgres -c "psql -tAc \"SELECT 1 FROM pg_roles WHERE rolname='bench'\"" | grep -q 1 ||
  su postgres -c "psql -qc \"CREATE ROLE bench LOGIN PASSWORD 'bench'\""
su postgres -c "psql -tAc \"SELECT 1 FROM pg_database WHERE datname='bench'\"" | grep -q 1 ||
  su postgres -c "psql -qc 'CREATE DATABASE bench OWNER bench'"

echo "== system"
cat > /etc/sysctl.d/90-bench.conf <<EOF
net.core.somaxconn = 65535
net.ipv4.ip_local_port_range = 1024 65535
net.ipv4.tcp_tw_reuse = 1
EOF
sysctl -q --system
cat > /etc/security/limits.d/90-bench.conf <<EOF
* soft nofile 1048576
* hard nofile 1048576
EOF
for governor in /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor; do
  [ -w "$governor" ] && echo performance > "$governor" || true
done

echo "== repository tools (jsdom, Playwright, esbuild)"
cd "$REPO"
npm ci --no-audit --no-fund
npx playwright install --with-deps chromium || echo "warning: Chromium did not install; --mode browser will not run"

echo "== applications"
cd "$REPO/bench"
GOWORK=off go build -o bin/seed ./seed
vela-app/build.sh
(cd sveltekit-app && npm ci --no-audit --no-fund && npm run build)
echo "== seed"
bin/seed -fixture ''

cat <<EOF

Ready. PostgreSQL $PGVER runs on CPU 0; the runner puts the application on
CPU 1 (1 CPU) or 1-2 (2 CPUs) and k6 on CPU 3 on a 4-CPU host.

  cd $REPO/bench
  node shared/runner.mjs --cpus 1,2
  node shared/runner.mjs --profile step --cpus 1,2 --scenarios render-200,issue,board,api
  node shared/runner.mjs --profile spike --cpus 1,2 --scenarios render-200,issue
  node shared/runner.mjs --profile soak --duration 30m --cpus 1 --scenarios journey
  node shared/runner.mjs --mode browser --cpus 1
  node shared/runner.mjs --mode build
EOF
