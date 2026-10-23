// How the extension starts vela-lsp, without the vscode API, so that the
// smoke test (tools/vscode-extension-check.mjs) starts it the same way.
'use strict';
const fs = require('node:fs');
const path = require('node:path');

const executable = process.platform === 'win32' ? 'vela-lsp.exe' : 'vela-lsp';

/**
 * The vela-lsp binary: the configured path, else the one of the
 * @govela/compiler platform package in a workspace folder's node_modules,
 * else vela-lsp on PATH.
 */
function serverCommand(configured, workspaceFolders) {
  if (configured) return configured;
  const platformPackage = `compiler-${process.platform}-${process.arch}`;
  for (const folder of workspaceFolders) {
    // node_modules of the folder or of a parent, as Node resolves.
    for (let dir = folder; ; dir = path.dirname(dir)) {
      const binary = path.join(dir, 'node_modules', '@govela', platformPackage, 'bin', executable);
      if (fs.existsSync(binary)) return binary;
      if (path.dirname(dir) === dir) break;
    }
  }
  return 'vela-lsp';
}

/**
 * The initializationOptions of vela-lsp (docs/EDITORS.md): TypeScript is
 * the configured one, else the one this extension ships.
 */
function initializationOptions(extensionPath, settings) {
  return {
    diagnosticsDelay: settings.diagnosticsDelay,
    typescript: {
      enable: settings.typescriptEnable,
      tsdk: settings.tsdk || path.join(extensionPath, 'node_modules', 'typescript', 'lib'),
    },
  };
}

module.exports = { serverCommand, initializationOptions };
