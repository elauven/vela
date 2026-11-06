import { defineConfig } from 'eslint/config';
import js from '@eslint/js';
import globals from 'globals';
import vela from '@govela/eslint-plugin';

export default defineConfig(
  { ignores: ['dist/', 'out/', 'build/', 'src/_app/'] },
  js.configs.recommended,
  vela.configs.recommended,
  { files: ['src/**/*.js', '**/*.vela/*.js'], languageOptions: { globals: globals.browser } },
  { files: ['*.config.js'], languageOptions: { globals: globals.node } },
);
