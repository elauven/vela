// The checks inside VS Code (test/run.mjs): the language, the activation,
// and the client's answers from vela-lsp — a TypeScript hover with the Vela
// kind, the attributes of a child component, diagnostics after an edit —
// then a TypeScript component and a .ts module that imports it (task 085):
// hover, diagnostics, definition and rename in both, the .ts module through
// @govela/typescript-plugin in VS Code's TypeScript.
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

    // A TypeScript component.
    const typedUri = vscode.Uri.file(path.join(root, 'src', 'Typed.vela'));
    const typed = await vscode.workspace.openTextDocument(typedUri);
    await vscode.window.showTextDocument(typed);
    const typedText = typed.getText();
    const typedAt = (needle, delta) => typed.positionAt(typedText.indexOf(needle) + delta);
    const typeError = await poll('TS2322 in Typed.vela', () => vscode.languages.getDiagnostics(typedUri).find((d) => String(d.code) === 'TS2322'));
    out.typedDiagnostic = `${typeError.code} ${typed.getText(typeError.range)}`;
    assert.strictEqual(typed.getText(typeError.range), 'total');
    out.typedHover = await poll('a hover of row', async () => {
      const hovers = await vscode.commands.executeCommand('vscode.executeHoverProvider', typedUri, typedAt('row.title', 1));
      const value = hovers.flatMap((h) => h.contents.map((c) => c.value ?? String(c))).join('\n');
      return value.includes('row: Row') ? value : null;
    });
    const cardDefinition = await vscode.commands.executeCommand('vscode.executeDefinitionProvider', typedUri, typedAt('<Card', 2));
    out.typedDefinition = cardDefinition.map((d) => path.basename((d.uri ?? d.targetUri).fsPath));
    assert.ok(out.typedDefinition.includes('Card.vela'), 'definition of <Card');
    const typedRename = await vscode.commands.executeCommand('vscode.executeDocumentRenameProvider', typedUri, typedAt('rows: Row', 1), 'items');
    out.typedRename = typedRename.get(typedUri).map((e) => e.newText);
    assert.strictEqual(out.typedRename.length, 2);

    // A .ts module that imports it, in VS Code's TypeScript with the plugin.
    const useUri = vscode.Uri.file(path.join(root, 'src', 'use.ts'));
    const use = await vscode.workspace.openTextDocument(useUri);
    await vscode.window.showTextDocument(use);
    const useText = use.getText();
    const useAt = (needle, delta) => use.positionAt(useText.indexOf(needle) + delta);
    const propError = await poll('the wrong prop in use.ts', () => {
      const all = vscode.languages.getDiagnostics(useUri);
      out.useDiagnostics = all.map((d) => `${d.code} ${use.getText(d.range)}: ${d.message}`);
      return all.find((d) => String(d.code) === '2322');
    });
    out.useDiagnostic = use.getText(propError.range);
    assert.strictEqual(out.useDiagnostic, 'heading');
    out.useHover = await poll('a hover of Typed', async () => {
      const hovers = await vscode.commands.executeCommand('vscode.executeHoverProvider', useUri, useAt('mount(Typed', 7));
      const value = hovers.flatMap((h) => h.contents.map((c) => c.value ?? String(c))).join('\n');
      return value.includes('TypedProps') ? value : null;
    });
    const typedDefinition = await vscode.commands.executeCommand('vscode.executeDefinitionProvider', useUri, useAt('mount(Typed', 7));
    out.useDefinition = typedDefinition.map((d) => path.basename((d.uri ?? d.targetUri).fsPath));
    assert.ok(out.useDefinition.includes('Typed.vela'), 'definition of Typed');
    const useRename = await vscode.commands.executeCommand('vscode.executeDocumentRenameProvider', useUri, useAt('target =', 1), 'root');
    out.useRename = useRename.get(useUri).length;
    assert.strictEqual(out.useRename, 2);
    out.ok = true;
  } catch (error) {
    out.error = String(error?.stack ?? error);
    throw error;
  } finally {
    fs.writeFileSync(process.env.VELA_RESULT, JSON.stringify(out, null, 2));
  }
};
