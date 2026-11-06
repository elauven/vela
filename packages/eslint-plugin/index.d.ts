// Types of @govela/eslint-plugin.
import type { ESLint, Linter } from 'eslint';

/** The compiler intrinsics, as globals of the components' code. */
export const globals: Record<'$state' | '$derived' | '$effect' | '$bindable' | '$$dispatch', 'readonly'>;

/** The processor of .vela files. */
export const processor: Linter.Processor;

declare const plugin: ESLint.Plugin & {
  configs: { recommended: Linter.Config[] };
};

export default plugin;
