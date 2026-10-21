// The Vela bundler plugin (task 063): `.vela` modules to JavaScript and CSS
// through the compiler service (docs/TOOLING_PROTOCOL.md), for Vite,
// Rollup, Rolldown, esbuild, webpack and Rspack. The bundler resolves,
// bundles and serves; the plugin only compiles components.
import { startService } from '@govela/compiler';
import { createRequire } from 'node:module';
import path from 'node:path';
import { createUnplugin } from 'unplugin';

const runtimePackage = '@govela/runtime';
const runtimeEntries = [runtimePackage, `${runtimePackage}/blocks`, `${runtimePackage}/hydrate`];

// The CSS of a component is the virtual module <file>.vela + styleQuery,
// so the bundler's CSS pipeline processes, hashes and extracts it.
const styleQuery = '?vela&type=style&lang.css';
const styleId = /\.vela\?vela&type=style&lang\.css$/;
const componentId = /\.vela$/;
const runtimeAlias = /^vela(\/store)?$/;

// Bundlers that process an imported .css module without further setup;
// the others get the CSS injected by the component.
const cssBundlers = new Set(['vite', 'esbuild']);

function stripQuery(id) {
  const query = id.indexOf('?');
  return query < 0 ? id : id.slice(0, query);
}

function describe(d) {
  return (d.code ? `${d.code}: ` : '') + d.message + (d.suggestion ? `\n  ${d.suggestion}` : '');
}

function location(d, file) {
  // Bundlers count columns from zero.
  return { file, line: d.line, column: Math.max(0, d.column - 1) };
}

export const unplugin = createUnplugin((options = {}, meta) => {
  const css = options.css ?? (cssBundlers.has(meta.framework) ? 'external' : 'inject');
  if (css !== 'external' && css !== 'inject') {
    throw new Error(`@govela/unplugin: invalid css option ${JSON.stringify(css)} (want 'external' or 'inject')`);
  }
  let cwd = options.cwd ?? process.cwd();
  let service = null;
  const styles = new Map();

  function getService() {
    service ??= startService({ cwd, config: options.config, binary: options.binary });
    return service;
  }

  async function closeService() {
    const current = service;
    service = null;
    if (current) await (await current).close();
  }

  async function compile(file, source) {
    const result = await (await getService()).compile({
      path: file,
      source,
      options: {
        ...options.compilerOptions,
        css,
        sourcemap: true,
        runtimeImport: 'bare',
        componentImports: 'vela',
      },
    });
    if (result.ok && result.css !== undefined) styles.set(file, result.css);
    else styles.delete(file);
    return result;
  }

  return {
    name: 'vela',
    enforce: 'pre',

    resolveId: {
      filter: { id: /(^vela(\/store)?$|\?vela&type=style)/ },
      async handler(id, importer) {
        if (styleId.test(id)) {
          const base = importer ? path.dirname(stripQuery(importer)) : cwd;
          return path.resolve(base, stripQuery(id)) + styleQuery;
        }
        if (!runtimeAlias.test(id)) return null;
        // `vela` and `vela/store` in the application's own modules are the
        // runtime package, as in a build (Vite and esbuild use an alias).
        if (typeof this.resolve === 'function') {
          return this.resolve(runtimePackage, importer, { skipSelf: true });
        }
        return createRequire(importer ?? path.join(cwd, 'index.js')).resolve(runtimePackage);
      },
    },

    load: {
      filter: { id: styleId },
      async handler(id) {
        const file = stripQuery(id);
        if (!styles.has(file)) await compile(file);
        return styles.get(file) ?? '';
      },
    },

    transform: {
      filter: { id: componentId },
      async handler(code, id) {
        const file = stripQuery(id);
        const result = await compile(file, code);
        for (const d of result.diagnostics ?? []) {
          if (d.severity === 'warning') this.warn({ message: describe(d), id: file, loc: location(d, file) });
        }
        if (!result.ok) {
          const errors = (result.diagnostics ?? []).filter((d) => d.severity === 'error');
          if (errors.length === 0) {
            this.error({ message: result.error ?? 'compilation failed', id: file });
          } else {
            this.error({ message: errors.map(describe).join('\n'), id: file, loc: location(errors[0], file) });
          }
          // esbuild collects errors and reports them with a result.
          return { code: 'export {};' };
        }
        let js = result.js;
        if (styles.has(file)) {
          // Last, so the source map lines stay as generated.
          js += `\nimport ${JSON.stringify('./' + path.basename(file) + styleQuery)};\n`;
        }
        const map = result.map && { ...result.map, sources: [file], sourcesContent: [code] };
        return { code: js, map };
      },
    },

    buildEnd: closeService,

    vite: {
      config() {
        return {
          resolve: { alias: [{ find: runtimeAlias, replacement: runtimePackage }] },
          optimizeDeps: { include: runtimeEntries },
        };
      },
      configResolved(config) {
        cwd = options.cwd ?? config.root;
      },
    },

    esbuild: {
      onResolveFilter: /\?vela&type=style/,
      onLoadFilter: /\.vela(\?|$)/,
      config(buildOptions) {
        buildOptions.alias = { vela: runtimePackage, 'vela/store': runtimePackage, ...buildOptions.alias };
      },
    },
  };
});

export const vite = unplugin.vite;
export const rollup = unplugin.rollup;
export const rolldown = unplugin.rolldown;
export const esbuild = unplugin.esbuild;
export const webpack = unplugin.webpack;
export const rspack = unplugin.rspack;
export default unplugin;
