// Runs the built SvelteKit server in WORKERS processes sharing one port
// (node:cluster), for the multi-CPU benchmark configuration (task 058).
// adapter-node itself is single-process.
import cluster from 'node:cluster';
import { availableParallelism } from 'node:os';

const workers = Number(process.env.WORKERS || availableParallelism());

if (cluster.isPrimary) {
  for (let i = 0; i < workers; i++) cluster.fork();
  cluster.on('exit', (worker, code) => {
    if (code !== 0) console.error(`worker ${worker.process.pid} exited with ${code}`);
  });
} else {
  await import('./build/index.js');
}
