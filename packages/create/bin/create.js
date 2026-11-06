#!/usr/bin/env node
// npm create @govela [directory] -- [--ssr | --app] [--module path]
//
// Creates a Vela application from one of the templates of this package
// (task 066): default (Vite), ssr (a Go server renders, Vite builds the
// client) or app (a Vela App with routes and Go loaders).
import { existsSync, mkdirSync, readdirSync, readFileSync, realpathSync, statSync, writeFileSync } from 'node:fs';
import { basename, dirname, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const manifest = JSON.parse(readFileSync(join(root, 'package.json'), 'utf8'));

const usage = `usage: npm create @govela [directory] -- [options]

options:
  --ssr            server rendering: a Go server renders, Vite builds the client
  --app            a Vela App: file-based routes with Go loaders
  --module path    the Go module path of --ssr and --app (example.com/<name>)
  --help           this text
`;

function fail(message) {
  process.stderr.write(`create-vela: ${message}\n`);
  process.exit(1);
}

export function parseArgs(argv) {
  const options = { template: 'default', directory: null, module: null };
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    switch (arg) {
      case '--ssr':
      case '--app': {
        const template = arg.slice(2);
        if (options.template !== 'default' && options.template !== template) throw new Error('--ssr and --app exclude each other');
        options.template = template;
        break;
      }
      case '--module':
        if (!argv[i + 1]) throw new Error('--module needs a Go module path');
        options.module = argv[++i];
        break;
      case '--help':
      case '-h':
        options.help = true;
        break;
      default:
        if (arg.startsWith('-')) throw new Error(`unknown option ${arg}`);
        if (options.directory) throw new Error(`a second directory ${arg}`);
        options.directory = arg;
    }
  }
  return options;
}

// packageName turns a directory name into a valid npm package name.
function packageName(dir) {
  const name = basename(dir).toLowerCase().replace(/[^a-z0-9._~-]+/g, '-').replace(/^[._-]+|-+$/g, '');
  return name || 'vela-app';
}

// Files that npm would rename or drop when publishing are stored with a
// leading underscore.
function outputName(name) {
  if (name === '_gitignore') return '.gitignore';
  if (name === '_github') return '.github';
  return name;
}

function copyTemplate(from, to, values) {
  mkdirSync(to, { recursive: true });
  for (const name of readdirSync(from)) {
    const source = join(from, name);
    const target = join(to, outputName(name));
    if (statSync(source).isDirectory()) {
      copyTemplate(source, target, values);
      continue;
    }
    let text = readFileSync(source, 'utf8');
    for (const [key, value] of Object.entries(values)) text = text.split(key).join(value);
    writeFileSync(target, text);
  }
}

export function create(options) {
  const directory = resolve(options.directory || 'vela-app');
  if (existsSync(directory) && readdirSync(directory).length > 0) {
    throw new Error(`${directory} is not empty`);
  }
  const name = packageName(directory);
  const module = options.module || `example.com/${name}`;
  if (!/^[A-Za-z0-9._~\/-]+$/.test(module)) throw new Error(`${module} is not a Go module path`);
  copyTemplate(join(root, 'templates', options.template), directory, {
    __NAME__: name,
    __MODULE__: module,
    __VERSION__: manifest.version,
    __APP_VERSION__: manifest.govela.app,
  });
  return { directory, name, template: options.template };
}

function main() {
  let options;
  try {
    options = parseArgs(process.argv.slice(2));
  } catch (err) {
    process.stderr.write(usage);
    fail(err.message);
  }
  if (options.help) {
    process.stdout.write(usage);
    return;
  }
  let result;
  try {
    result = create(options);
  } catch (err) {
    fail(err.message);
  }
  const dir = relative(process.cwd(), result.directory) || '.';
  const steps = [`cd ${dir}`, 'npm install'];
  if (result.template === 'ssr') steps.push('npx vela', 'go mod tidy', 'npm run dev:vela & npm run dev & npm run dev:server');
  else if (result.template === 'app') steps.push('npx vela-app gen && npx vela', 'go mod tidy', 'npm run dev');
  else steps.push('npm run dev');
  process.stdout.write(`Created ${result.name} (${result.template}) in ${result.directory}\n\nNext:\n${steps.map((s) => `  ${s}\n`).join('')}`);
}

// npm runs the command through a link in node_modules/.bin.
if (process.argv[1] && realpathSync(process.argv[1]) === realpathSync(fileURLToPath(import.meta.url))) main();
