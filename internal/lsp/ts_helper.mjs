// The TypeScript helper of vela-lsp (task 065c, docs/EDITORS.md). vela-lsp
// embeds this file and runs it with node: one JSON request per line on
// stdin, one response per line on stdout. It knows nothing about Vela: it
// runs TypeScript's language service over the virtual documents vela-lsp
// sends (`App.vela.js` for `App.vela`) and resolves `./X.vela` imports to
// them. Positions are UTF-16 offsets of a file's text.
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import { createRequire } from 'node:module';
import path from 'node:path';
import { createInterface } from 'node:readline';

const send = (message) => process.stdout.write(JSON.stringify(message) + '\n');

// The TypeScript that has the language service API (5.x and 6.x; 7.x has
// none yet): the editor's, the workspace's, then the global one.
function loadTypeScript(root, tsdk) {
  const tried = [];
  const attempt = (label, load) => {
    try {
      const ts = load();
      if (ts && typeof ts.createLanguageService === 'function') return ts;
      tried.push(`${label}: TypeScript ${ts?.version ?? '?'} has no language service API`);
    } catch (error) {
      tried.push(`${label}: ${error.code === 'MODULE_NOT_FOUND' ? 'not found' : error.message}`);
    }
    return null;
  };
  const fromDir = (dir) => createRequire(path.join(dir, 'noop.js'));
  let ts = null;
  if (tsdk) {
    // A typescript/lib directory, as the VS Code setting typescript.tsdk
    // names it, or the package directory.
    ts = attempt(tsdk, () => {
      for (const file of [path.join(tsdk, 'typescript.js'), path.join(tsdk, 'lib', 'typescript.js')]) {
        if (fs.existsSync(file)) return fromDir(tsdk)(file);
      }
      throw new Error('no typescript.js');
    });
  }
  ts ??= attempt('the workspace', () => fromDir(root)('typescript'));
  ts ??= attempt('the global packages', () => {
    const global = execFileSync('npm', ['root', '-g'], { encoding: 'utf8', shell: process.platform === 'win32', stdio: ['ignore', 'pipe', 'ignore'] }).trim();
    return fromDir(global)('typescript');
  });
  return { ts, tried };
}

let ts;
let service;
let root;
let envPath;
let envText;
let options;
const files = new Map(); // virtual path → { text, version }

function compilerOptions() {
  const defaults = {
    allowJs: true,
    checkJs: false,
    // TypeScript 6 is strict by default; a JavaScript project without a
    // configuration is checked (with // @ts-check) as TypeScript 5 did.
    strict: false,
    noEmit: true,
    skipLibCheck: true,
    target: ts.ScriptTarget.ESNext,
    module: ts.ModuleKind.ESNext,
    moduleResolution: ts.ModuleResolutionKind.Bundler ?? ts.ModuleResolutionKind.NodeJs,
    lib: ['lib.esnext.d.ts', 'lib.dom.d.ts', 'lib.dom.iterable.d.ts'],
  };
  for (const name of ['jsconfig.json', 'tsconfig.json']) {
    const file = path.join(root, name);
    if (!fs.existsSync(file)) continue;
    const { config } = ts.readConfigFile(file, ts.sys.readFile);
    const parsed = ts.parseJsonConfigFileContent(config ?? {}, ts.sys, root);
    return { ...defaults, ...parsed.options, allowJs: true, noEmit: true };
  }
  return defaults;
}

const textOf = (file) => (file === envPath ? envText : files.get(file)?.text);

function resolveModule(name, containing, opts) {
  if (name.startsWith('./') || name.startsWith('../')) {
    let target = path.resolve(path.dirname(containing), name);
    if (name.endsWith('.vela')) target += '.js';
    else if (name.endsWith('.js') && !files.has(target) && !ts.sys.fileExists(target) && files.has(target.slice(0, -3) + '.vela.js')) target = target.slice(0, -3) + '.vela.js';
    if (files.has(target)) return { resolvedFileName: target, extension: ts.Extension.Js, isExternalLibraryImport: false };
  }
  return ts.resolveModuleName(name, containing, opts, ts.sys).resolvedModule;
}

function start(params) {
  root = params.root;
  const loaded = loadTypeScript(root, params.tsdk);
  if (!loaded.ts) return { error: `no TypeScript with a language service API (5.x or 6.x): ${loaded.tried.join('; ')}` };
  ts = loaded.ts;
  envPath = path.join(root, '__vela_env__.d.ts');
  envText = params.env;
  options = compilerOptions();
  const host = {
    getCompilationSettings: () => options,
    getScriptFileNames: () => [envPath, ...files.keys()],
    getScriptVersion: (file) => {
      if (file === envPath) return '0';
      const open = files.get(file);
      if (open) return String(open.version);
      try { return String(fs.statSync(file).mtimeMs); } catch { return '0'; }
    },
    getScriptSnapshot: (file) => {
      const text = textOf(file) ?? ts.sys.readFile(file);
      return text === undefined ? undefined : ts.ScriptSnapshot.fromString(text);
    },
    getCurrentDirectory: () => root,
    getDefaultLibFileName: (o) => ts.getDefaultLibFilePath(o),
    fileExists: (file) => textOf(file) !== undefined || ts.sys.fileExists(file),
    readFile: (file, encoding) => textOf(file) ?? ts.sys.readFile(file, encoding),
    readDirectory: ts.sys.readDirectory,
    directoryExists: ts.sys.directoryExists,
    getDirectories: ts.sys.getDirectories,
    realpath: ts.sys.realpath,
    useCaseSensitiveFileNames: () => ts.sys.useCaseSensitiveFileNames,
    resolveModuleNameLiterals: (literals, containing, _redirected, opts) => literals.map((literal) => ({ resolvedModule: resolveModule(literal.text, containing, opts) })),
  };
  service = ts.createLanguageService(host, ts.createDocumentRegistry());
  return { typescript: ts.version, checkJs: Boolean(options.checkJs) };
}

// span is a text span of file with its line and character positions.
function span(file, start, length) {
  const out = { file, start, length };
  const source = service.getProgram()?.getSourceFile(file);
  if (source) {
    const s = source.getLineAndCharacterOfPosition(start);
    const e = source.getLineAndCharacterOfPosition(start + length);
    Object.assign(out, { line: s.line, character: s.character, endLine: e.line, endCharacter: e.character });
  }
  return out;
}

const text = (parts) => (parts ? ts.displayPartsToString(parts) : '');

function documentation(doc, tags) {
  const lines = [text(doc)];
  for (const tag of tags ?? []) lines.push(`*@${tag.name}*${tag.text ? ' — ' + text(tag.text) : ''}`);
  return lines.filter(Boolean).join('\n\n');
}

const methods = {
  start,
  update({ files: changed = [], remove = [] }) {
    for (const { path: file, text: content } of changed) {
      files.set(file, { text: content, version: (files.get(file)?.version ?? 0) + 1 });
    }
    for (const file of remove) files.delete(file);
    return {};
  },
  completion({ path: file, position }) {
    const info = service.getCompletionsAtPosition(file, position, { includeCompletionsWithInsertText: true, includeCompletionsWithSnippetText: false });
    if (!info) return { entries: [] };
    return {
      isMember: info.isMemberCompletion,
      entries: info.entries.map((e) => ({
        name: e.name,
        kind: e.kind,
        sortText: e.sortText,
        insertText: e.insertText,
        source: e.source,
        replacement: e.replacementSpan && { start: e.replacementSpan.start, length: e.replacementSpan.length },
      })),
    };
  },
  completionDetails({ path: file, position, name, source }) {
    const d = service.getCompletionEntryDetails(file, position, name, {}, source || undefined, {}, undefined);
    return d ? { detail: text(d.displayParts), documentation: documentation(d.documentation, d.tags) } : {};
  },
  quickInfo({ path: file, position }) {
    const info = service.getQuickInfoAtPosition(file, position);
    if (!info) return {};
    return { display: text(info.displayParts), documentation: documentation(info.documentation, info.tags), start: info.textSpan.start, length: info.textSpan.length };
  },
  definition({ path: file, position }) {
    return (service.getDefinitionAtPosition(file, position) ?? []).map((d) => span(d.fileName, d.textSpan.start, d.textSpan.length));
  },
  references({ path: file, position }) {
    return (service.getReferencesAtPosition(file, position) ?? []).map((r) => span(r.fileName, r.textSpan.start, r.textSpan.length));
  },
  rename({ path: file, position }) {
    const info = service.getRenameInfo(file, position, { allowRenameOfImportPath: false });
    if (!info.canRename) return { error: info.localizedErrorMessage };
    const locations = service.findRenameLocations(file, position, false, false, { providePrefixAndSuffixTextForRename: true }) ?? [];
    return { locations: locations.map((l) => ({ ...span(l.fileName, l.textSpan.start, l.textSpan.length), prefix: l.prefixText ?? '', suffix: l.suffixText ?? '' })) };
  },
  signatureHelp({ path: file, position }) {
    const help = service.getSignatureHelpItems(file, position, {});
    if (!help) return {};
    return {
      activeSignature: help.selectedItemIndex,
      activeParameter: help.argumentIndex,
      signatures: help.items.map((item) => {
        const params = item.parameters.map((p) => ({ label: text(p.displayParts), documentation: text(p.documentation) }));
        return {
          label: text(item.prefixDisplayParts) + params.map((p) => p.label).join(text(item.separatorDisplayParts)) + text(item.suffixDisplayParts),
          documentation: text(item.documentation),
          parameters: params,
        };
      }),
    };
  },
  diagnostics({ path: file }) {
    return service.getSemanticDiagnostics(file).map((d) => ({
      start: d.start ?? 0,
      length: d.length ?? 0,
      message: ts.flattenDiagnosticMessageText(d.messageText, '\n'),
      category: d.category === ts.DiagnosticCategory.Error ? 'error' : d.category === ts.DiagnosticCategory.Warning ? 'warning' : 'hint',
      code: d.code,
    }));
  },
};

createInterface({ input: process.stdin }).on('line', (line) => {
  let request;
  try {
    request = JSON.parse(line);
  } catch (error) {
    send({ error: `invalid request: ${error.message}` });
    return;
  }
  const { id, method, params } = request;
  try {
    const method_ = methods[method];
    if (!method_) throw new Error(`unknown method ${method}`);
    if (method !== 'start' && !service) throw new Error('not started');
    const result = method_(params ?? {});
    send(result?.error && method === 'start' ? { id, error: result.error } : { id, result });
  } catch (error) {
    send({ id, error: String(error?.stack ?? error) });
  }
});
