// Types of @govela/unplugin, the Vela bundler plugin.
import type { CompileOptions } from '@govela/compiler';
import type { UnpluginInstance } from 'unplugin';

export interface Options {
  /**
   * CSS delivery. 'external' (default for Vite and esbuild): the component
   * imports its CSS as the module `X.vela?vela&type=style&lang.css`, which
   * the bundler's CSS pipeline processes. 'inject' (default elsewhere): the
   * component adds its CSS to the document at runtime.
   */
  css?: 'external' | 'inject';
  /**
   * Working directory of the compiler service: the scope classes of the CSS,
   * the paths of diagnostics and vela.config.json. Default: the Vite root,
   * else the process working directory.
   */
  cwd?: string;
  /** A configuration file instead of vela.config.json in cwd. */
  config?: string;
  /** A vela binary instead of the one of @govela/compiler (also VELA_BINARY). */
  binary?: string;
  /**
   * File options of the compiler (`target`, `strict`, …). The plugin always
   * compiles with source maps, bare runtime imports and `.vela` component
   * imports.
   */
  compilerOptions?: Omit<CompileOptions, 'css' | 'sourcemap' | 'runtimeImport' | 'componentImports'>;
}

export declare const unplugin: UnpluginInstance<Options | undefined, false>;
export declare const vite: typeof unplugin.vite;
export declare const rollup: typeof unplugin.rollup;
export declare const rolldown: typeof unplugin.rolldown;
export declare const esbuild: typeof unplugin.esbuild;
export declare const webpack: typeof unplugin.webpack;
export declare const rspack: typeof unplugin.rspack;
export default unplugin;
