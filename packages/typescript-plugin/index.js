// @govela/typescript-plugin: `.vela` components in TypeScript's language
// server (task 085, docs/TYPESCRIPT.md TS-6.4). Editors load it through
// tsconfig.json `compilerOptions.plugins` or, in VS Code, the Vela
// extension; a `.ts` module that imports `./Card.vela` then sees the
// component's props, events, slots and bindable props.
//
// For `./Card.vela` TypeScript reads the virtual declaration file
// `Card.d.vela.ts`, which `vela compile --dts` writes (the declarations of
// `vela --dts`, TS-6.5). The plugin is CommonJS and synchronous, as
// TypeScript's server requires; it compiles a component once per
// modification of its file.
'use strict';

const { execFileSync } = require('node:child_process');
const fs = require('node:fs');
const path = require('node:path');

const VIRTUAL = '.d.vela.ts';
const platforms = ['linux-x64', 'linux-arm64', 'darwin-x64', 'darwin-arm64', 'win32-x64'];

// The vela binary: the plugin option `binary`, VELA_BINARY, the binary of
// @govela/compiler installed in the project, else `vela` on PATH.
function velaBinary(config, projectDir) {
  if (config.binary) return config.binary;
  if (process.env.VELA_BINARY) return process.env.VELA_BINARY;
  const platform = `${process.platform}-${process.arch}`;
  if (platforms.includes(platform)) {
    const file = `@govela/compiler-${platform}/bin/vela${process.platform === 'win32' ? '.exe' : ''}`;
    for (const from of [projectDir, __dirname]) {
      try {
        return require.resolve(file, { paths: [from] });
      } catch {}
    }
  }
  return 'vela';
}

// The declarations of a component that does not compile: a component of
// any props, so that its importers still resolve it.
function looseDeclarations(component) {
  const name = path.basename(component, '.vela');
  const id = /^[A-Za-z_$][\w$]*$/.test(name) ? name : '$$component';
  return `import type { Component } from '@govela/runtime';\ndeclare const ${id}: Component;\nexport { ${id} };\nexport default ${id};\n`;
}

function init(modules) {
  const ts = modules.typescript;

  function create(info) {
    const host = info.languageServiceHost;
    const config = info.config || {};
    const projectDir = info.project.getCurrentDirectory();
    const binary = velaBinary(config, projectDir);
    const log = (message) => info.project.projectService.logger.info(`[@govela/typescript-plugin] ${message}`);
    const cache = new Map(); // component → { mtime, text }

    const componentOf = (file) => (file.endsWith(VIRTUAL) ? file.slice(0, -VIRTUAL.length) + '.vela' : null);
    const mtimeOf = (file) => {
      try {
        return fs.statSync(file).mtimeMs;
      } catch {
        return -1;
      }
    };

    // The declaration file of component; the specifiers of the components
    // it imports stay `.vela`, so that they resolve through the plugin too.
    function declarations(component) {
      const mtime = mtimeOf(component);
      const cached = cache.get(component);
      if (cached && cached.mtime === mtime) return cached.text;
      let text = looseDeclarations(component);
      if (mtime >= 0) {
        try {
          const out = execFileSync(binary, ['compile', '--dts', '--path', component], { cwd: projectDir, encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'], maxBuffer: 64 << 20 });
          const result = JSON.parse(out);
          if (result.dts) text = result.dts;
        } catch (error) {
          // A component with errors exits 1 and still prints its result.
          try {
            const result = JSON.parse(String(error.stdout || ''));
            if (result.dts) text = result.dts;
          } catch {
            log(`vela compile --dts ${component}: ${error.message}`);
          }
        }
        const dir = path.dirname(component);
        text = text.replace(/(from\s+)(["'])(\.\.?\/[^"']+)\.js\2/g, (all, from, quote, spec) => {
          const base = path.resolve(dir, spec);
          return fs.existsSync(base + '.vela') && !fs.existsSync(base + '.js') && !fs.existsSync(base + '.ts') ? `${from}${quote}${spec}.vela${quote}` : all;
        });
      }
      cache.set(component, { mtime, text });
      return text;
    }

    const wrapOn = (target, name, fn) => {
      const original = target[name] && target[name].bind(target);
      if (original) target[name] = (...args) => fn(original, ...args);
    };
    const wrap = (name, fn) => wrapOn(host, name, fn);
    // tsserver keeps a script info per file, read through its server host:
    // the virtual declaration files must exist there too.
    const serverHost = info.serverHost;
    if (serverHost && !serverHost.__velaPlugin) {
      serverHost.__velaPlugin = true;
      wrapOn(serverHost, 'fileExists', (original, file) => (componentOf(file) ? fs.existsSync(componentOf(file)) : original(file)));
      wrapOn(serverHost, 'readFile', (original, file, encoding) => (componentOf(file) ? declarations(componentOf(file)) : original(file, encoding)));
      wrapOn(serverHost, 'getModifiedTime', (original, file) => (componentOf(file) ? fs.statSync(componentOf(file)).mtime : original(file)));
    }
    wrap('getScriptSnapshot', (original, file) => {
      const component = componentOf(file);
      if (!component) return original(file);
      const text = declarations(component);
      // In tsserver the project creates the file's script info (from the
      // server host above); it is reloaded when the component changed.
      const snapshot = original(file);
      const scriptInfo = info.project.getScriptInfo && info.project.getScriptInfo(file);
      if (snapshot && scriptInfo && snapshot.getText(0, snapshot.getLength()) !== text && scriptInfo.reloadFromFile) {
        scriptInfo.reloadFromFile();
        return original(file) || ts.ScriptSnapshot.fromString(text);
      }
      return snapshot || ts.ScriptSnapshot.fromString(text);
    });
    wrap('getScriptVersion', (original, file) => {
      const component = componentOf(file);
      return component ? String(mtimeOf(component)) : original(file);
    });
    wrap('fileExists', (original, file) => (componentOf(file) ? fs.existsSync(componentOf(file)) : original(file)));
    wrap('readFile', (original, file, encoding) => {
      const component = componentOf(file);
      return component ? declarations(component) : original(file, encoding);
    });
    wrap('getScriptKind', (original, file) => (componentOf(file) ? ts.ScriptKind.TS : original(file)));
    wrap('resolveModuleNameLiterals', (original, literals, containing, redirected, options, ...rest) => {
      const resolved = original(literals, containing, redirected, options, ...rest);
      return resolved.map((r, i) => {
        const name = literals[i].text;
        if (!name.endsWith('.vela') || !(name.startsWith('./') || name.startsWith('../'))) return r;
        const component = path.resolve(path.dirname(containing), name);
        if (!fs.existsSync(component)) return r;
        return { resolvedModule: { resolvedFileName: component.slice(0, -'.vela'.length) + VIRTUAL, extension: ts.Extension.Dts, isExternalLibraryImport: false } };
      });
    });
    log(`components through ${binary}`);

    // Go to definition of an imported component opens the .vela file, not
    // its virtual declarations.
    const service = info.languageService;
    const proxy = Object.create(null);
    for (const key of Object.keys(service)) {
      const method = service[key];
      proxy[key] = typeof method === 'function' ? (...args) => method.apply(service, args) : method;
    }
    const toComponent = (d) => {
      const component = componentOf(d.fileName);
      return component ? { ...d, fileName: component, textSpan: { start: 0, length: 0 }, contextSpan: undefined } : d;
    };
    proxy.getDefinitionAtPosition = (file, position) => service.getDefinitionAtPosition(file, position)?.map(toComponent);
    proxy.getDefinitionAndBoundSpan = (file, position) => {
      const result = service.getDefinitionAndBoundSpan(file, position);
      if (result?.definitions) result.definitions = result.definitions.map(toComponent);
      return result;
    };
    return proxy;
  }

  return { create };
}

module.exports = init;
