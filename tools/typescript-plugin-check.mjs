// Runs @govela/typescript-plugin in a TypeScript language service, as
// tsserver loads it, over a project with a .ts module that imports
// components (TypeScriptPluginTest, task 085). Prints JSON: the semantic
// diagnostics of main.ts, the quick info of the imported component, the
// completions of its props and its definition.
//
//   node tools/typescript-plugin-check.mjs <typescript dir> <project dir> <vela binary>
import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';

const [tsDir, root, binary] = process.argv.slice(2);
const require = createRequire(import.meta.url);
const ts = require(path.join(tsDir, 'lib', 'typescript.js'));
const plugin = require(path.resolve('packages/typescript-plugin/index.js'));

const { config } = ts.readConfigFile(path.join(root, 'tsconfig.json'), ts.sys.readFile);
const parsed = ts.parseJsonConfigFileContent(config, ts.sys, root);
const host = {
  getCompilationSettings: () => parsed.options,
  getScriptFileNames: () => parsed.fileNames,
  getScriptVersion: (file) => String(fs.existsSync(file) ? fs.statSync(file).mtimeMs : 0),
  getScriptSnapshot: (file) => (fs.existsSync(file) ? ts.ScriptSnapshot.fromString(fs.readFileSync(file, 'utf8')) : undefined),
  getCurrentDirectory: () => root,
  getDefaultLibFileName: (o) => ts.getDefaultLibFilePath(o),
  fileExists: ts.sys.fileExists,
  readFile: ts.sys.readFile,
  readDirectory: ts.sys.readDirectory,
  directoryExists: ts.sys.directoryExists,
  getDirectories: ts.sys.getDirectories,
  realpath: ts.sys.realpath,
  resolveModuleNameLiterals: (literals, containing, _redirected, options) =>
    literals.map((l) => ts.resolveModuleName(l.text, containing, options, ts.sys)),
};
const service = ts.createLanguageService(host, ts.createDocumentRegistry());
const logged = [];
const info = {
  languageServiceHost: host,
  languageService: service,
  project: { getCurrentDirectory: () => root, projectService: { logger: { info: (m) => logged.push(m) } } },
  config: { name: '@govela/typescript-plugin', binary },
};
const ls = plugin({ typescript: ts }).create(info);

const main = path.join(root, 'src', 'main.ts');
const text = fs.readFileSync(main, 'utf8');
const diagnostics = ls.getSemanticDiagnostics(main).map((d) => ({
  code: d.code,
  at: text.slice(d.start, d.start + d.length),
  message: ts.flattenDiagnosticMessageText(d.messageText, '\n'),
}));
const quick = ls.getQuickInfoAtPosition(main, text.indexOf('Button,'));
const completionAt = text.indexOf('/*props*/') + '/*props*/'.length;
const completion = ls.getCompletionsAtPosition(main, completionAt, {});
const definition = ls.getDefinitionAndBoundSpan(main, text.indexOf('Button,', text.indexOf('mount(')))?.definitions?.map((d) => d.fileName) ?? [];
console.log(JSON.stringify({
  typescript: ts.version,
  diagnostics,
  quickInfo: quick ? ts.displayPartsToString(quick.displayParts) : '',
  completion: (completion?.entries ?? []).map((e) => e.name),
  definition,
  logged,
}));
