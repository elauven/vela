// Tokenizes components with the TextMate grammar of the VS Code extension
// (packages/vscode-vela), with the engines VS Code uses: vscode-textmate and
// vscode-oniguruma. The JavaScript and CSS grammars of VS Code are stubs: the
// check is that the grammar hands the right regions to them.
//
// Usage: node tools/vela-grammar-check.mjs <component.vela>...
// Prints {"files": {path: [[text, scopes]...]}} or {"error": "..."}.
import fs from 'node:fs';
import { createRequire } from 'node:module';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const require = createRequire(import.meta.url);
const textmate = require('vscode-textmate');
const oniguruma = require('vscode-oniguruma');

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const grammarFile = path.join(root, 'packages', 'vscode-vela', 'syntaxes', 'vela.tmLanguage.json');

async function main(files) {
  const wasm = fs.readFileSync(require.resolve('vscode-oniguruma/release/onig.wasm'));
  await oniguruma.loadWASM(wasm.buffer.slice(wasm.byteOffset, wasm.byteOffset + wasm.byteLength));
  const stub = (scopeName) => ({ scopeName, patterns: [{ match: '[A-Za-z_$][\\w$]*', name: 'variable.other.stub' }] });
  const registry = new textmate.Registry({
    onigLib: Promise.resolve({
      createOnigScanner: (patterns) => new oniguruma.OnigScanner(patterns),
      createOnigString: (s) => new oniguruma.OnigString(s),
    }),
    loadGrammar: async (scopeName) => {
      if (scopeName === 'source.vela') return textmate.parseRawGrammar(fs.readFileSync(grammarFile, 'utf8'), grammarFile);
      if (scopeName === 'source.js' || scopeName === 'source.css') return stub(scopeName);
      return null;
    },
  });
  const grammar = await registry.loadGrammar('source.vela');
  const out = {};
  for (const file of files) {
    let state = textmate.INITIAL;
    const tokens = [];
    for (const line of fs.readFileSync(file, 'utf8').split(/\r?\n/)) {
      const result = grammar.tokenizeLine(line, state);
      for (const t of result.tokens) {
        const text = line.slice(t.startIndex, t.endIndex);
        if (text.trim()) tokens.push([text, t.scopes.join(' ')]);
      }
      state = result.ruleStack;
    }
    out[file] = tokens;
  }
  return { files: out };
}

try {
  console.log(JSON.stringify(await main(process.argv.slice(2))));
} catch (error) {
  console.log(JSON.stringify({ error: String(error?.stack ?? error) }));
}
