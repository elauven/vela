#!/usr/bin/env node
// Load-test orchestrator of task 058 for one Linux host without Docker:
// pins PostgreSQL, the application and k6 to separate cores with taskset,
// reseeds the database before every application × CPU configuration, runs
// each k6 scenario after a warm-up, and samples the server's CPU time and
// RSS (whole process tree, so Node cluster workers count).
//
//   node shared/runner.mjs                       # constant load, every scenario
//   node shared/runner.mjs --apps vela --cpus 1 --scenarios render-200,board
//   node shared/runner.mjs --profile step --scenarios render-200,issue,api
//   node shared/runner.mjs --profile soak --duration 30m --cpus 1 --scenarios journey
//   node shared/runner.mjs --profile spike --scenarios render-200,issue
//   node shared/runner.mjs --mode browser        # shared/browser-metrics.mjs per app
//   node shared/runner.mjs --mode build          # build time and artifact sizes
//
// Profiles (shared/k6/lib.js): constant VUs; step 10 → 400 VUs; soak for
// --duration; spike 20 → 200 → 20 VUs. step, soak and spike report every
// time window (step hold, minute, 5 s) with the server's RSS in it.
//
// Results: results/<time>.json and Markdown tables on stdout.

import { execFileSync, spawn, spawnSync } from 'node:child_process';
import { gzipSync } from 'node:zlib';
import { mkdirSync, openSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseArgs } from 'node:util';

const bench = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const { values: args } = parseArgs({
  options: {
    apps: { type: 'string', default: 'vela,sveltekit' },
    cpus: { type: 'string', default: '1,4' },
    scenarios: { type: 'string', default: '' },
    profile: { type: 'string', default: 'constant' },
    mode: { type: 'string', default: 'load' },
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

// Uploads of both applications. The seed deletes the attachment rows, so the
// files of a previous block would stay on disk as orphans (task 058: 18 GB
// after three runs); they are removed together with the reseed.
const STORAGE = join(bench, 'storage');

const APPS = {
  vela: (n) => ({
    port: 8081,
    cwd: join(bench, 'vela-app'),
    cmd: [join(bench, 'vela-app/bin/server')],
    env: { APP_DIR: join(bench, 'vela-app'), PORT: '8081', GOMAXPROCS: String(n), STORAGE_DIR: STORAGE },
  }),
  sveltekit: (n) => ({
    port: 8082,
    cwd: join(bench, 'sveltekit-app'),
    cmd: n === 1 ? ['node', 'build/index.js'] : ['node', 'cluster.js'],
    env: { PORT: '8082', BODY_SIZE_LIMIT: '100M', WORKERS: String(n), NODE_ENV: 'production', STORAGE_DIR: STORAGE },
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
  mkdirSync(STORAGE, { recursive: true });
  for (const entry of readdirSync(STORAGE)) rmSync(join(STORAGE, entry), { recursive: true, force: true });
  execFileSync(join(bench, 'bin/seed'), ['-fixture', ''], { cwd: bench, stdio: 'ignore' });
}

function storageMB() {
  let bytes = 0;
  for (const entry of readdirSync(STORAGE)) bytes += statSync(join(STORAGE, entry)).size;
  return bytes / 1e6;
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

function k6(script, base, env, duration, vus, summary, profile = 'constant') {
  const envArgs = Object.entries({ BASE: base, PROFILE: profile, VUS: vus, DURATION: duration, ...env }).flatMap(([k, v]) => ['-e', `${k}=${v}`]);
  const argv = ['-c', CORES.k6, args.k6, 'run', '--quiet', '--no-color', ...envArgs, join(bench, 'shared/k6', script)];
  if (summary) argv.splice(5, 0, '--summary-export', summary);
  return new Promise((res) => {
    const child = spawn('taskset', argv, { stdio: ['ignore', 'ignore', 'inherit'] });
    child.on('exit', (code) => res(code));
  });
}

// Window of second t of a run, as shared/k6/lib.js tags requests, with its
// length in seconds and the VUs the profile runs in it.
const STEPS = [10, 50, 100, 200, 400];
function windowOf(profile, t) {
  if (profile === 'step') return t % 30 >= 5 ? `s${Math.floor(t / 30)}` : null;
  if (profile === 'soak') return `m${Math.floor(t / 60)}`;
  if (profile === 'spike') return `t${Math.floor(t / 5)}`;
  return null;
}
function windowInfo(profile, w) {
  const i = Number(w.slice(1));
  if (profile === 'step') return { seconds: 25, vus: STEPS[i] };
  if (profile === 'soak') return { seconds: 60, vus: Number(args.vus) };
  // spike: 20 VUs, ramp to 200 at 30–35 s, 200 until 65 s, back to 20 at 70 s
  const t = i * 5;
  return { seconds: 5, vus: t < 30 ? 20 : t < 35 ? '20→200' : t < 65 ? 200 : t < 70 ? '200→20' : 20 };
}

function windowRows(profile, metrics, rss) {
  const rows = [];
  for (const key of Object.keys(metrics)) {
    const m = /^http_req_duration\{w:([a-z]\d+)\}$/.exec(key);
    if (!m) continue;
    const w = m[1];
    const d = metrics[key];
    const count = metrics[`http_reqs{w:${w}}`]?.count || 0;
    if (!count) continue;
    const info = windowInfo(profile, w);
    rows.push({ w, vus: info.vus, rps: count / info.seconds, med: d.med, p95: d['p(95)'], p99: d['p(99)'],
      failed: metrics[`http_req_failed{w:${w}}`]?.value || 0, rssMB: (rss[w] || 0) / 1024 });
  }
  return rows.sort((a, b) => Number(a.w.slice(1)) - Number(b.w.slice(1)));
}

// Spike recovery: the baseline is the last 5 s before the spike (t5, at 20
// VUs; the first stage ramps up from 0). After the load drops (70 s), the
// seconds until a window is back within 1.5× of it without errors.
function spikeSummary(rows) {
  const at = (i) => rows.find((r) => r.w === `t${i}`);
  const baseline = at(5)?.p95 || 0;
  const during = rows.filter((r) => Number(r.w.slice(1)) >= 7 && Number(r.w.slice(1)) <= 12);
  const after = rows.filter((r) => Number(r.w.slice(1)) >= 14);
  const back = after.find((r) => r.p95 <= baseline * 1.5 && r.failed === 0);
  return {
    baselineP95: baseline,
    spikeP95: Math.max(0, ...during.map((r) => r.p95)),
    spikeFailed: Math.max(0, ...during.map((r) => r.failed)),
    recoverySeconds: back ? (Number(back.w.slice(1)) - 14) * 5 : null,
  };
}

const selected = args.scenarios ? SCENARIOS.filter((s) => args.scenarios.split(',').includes(s.name)) : SCENARIOS;
const resultsDir = join(bench, 'results');
mkdirSync(resultsDir, { recursive: true });
const stamp = new Date().toISOString().replace(/[:.]/g, '-');
const results = [];
const ticks = Number(execFileSync('getconf', ['CLK_TCK'], { encoding: 'utf8' }));

pinPostgres();

// ── Build mode: build time and artifact sizes ──────────────────────────────
function timed(cmd, argv, cwd, env = {}) {
  const started = Date.now();
  const r = spawnSync(cmd, argv, { cwd, env: { ...process.env, GOWORK: 'off', ...env }, stdio: ['ignore', 'ignore', 'pipe'] });
  if (r.status !== 0) throw new Error(`${cmd} ${argv.join(' ')}: ${r.stderr}`);
  return (Date.now() - started) / 1000;
}
function files(dir, test = () => true) {
  const out = [];
  const walk = (d) => {
    for (const e of readdirSync(d, { withFileTypes: true })) {
      const p = join(d, e.name);
      if (e.isDirectory()) walk(p);
      else if (test(p)) out.push(p);
    }
  };
  walk(dir);
  return out;
}
const sizeOf = (list) => list.reduce((n, p) => n + statSync(p).size, 0);
const gzipOf = (list) => list.reduce((n, p) => n + gzipSync(readFileSync(p), { level: 9 }).length, 0);

if (args.mode === 'build') {
  const vela = join(bench, 'vela-app');
  const svk = join(bench, 'sveltekit-app');
  const root = resolve(bench, '..');
  timed('go', ['build', '-o', join(vela, 'bin/vela'), '.'], root);
  rmSync(join(vela, 'out'), { recursive: true, force: true });
  const compile = timed(join(vela, 'bin/vela'), ['--ssr', '--mode=production', '--project', 'src', '--out', 'out'], vela);
  for (const f of readdirSync(join(vela, 'client'))) writeFileSync(join(vela, 'out/client', f), readFileSync(join(vela, 'client', f)));
  const bundle = timed(join(root, 'node_modules/.bin/esbuild'), ['out/client/board.js', 'out/client/issues.js', 'out/client/issue.js', '--bundle', '--format=esm', '--minify', '--outdir=static/dist', '--log-level=warning'], vela);
  rmSync(join(vela, 'bin/server'), { force: true });
  const gocold = timed('go', ['build', '-a', '-o', 'bin/server', './server'], vela);
  // With the build cache filled: the cost of relinking after a change.
  rmSync(join(vela, 'bin/server'), { force: true });
  const gowarm = timed('go', ['build', '-o', 'bin/server', './server'], vela);
  const svkBuild = timed('npm', ['run', 'build'], svk);
  const velaJS = files(join(vela, 'static/dist'), (p) => p.endsWith('.js'));
  const svkJS = files(join(svk, 'build/client'), (p) => p.endsWith('.js'));
  const prodDeps = execFileSync('npm', ['ls', '--omit=dev', '--all', '--parseable'], { cwd: svk, encoding: 'utf8' }).trim().split('\n').slice(1);
  const depBytes = prodDeps.reduce((n, d) => n + sizeOf(files(d, (p) => !p.includes('/node_modules/', d.length))), 0);
  const report = {
    vela: { compileSeconds: compile, bundleSeconds: bundle, goBuildColdSeconds: gocold, goBuildWarmSeconds: gowarm,
      serverBytes: statSync(join(vela, 'bin/server')).size, clientJSBytes: sizeOf(velaJS), clientJSGzip: gzipOf(velaJS), clientFiles: velaJS.length },
    sveltekit: { buildSeconds: svkBuild, buildDirBytes: sizeOf(files(join(svk, 'build'))), productionDepsBytes: depBytes,
      clientJSBytes: sizeOf(svkJS), clientJSGzip: gzipOf(svkJS), clientFiles: svkJS.length },
  };
  writeFileSync(join(resultsDir, `${stamp}-build.json`), JSON.stringify(report, null, 2));
  const kb = (n) => (n / 1024).toFixed(0) + ' KB';
  const mb = (n) => (n / 1048576).toFixed(1) + ' MB';
  console.log('| | Vela + Go | SvelteKit |\n| --- | ---: | ---: |');
  console.log(`| Build | vela ${report.vela.compileSeconds.toFixed(2)} s + esbuild ${report.vela.bundleSeconds.toFixed(2)} s + go build ${report.vela.goBuildWarmSeconds.toFixed(2)} s with the build cache (${report.vela.goBuildColdSeconds.toFixed(1)} s without) | vite build ${report.sveltekit.buildSeconds.toFixed(2)} s |`);
  console.log(`| Server artifact | ${mb(report.vela.serverBytes)} binary, no runtime needed | ${mb(report.sveltekit.buildDirBytes)} build/ + ${mb(report.sveltekit.productionDepsBytes)} production node_modules + Node.js |`);
  console.log(`| Client JavaScript (all pages) | ${kb(report.vela.clientJSBytes)}, ${kb(report.vela.clientJSGzip)} gzip, ${report.vela.clientFiles} files | ${kb(report.sveltekit.clientJSBytes)}, ${kb(report.sveltekit.clientJSGzip)} gzip, ${report.sveltekit.clientFiles} files |`);
  process.exit(0);
}

// ── Browser mode: shared/browser-metrics.mjs against each application ──────
if (args.mode === 'browser') {
  const cpus = Number(args.cpus.split(',')[0]);
  const runs = {};
  for (const name of args.apps.split(',')) {
    const app = APPS[name](cpus);
    const child = await startApp(app, CORES.app[cpus], 'ignore');
    try {
      for (const profile of ['desktop', 'mobile']) {
        console.error(`== ${name}, ${profile}`);
        const outFile = join(resultsDir, `${stamp}-browser-${name}-${profile}.json`);
        // Chromium and the script run beside the server, away from its core.
        const r = spawnSync('taskset', ['-c', '3-7', 'node', join(bench, 'shared/browser-metrics.mjs'), `http://localhost:${app.port}`, outFile, '--runs', '5', '--profile', profile], { stdio: ['ignore', 'ignore', 'inherit'] });
        if (r.status !== 0) throw new Error(`browser metrics failed for ${name} ${profile}`);
        runs[`${name}-${profile}`] = JSON.parse(readFileSync(outFile, 'utf8'));
      }
    } finally {
      await stopApp(child);
    }
  }
  const names = args.apps.split(',');
  const f0 = (x) => (x === undefined ? '—' : x.toFixed(0));
  for (const profile of ['desktop', 'mobile']) {
    console.log(`\n### Browser, ${profile} (median of 5 cold loads)\n`);
    console.log(`| Page | Metric | ${names.join(' | ')} |\n| --- | --- | ${names.map(() => '---:').join(' | ')} |`);
    for (const page of Object.keys(runs[`${names[0]}-${profile}`].pages)) {
      for (const [metric, label, scale] of [['ttfb', 'TTFB ms', 1], ['fcp', 'FCP ms', 1], ['lcp', 'LCP ms', 1], ['hydrated', 'hydrated ms', 1], ['tbt', 'TBT ms', 1], ['htmlBytes', 'HTML KB', 1024], ['jsBytes', 'JS KB', 1024], ['heapMB', 'JS heap MB', 1]]) {
        console.log(`| ${page} | ${label} | ${names.map((n) => f0(runs[`${n}-${profile}`].pages[page].cold[metric] / scale)).join(' | ')} |`);
      }
    }
    console.log(`\n| Interaction | ${names.join(' | ')} |\n| --- | ${names.map(() => '---:').join(' | ')} |`);
    for (const [k, label] of [['typeMs', 'typing in the board filter, slowest event ms'], ['dropMs', 'drag and drop, slowest event ms'], ['filterMs', 'list status filter until rows updated ms']]) {
      console.log(`| ${label} | ${names.map((n) => f0(runs[`${n}-${profile}`].interactionsMedian[k])).join(' | ')} |`);
    }
  }
  process.exit(0);
}

for (const name of args.apps.split(',')) {
  for (const n of args.cpus.split(',').map(Number)) {
    console.error(`== ${name}, ${n} CPU: reseeding`);
    reseed();
    const app = APPS[name](n);
    // Server output (errors under load) goes to results/<time>-<app>-<cpus>.log.
    const log = openSync(join(resultsDir, `${stamp}-${name}-${n}.log`), 'a');
    const spawnAt = Date.now();
    const child = await startApp(app, CORES.app[n], log);
    const coldStartMs = Date.now() - spawnAt;
    console.error(`   cold start ${coldStartMs} ms`);
    const base = `http://localhost:${app.port}`;
    try {
      for (const scenario of selected) {
        console.error(`   ${scenario.name}`);
        await k6(scenario.script, base, scenario.env, args.warmup, 16, null);
        const summary = join(resultsDir, `${stamp}-${name}-${n}-${scenario.name}.json`);
        const before = treeStats(child.pid);
        const started = Date.now();
        let maxRss = before.rss;
        const windowRss = {};
        const sampler = setInterval(() => {
          const rss = treeStats(child.pid).rss;
          maxRss = Math.max(maxRss, rss);
          const w = windowOf(args.profile, Math.floor((Date.now() - started) / 1000));
          if (w) windowRss[w] = Math.max(windowRss[w] || 0, rss);
        }, 500);
        const code = await k6(scenario.script, base, scenario.env, args.duration, args.vus, summary, args.profile);
        clearInterval(sampler);
        const after = treeStats(child.pid);
        const seconds = (Date.now() - started) / 1000;
        const m = JSON.parse(readFileSync(summary, 'utf8')).metrics;
        results.push({
          app: name, cpus: n, scenario: scenario.name, k6Exit: code,
          rps: m.http_reqs.rate, med: m.http_req_duration.med, p95: m.http_req_duration['p(95)'], p99: m.http_req_duration['p(99)'],
          failed: m.http_req_failed.value, cpu: (after.cpu - before.cpu) / ticks / seconds, rssMB: maxRss / 1024,
          receivedMBs: m.data_received.rate / 1e6, storageMB: storageMB(), coldStartMs,
          windows: windowRows(args.profile, m, windowRss),
        });
      }
    } finally {
      await stopApp(child);
    }
  }
}

// Leave no uploads behind; the sizes are in the results.
for (const entry of readdirSync(STORAGE)) rmSync(join(STORAGE, entry), { recursive: true, force: true });

writeFileSync(join(resultsDir, `${stamp}.json`), JSON.stringify({ args, cores: CORES, results }, null, 2));
const f = (x, d = 1) => x.toFixed(d);
console.log(`\nProfile ${args.profile}, VUs ${args.vus}, ${args.duration} per scenario after ${args.warmup} warm-up; results/${stamp}.json\n`);
const blocks = [...new Map(results.map((r) => [`${r.app}-${r.cpus}`, r])).values()];
console.log('Cold start (process start to the first /healthz answer): ' + blocks.map((r) => `${r.app} ${r.cpus} CPU ${r.coldStartMs} ms`).join(', ') + '\n');
if (args.profile !== 'constant') {
  for (const r of results) {
    console.log(`### ${r.scenario}, ${r.app}, ${r.cpus} CPU\n`);
    console.log('| Window | VUs | req/s | med ms | p95 ms | p99 ms | failed | RSS MB |');
    console.log('| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |');
    for (const w of r.windows) console.log(`| ${w.w} | ${w.vus} | ${f(w.rps, 0)} | ${f(w.med)} | ${f(w.p95)} | ${f(w.p99)} | ${f(w.failed * 100, 2)}% | ${f(w.rssMB, 0)} |`);
    if (args.profile === 'spike') {
      const sp = spikeSummary(r.windows);
      r.spike = sp;
      console.log(`\nbaseline p95 ${f(sp.baselineP95)} ms, worst p95 under the spike ${f(sp.spikeP95)} ms, worst error rate ${f(sp.spikeFailed * 100, 2)}%, back within 1.5× ${sp.recoverySeconds === null ? 'not within the run' : sp.recoverySeconds + ' s after the load dropped'}`);
    }
    if (args.profile === 'step') {
      const best = r.windows.reduce((a, b) => (b.rps > a.rps ? b : a), r.windows[0] || { rps: 0 });
      console.log(`\npeak ${f(best.rps, 0)} req/s at ${best.vus} VUs`);
    }
    console.log('');
  }
  writeFileSync(join(resultsDir, `${stamp}.json`), JSON.stringify({ args, cores: CORES, results }, null, 2));
}
console.log('Whole runs:\n');
console.log('| Scenario | App | CPUs | req/s | med ms | p95 ms | p99 ms | failed | CPU cores | max RSS MB | MB/s | storage MB |');
console.log('| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |');
for (const r of results.sort((a, b) => a.scenario.localeCompare(b.scenario) || a.cpus - b.cpus || a.app.localeCompare(b.app))) {
  console.log(`| ${r.scenario} | ${r.app} | ${r.cpus} | ${f(r.rps, 0)} | ${f(r.med)} | ${f(r.p95)} | ${f(r.p99)} | ${f(r.failed * 100, 2)}% | ${f(r.cpu, 2)} | ${f(r.rssMB, 0)} | ${f(r.receivedMBs)} | ${f(r.storageMB, 0)} |`);
}
