// The Vela extension: the language client of vela-lsp (docs/EDITORS.md).
'use strict';
const vscode = require('vscode');
const { LanguageClient, TransportKind } = require('vscode-languageclient/node');
const { serverCommand, initializationOptions } = require('./server.js');

let client;

function createClient(context) {
  const config = vscode.workspace.getConfiguration('vela');
  const folders = (vscode.workspace.workspaceFolders ?? []).map((f) => f.uri.fsPath);
  const command = serverCommand(config.get('server.path'), folders);
  const serverOptions = { command, args: ['--stdio'], transport: TransportKind.stdio };
  return new LanguageClient('vela', 'Vela', serverOptions, {
    documentSelector: [{ scheme: 'file', language: 'vela' }],
    initializationOptions: initializationOptions(context.extensionPath, {
      diagnosticsDelay: config.get('diagnosticsDelay'),
      typescriptEnable: config.get('typescript.enable'),
      tsdk: config.get('typescript.tsdk'),
    }),
  });
}

async function start(context) {
  client = createClient(context);
  try {
    await client.start();
  } catch (error) {
    vscode.window.showErrorMessage(`Vela: cannot start vela-lsp (${error.message}). Install @govela/compiler in the workspace or set vela.server.path.`);
  }
}

async function activate(context) {
  context.subscriptions.push(
    vscode.commands.registerCommand('vela.restartServer', async () => {
      if (client) await client.stop();
      await start(context);
    }),
    vscode.workspace.onDidChangeConfiguration(async (event) => {
      if (event.affectsConfiguration('vela') && !event.affectsConfiguration('vela.trace')) {
        await vscode.commands.executeCommand('vela.restartServer');
      }
    }),
  );
  await start(context);
}

function deactivate() {
  return client?.stop();
}

module.exports = { activate, deactivate };
