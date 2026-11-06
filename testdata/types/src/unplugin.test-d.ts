// @govela/unplugin: the bundler plugin in each bundler's configuration.
import vela, { unplugin, vite, rollup, rolldown, esbuild, webpack, rspack, type Options } from '@govela/unplugin';
import velaVite from '@govela/unplugin/vite';
import velaEsbuild from '@govela/unplugin/esbuild';
import velaRolldown from '@govela/unplugin/rolldown';
import velaRollup from '@govela/unplugin/rollup';
import velaWebpack from '@govela/unplugin/webpack';
import velaRspack from '@govela/unplugin/rspack';
import { expectType } from './expect.ts';

const options: Options = { css: 'external', cwd: '.', compilerOptions: { target: 'browser', strict: true } };
// @ts-expect-error the plugin owns the source map option
const owned: Options = { compilerOptions: { sourcemap: true } };
vite(options);
velaVite();
velaEsbuild(options);
velaRolldown();
velaRollup();
velaWebpack();
velaRspack();
rollup();
rolldown();
esbuild();
webpack();
rspack();
expectType<typeof unplugin>(vela);
export { owned };
