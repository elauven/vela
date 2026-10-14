#!/usr/bin/env node
// Load-test orchestrator of task 058 for one Linux host without Docker:
// pins PostgreSQL, the application and k6 to separate cores with taskset,
// reseeds the database before every application × CPU configuration, runs
// each k6 scenario after a warm-up, and samples the server's CPU time and
// RSS (whole process tree, so Node cluster workers count).
//
//   node shared/runner.mjs                       # everything
//   node shared/runner.mjs --apps vela --cpus 1 --scenarios render-200,board
//
// Results: results/<time>.json and a Markdown table on stdout.

import { execFileSync, spawn } from 'node:child_process';
import { mkdirSync, openSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseArgs } from 'node:util';

const bench = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const { values: args } = parseArgs({
  options: {
    apps: { type: 'string', default: 'vela,sveltekit' },
    cpus: { type: 'string', default: '1,4' },
    scenarios: { type: 'string', default: '' },
    vus: { type: 'string', default: '64' },
    duration: { type: 'string', default: '30s' },
    warmup: { type: 'string', default: '10s' },
    k6: { type: 'string', default: process.env.K6 || `${process.env.HOME}/go/bin/k6` },
  },
});

// Core layout on an 8-core host: PostgreSQL 0-1, application 2-5, k6 6-7.
const CORES = { db: '0,1', k6: '6,7', app: { 1: '2', 2: '2,3', 4: '2-5' } };

const SCENARIOS = [
  { name: 'render-50', script: 'render.js', env: { N: 50 } },
  { name: 'render-200', script: 'render.js', env: { N: 200 } },
  { name: 'render-500', script: 'render.js', env: { N: 500 } },
  { name: 'board', script: 'pages.js', env: { PAGE: 'board' } },
  { name: 'issues', script: 'pages.js', env: { PAGE: 'issues' } },
  { name: 'issue', script: 'pages.js', env: { PAGE: 'issue' } },
  { name: 'dashboard', script: 'pages.js', env: { PAGE: 'dashboard' } },
  { name: 'api', script: 'api.js', env: {} },
  { name: 'files-100k', script: 'files.js', env: { SIZE: 100 } },
  { name: 'journey', script: 'journey.js', env: { THINK: 0.2 } },
];

const APPS = {
  vela: (n) => ({
    port: 8081,
    cwd: join(bench, 'vela-app'),
    cmd: [join(bench, 'vela-app/bin/server')],
    env: { APP_DIR: join(bench, 'vela-app'), PORT: '8081', GOMAXPROCS: String(n) },
  }),
  sveltekit: (n) => ({
    port: 8082,
    cwd: join(bench, 'sveltekit-app'),
    cmd: n === 1 ? ['node', 'build/index.js'] : ['node', 'cluster.js'],
    env: { PORT: '8082', BODY_SIZE_LIMIT: '100M', WORKERS: String(n), NODE_ENV: 'production' },
  }),
};

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// PostgreSQL should run on CORES.db; start it with
// `taskset -c 0,1 pg_ctlcluster 18 main start` when its affinity cannot be
// changed afterwards (another user, no CAP_SYS_NICE).
function pinPostgres() {
  const pids = execFileSync('pgrep', ['-x', 'postgres'], { encoding: 'utf8' }).trim().split('\n').filter(Boolean);
  for (const pid of pids) {
    try {
      execFileSync('taskset', ['-acp', CORES.db, pid], { stdio: 'ignore' });
    } catch {
      const current = execFileSync('taskset', ['-cp', pid], { encoding: 'utf8' }).trim().split(': ').pop();
      if (current !== CORES.db.replace(',', '-') && current !== CORES.db) console.error(`warning: postgres ${pid} runs on ${current}, not ${CORES.db}`);
    }
  }
}

function reseed() {
  execFileSync(join(bench, 'bin/seed'), ['-fixture', ''], { cwd: bench, stdio: 'ignore' });
}

// Process tree of pid: total RSS (KB) and CPU time (clock ticks).
function treeStats(root) {
  const children = new Map();
  const stats = new Map();
  for (const entry of readdirSync('/proc')) {
    if (!/^\d+$/.test(entry)) continue;
    try {
      const stat = readFileSync(`/proc/${entry}/stat`, 'utf8');
      const fields = stat.slice(stat.lastIndexOf(')') + 2).split(' ');
      const ppid = Number(fields[1]);
      stats.set(Number(entry), { cpu: Number(fields[11]) + Number(fields[12]), rss: Number(fields[21]) * 4 });
      if (!children.has(ppid)) children.set(ppid, []);
      children.get(ppid).push(Number(entry));
    } catch {}
  }
  let rss = 0;
  let cpu = 0;
  const walk = (pid) => {
    const s = stats.get(pid);
    if (s) {
      rss += s.rss;
      cpu += s.cpu;
    }
    for (const c of children.get(pid) || []) walk(c);
  };
  walk(root);
  return { rss, cpu };
}

async function startApp(app, cores, log) {
  // A server left on the port would answer instead of the measured one.
  const busy = await fetch(`http://localhost:${app.port}/healthz`).then(() => true, () => false);
  if (busy) throw new Error(`port ${app.port} is already in use; stop that server first`);
  const child = spawn('taskset', ['-c', cores, ...app.cmd], { cwd: app.cwd, env: { ...process.env, ...app.env }, stdio: ['ignore', log, log] });
  for (let i = 0; i < 100; i++) {
    try {
      if ((await fetch(`http://localhost:${app.port}/healthz`)).ok) return child;
    } catch {}
    await sleep(100);
  }
  child.kill();
  throw new Error(`server on ${app.port} did not start`);
}

async function stopApp(child) {
  const done = new Promise((r) => child.once('exit', r));
  child.kill('SIGTERM');
  await Promise.race([done, sleep(3000)]);
  if (child.exitCode === null) child.kill('SIGKILL');
  // Cluster workers follow their primary; give them a moment.
  await sleep(500);
}

function k6(script, base, env, duration, vus, summary) {
  const envArgs = Object.entries({ BASE: base, PROFILE: 'constant', VUS: vus, DURATION: duration, ...env }).flatMap(([k, v]) => ['-e', `${k}=${v}`]);
  const argv = ['-c', CORES.k6, args.k6, 'run', '--quiet', '--no-color', ...envArgs, join(bench, 'shared/k6', script)];
  if (summary) argv.splice(5, 0, '--summary-export', summary);
  return new Promise((res) => {
    const child = spawn('taskset', argv, { stdio: ['ignore', 'ignore', 'inherit'] });
    child.on('exit', (code) => res(code));
  });
}

const selected = args.scenarios ? SCENARIOS.filter((s) => args.scenarios.split(',').includes(s.name)) : SCENARIOS;
const resultsDir = join(bench, 'results');
mkdirSync(resultsDir, { recursive: true });
const stamp = new Date().toISOString().replace(/[:.]/g, '-');
const results = [];
const ticks = Number(execFileSync('getconf', ['CLK_TCK'], { encoding: 'utf8' }));

pinPostgres();
for (const name of args.apps.split(',')) {
  for (const n of args.cpus.split(',').map(Number)) {
    console.error(`== ${name}, ${n} CPU: reseeding`);
    reseed();
    const app = APPS[name](n);
    // Server output (errors under load) goes to results/<time>-<app>-<cpus>.log.
    const log = openSync(join(resultsDir, `${stamp}-${name}-${n}.log`), 'a');
    const child = await startApp(app, CORES.app[n], log);
    const base = `http://localhost:${app.port}`;
    try {
      for (const scenario of selected) {
        console.error(`   ${scenario.name}`);
        await k6(scenario.script, base, scenario.env, args.warmup, 16, null);
        const summary = join(resultsDir, `${stamp}-${name}-${n}-${scenario.name}.json`);
        const before = treeStats(child.pid);
        const started = Date.now();
        let maxRss = before.rss;
        const sampler = setInterval(() => (maxRss = Math.max(maxRss, treeStats(child.pid).rss)), 500);
        const code = await k6(scenario.script, base, scenario.env, args.duration, args.vus, summary);
        clearInterval(sampler);
        const after = treeStats(child.pid);
        const seconds = (Date.now() - started) / 1000;
        const m = JSON.parse(readFileSync(summary, 'utf8')).metrics;
        results.push({
          app: name, cpus: n, scenario: scenario.name, k6Exit: code,
          rps: m.http_reqs.rate, med: m.http_req_duration.med, p95: m.http_req_duration['p(95)'], p99: m.http_req_duration['p(99)'],
          failed: m.http_req_failed.value, cpu: (after.cpu - before.cpu) / ticks / seconds, rssMB: maxRss / 1024,
          receivedMBs: m.data_received.rate / 1e6,
        });
      }
    } finally {
      await stopApp(child);
    }
  }
}

writeFileSync(join(resultsDir, `${stamp}.json`), JSON.stringify({ args, cores: CORES, results }, null, 2));
const f = (x, d = 1) => x.toFixed(d);
console.log(`\nVUs ${args.vus}, ${args.duration} per scenario after ${args.warmup} warm-up; results/${stamp}.json\n`);
console.log('| Scenario | App | CPUs | req/s | med ms | p95 ms | p99 ms | failed | CPU cores | max RSS MB | MB/s |');
console.log('| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |');
for (const r of results.sort((a, b) => a.scenario.localeCompare(b.scenario) || a.cpus - b.cpus || a.app.localeCompare(b.app))) {
  console.log(`| ${r.scenario} | ${r.app} | ${r.cpus} | ${f(r.rps, 0)} | ${f(r.med)} | ${f(r.p95)} | ${f(r.p99)} | ${f(r.failed * 100, 2)}% | ${f(r.cpu, 2)} | ${f(r.rssMB, 0)} | ${f(r.receivedMBs)} |`);
}
