// The checks inside VS Code (test/run.mjs): the language, the activation,
// and the client's answers from vela-lsp — a TypeScript hover with the Vela
// kind, the attributes of a child component, diagnostics after an edit.
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');
const vscode = require('vscode');

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function poll(what, fn) {
  for (let i = 0; i < 150; i++) {
    const value = await fn();
    if (value) return value;
    await sleep(200);
  }
  throw new Error(`timeout: ${what}`);
}

exports.run = async () => {
  const out = {};
  try {
    const root = vscode.workspace.workspaceFolders[0].uri.fsPath;
    const uri = vscode.Uri.file(path.join(root, 'src', 'App.vela'));
    const doc = await vscode.workspace.openTextDocument(uri);
    await vscode.window.showTextDocument(doc);
    assert.strictEqual(doc.languageId, 'vela');
    const extension = vscode.extensions.getExtension('govela.vela');
    await extension.activate();
    assert.ok(extension.isActive);
    const text = doc.getText();
    const at = (needle, delta) => doc.positionAt(text.indexOf(needle) + delta);

    out.hover = await poll('a TypeScript hover', async () => {
      const hovers = await vscode.commands.executeCommand('vscode.executeHoverProvider', uri, at('{count}', 2));
      const value = hovers.flatMap((h) => h.contents.map((c) => c.value ?? String(c))).join('\n');
      return value.includes('let count: number') ? value : null;
    });
    assert.match(out.hover, /derived/);

    const completion = await vscode.commands.executeCommand('vscode.executeCompletionItemProvider', uri, at(':title', 0));
    out.completion = completion.items.map((item) => (typeof item.label === 'string' ? item.label : item.label.label));
    for (const label of [':title', 'bind:value', '@on:close']) assert.ok(out.completion.includes(label), label);

    const edit = new vscode.WorkspaceEdit();
    edit.replace(uri, new vscode.Range(at('{count}', 1), at('{count}', 6)), 'cuont');
    await vscode.workspace.applyEdit(edit);
    const diagnostic = await poll('diagnostics', () => vscode.languages.getDiagnostics(uri).find((d) => d.message.includes('"cuont"')));
    out.diagnostic = `${diagnostic.source} ${diagnostic.range.start.line}:${diagnostic.range.start.character}-${diagnostic.range.end.character}`;
    assert.strictEqual(diagnostic.range.end.character - diagnostic.range.start.character, 5);
    out.ok = true;
  } catch (error) {
    out.error = String(error?.stack ?? error);
    throw error;
  } finally {
    fs.writeFileSync(process.env.VELA_RESULT, JSON.stringify(out, null, 2));
  }
};
