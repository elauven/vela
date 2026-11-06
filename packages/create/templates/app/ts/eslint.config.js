import { defineConfig } from 'eslint/config';
import js from '@eslint/js';
import globals from 'globals';
import tseslint from 'typescript-eslint';
import vela from '@govela/eslint-plugin';

export default defineConfig(
  { ignores: ['dist/', 'out/', 'build/', 'src/_app/'] },
  js.configs.recommended,
  tseslint.configs.recommended,
  vela.configs.recommended,
  { files: ['src/**/*.ts', '**/*.vela/*.ts'], languageOptions: { globals: globals.browser } },
  { files: ['*.config.js'], languageOptions: { globals: globals.node } },
);
