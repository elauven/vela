// Types of @govela/eslint-plugin.
import type { ESLint, Linter } from 'eslint';

/** The compiler intrinsics, as globals of the components' code. */
export const globals: Record<'$state' | '$derived' | '$effect' | '$bindable' | '$$dispatch', 'readonly'>;

/** The processor of .vela files. */
export const processor: Linter.Processor;

/** The rules of the plugin: vela/no-at-html-tags. */
export const rules: { 'no-at-html-tags': import('eslint').Rule.RuleModule };

declare const plugin: ESLint.Plugin & {
  configs: { recommended: Linter.Config[] };
};

export default plugin;
