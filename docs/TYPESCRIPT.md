# TypeScript in Vela

This page is the contract for TypeScript support: what the compiler
accepts, how types are checked, and how TypeScript reaches server
rendering and Vela App. Tasks 080–089 implement it; the table
[Rule ownership](#rule-ownership) names the task of each rule.

Status: **planned**, contract of task 079, written 2026-11-05, pending
approval. Nothing on this page is implemented yet: today `lang="ts"` is
ignored and the first type annotation is `VELA-JS-SYNTAX`. Until task 089
marks TypeScript as experimental in [FEATURES.md](FEATURES.md), write
JavaScript (with JSDoc where types help).

The rules are numbered so that code, tests and reviews can cite them:

| Prefix | Topic |
| --- | --- |
| `TS-1` | Build and checking |
| `TS-2` | Accepted and rejected syntax |
| `TS-3` | Isolated modules and imports |
| `TS-4` | Components |
| `TS-5` | `.ts` modules |
| `TS-6` | Types published by Vela |
| `TS-7` | Server rendering |
| `TS-8` | Vela App |
| `TS-9` | JavaScript compatibility |
| `TS-10` | Versions and tsconfig |

## TS-1 Build and checking

- **TS-1.1** The build strips types and never checks them. `vela`, `vela
  --ssr`, `vela --watch`, the compiler service and `@govela/unplugin`
  parse TypeScript with the compiler's own parser (`internal/jsast`) and
  emit JavaScript without the types, as esbuild, Vite and Node's type
  stripping do. They never load TypeScript and never need node: the
  compiler stays one Go binary.
- **TS-1.2** Type errors come from two tools that run the project's own
  TypeScript (TS-10.1):
  - `vela check --types`: every component, template and `.ts` module of
    the project, for CI;
  - `vela-lsp`: the open buffers while typing.
- **TS-1.3** A project with type errors still builds. A build fails on
  Vela's syntax and semantic diagnostics only. Projects run `vela check
  --types` in CI; the starter (088) does.
- **TS-1.4** Erasure never changes behaviour: a TypeScript component
  compiles to the JavaScript its hand-written JavaScript twin compiles to.
  Each feature is tested as such a pair.
- **TS-1.5** Templates are type-checked, not only scripts: expressions,
  child component props, `bind:` targets, events and slot names (084).

## TS-2 Accepted and rejected syntax

The boundary is TypeScript's own `erasableSyntaxOnly` (TypeScript 5.8):
what can be removed to leave working JavaScript is accepted; what
generates code at run time is rejected.

### TS-2.1 Accepted

| Area | Syntax |
| --- | --- |
| Annotations | variables, parameters (including `this: T`), return types, class fields, optional parameters `x?: T`, catch clause `unknown`/`any` |
| Declarations | `interface`, `type`, overload signatures, `declare` (variables, functions, classes, `module`, `global`), namespaces that contain only types |
| Type language | keywords, references with arguments, unions, intersections, arrays, tuples (named, optional, rest elements), function and constructor types, object types with index signatures, mapped types (with `as` and modifiers), conditional types with `infer`, indexed access, `keyof`, `typeof`, `unique symbol`, template literal types, type predicates, `asserts`, `import('m').T` |
| Generics | on functions, arrows, classes, interfaces, aliases and methods; constraints, defaults, `in`/`out` variance, `const` type parameters |
| Expressions | `x as T`, `x as const`, `x satisfies T`, non-null `x!`, type arguments on calls, `new` and tagged templates (`f<T>(x)`, `new Map<K, V>()`), generic arrows (`<T,>(x: T) => x`) |
| Classes | `public`, `private`, `protected`, `readonly`, `abstract`, `override` modifiers, `implements`, optional `x?: T` and definite `x!: T` fields, `declare x: T` fields, abstract classes and members |
| Imports and exports | `import type`, `export type`, inline `type` specifiers (`import { type X, y }`), `export type * from` |

- **TS-2.2** A class field without an initializer stays a JavaScript
  field: `x: T;` becomes `x;`, which defines `x` as `undefined`
  (`useDefineForClassFields`, TS-10.2). Only `declare x: T` disappears.
- **TS-2.3** Erasure is careful where line breaks matter: an erased
  declaration does not join the lines around it, and `return`, `throw`
  and `yield` keep their operand on their line.

### TS-2.4 Rejected

Each is `VELA-TS-NON-ERASABLE` with the range of the construct and the
rewrite in the message:

| Construct | Rewrite |
| --- | --- |
| `enum`, `const enum`, `declare enum` | an object `as const` and `type E = (typeof E)[keyof typeof E]` |
| `namespace N { … }` with values | a module |
| parameter properties `constructor(private x: number)` | a field and `this.x = x` |
| `import x = require('m')`, `import x = N.y` | `import` / `const` |
| `export = x` | `export default x` |
| angle-bracket assertions `<T>x` | `x as T` |
| decorators (experimental and ES), `accessor` fields | none; decorators are not supported in JavaScript either |

- **TS-2.5** TypeScript syntax in a component without `lang="ts"` or a
  `.js` module is `VELA-TS-IN-JS`, with the hint to add `lang="ts"` or
  rename the module, instead of today's `VELA-JS-SYNTAX` "expected ';'".
- **TS-2.6** Constructs that Vela rejects in JavaScript stay rejected in
  TypeScript: `using`, decorators, template features listed as
  unsupported in AGENTS.md.

## TS-3 Isolated modules and imports

- **TS-3.1** The compiler sees one file at a time and resolves no types
  across files (`isolatedModules`).
- **TS-3.2** `import type`, `export type` and `type` specifiers are
  erased. An import or export without `type` is kept as written
  (`verbatimModuleSyntax`). An import declaration left empty by erasing
  its `type` specifiers is removed entirely.
- **TS-3.3** A name imported without `type` and used only in type
  positions is `VELA-TS-TYPE-IMPORT`, with the fix `import type` / `type
  X`. Kept, it would be a missing export in the browser.
- **TS-3.4** A local type re-exported from a module must be exported
  with `export type`.

## TS-4 Components

```vela
<script lang="ts" generics="T extends { id: string }">
  import { onMount } from 'vela';
  import type { Filter } from './filter.ts';

  interface $$Events {
    select: { item: T };
  }

  export let items: T[] = [];
  export let filter: Filter | null = null;
  export let query: string = $bindable('');

  let selected = $state<T | null>(null);
  let visible = $derived(items.filter((item) => matches(item, query)));

  function matches(item: T, q: string): boolean {
    return item.id.includes(q);
  }

  function choose(item: T) {
    selected = item;
    $$dispatch('select', { item });
  }
</script>

<template>
  {#each visible as item (item.id)}
    <button @click={() => choose(item)}>{item.id}</button>
  {/each}
</template>
```

- **TS-4.1** `<script lang="ts">` (or `lang="typescript"`) makes the
  component a TypeScript component. `lang="js"` or no `lang` is
  JavaScript. Any other value is `VELA-TS-LANG`. `lang` combines with
  `context="client"`.
- **TS-4.2** The script accepts TS-2.1.
- **TS-4.3** Template expressions of a TypeScript component are parsed as
  TypeScript **expressions**: `as`, `satisfies`, `!`, type arguments, and
  annotated parameters of inline arrows (`@click={(e: MouseEvent) => …}`).
  Declarations cannot appear in a template. Binding patterns of `{#each}`
  and `{#await}` take no annotations; their types come from the
  collection and the promise. A `bind:` target stays an assignable
  reference (`bind:value={x as string}` is an error).
- **TS-4.4** Props:
  - `export let title: string = 'Untitled'`: the type is the annotation;
    a prop with a default is optional for the parent;
  - `export let title: string`: required (no default);
  - `export let value: string = $bindable('')`: bindable; the parent may
    use `bind:value`;
  - without an annotation, the type is inferred from the default, as
    TypeScript does.
- **TS-4.5** Runes take type arguments: `$state<T>(initial)`,
  `$state<T>()` (type `T | undefined`), `$state.deep<T>(initial)`,
  `$derived<T>(expression)`, `$bindable<T>(fallback)`.
- **TS-4.6** Events: an interface `$$Events` declared in the script maps
  event names to `detail` types. With it, `$$dispatch(name, detail)` is
  checked against it and the parent's `@on:name` handler receives
  `CustomEvent<detail>`. Without it, any name is accepted with `any`
  detail, as in JavaScript.
- **TS-4.7** Slots: an interface `$$Slots` declared in the script names
  the slots a parent may fill (`default`, `actions`, …) with `{}` values.
  Without it, the slot names come from the template's `<slot>` elements.
  Vela slots have no slot props; if they are added, the values of
  `$$Slots` will type them.
- **TS-4.8** Generic components: `generics="T extends { id: string }, K"`
  on `<script>` declares type parameters, written as inside `<…>` of a
  TypeScript generic function. The parent's props infer them. An invalid
  list is `VELA-TS-GENERICS`.
- **TS-4.9** `$$Events`, `$$Slots` and `generics` are type-level only:
  they never change the generated JavaScript, and in a JavaScript
  component they are ignored (with a JSDoc equivalent in 083).
- **TS-4.10** `<vela:component :this>`: props on the tag are checked
  against every candidate component; a prop no candidate declares is an
  error, a prop some candidates lack is allowed (they ignore it, as
  FEATURES documents). `bind:` requires a bindable prop on every
  candidate.

## TS-5 `.ts` modules

- **TS-5.1** Project mode compiles local `.ts` and `.mts` modules to
  `.js` beside the mirrored output, through the same erasure. `.d.ts`
  files are read by the checker and never emitted.
- **TS-5.2** `.tsx`, `.jsx` and `.cts` are `VELA-TS-MODULE`.
- **TS-5.3** Specifiers: `./util.ts` (recommended) is rewritten to
  `./util.js` in the output, like `./Card.vela`; this also applies to
  dynamic `import()`. A `./util.js` specifier whose file is `util.ts` is
  accepted and kept. Package specifiers are never rewritten.
- **TS-5.4** With the bundler plugin, `.ts` modules are the bundler's
  (Vite and esbuild strip them); only `.vela` files go through Vela.
  Both strippers must agree, which TS-10.2 guarantees by
  `erasableSyntaxOnly`.

## TS-6 Types published by Vela

- **TS-6.1** Every npm package ships declarations, wired by the `types`
  condition of `exports`: `@govela/runtime` (`.`, `./hydrate`, the
  stores), `@govela/compiler`, `@govela/unplugin`, `@govela/app/client`.
  Only public names are declared; `$$` helpers stay undeclared.
- **TS-6.2** An ambient declaration file of `@govela/runtime` declares
  the runes, `$$dispatch`, and the modules `vela` and `vela/store` that
  components import (they are rewritten by the compiler). The project
  includes it through `types` in tsconfig (TS-10.2). The language
  server's built-in declarations are generated from the same file.
- **TS-6.3** `vela` exports `Component<Props, Events, Slots>`, the type
  of a component value, and `Lazy<C>`, the type of `lazy()`. `mount`,
  `hydrate` and `<vela:component :this>` accept both and check props.
- **TS-6.4** Component types in a project come from the checker, not
  from files on disk: `vela check --types`, `vela-lsp` and the TypeScript
  server plugin (085) present `Card.vela` to TypeScript as the virtual
  declaration `Card.d.vela.ts` (TypeScript's `allowArbitraryExtensions`
  naming), so `import { Card } from './Card.vela'` in a `.ts` module is
  typed. Plain `tsc` does not know `.vela` files and is not a supported
  checker for projects that import them from TypeScript.
- **TS-6.5** `vela --dts` writes declarations for compiled output:
  `Card.d.ts` beside `Card.js`, for component libraries published to npm.

## TS-7 Server rendering

- **TS-7.1** `vela --ssr` renders a TypeScript component and translates a
  `.ts` module exactly like their JavaScript twins (TS-1.4); the HTML and
  the payload are byte-identical. Host lookup mirrors `.ts` paths:
  `ssrhost/<path>/util.go` serves `util.ts`.
- **TS-7.2** Annotations of props (`export let`) take the place JSDoc
  `@type` has today: they choose the Go type of the field in the
  generated `*_props.go` (`string`, `float64`, `bool`, slices, objects;
  anything else `any`). The Go compiler then checks the value at the
  boundary, so these annotations are safe to trust.
- **TS-7.3** Other annotations (locals, parameters, return types,
  module functions) do **not** select the typed tier in v1. The build does
  not check them, and a wrong one (`as any`, an unchecked cast) would let
  the typed tier produce a result the dynamic tier would not, breaking
  T-5.4a of [SSR_TRANSLATION_CONTRACT.md](SSR_TRANSLATION_CONTRACT.md).
  The existing inference (literals, flow, built-ins, Go types) decides
  the tier as before. Open question 2 discusses widening this.
- **TS-7.4** `--ssr-scaffold-hosts` takes Go signatures from TypeScript
  parameter and return types where they map (`number` → `float64`,
  `string`, `boolean` → `bool`, `T[]` → `[]T`, known object shapes →
  structs), instead of `any`. The host signature check of §8.3 compares
  the TypeScript signature with the Go one.
- **TS-7.5** `--ssr-report` records the source of each type: `go`,
  `ts`, `jsdoc` or `inferred`.

## TS-8 Vela App

- **TS-8.1** A project is a TypeScript project when `vela.app.json` says
  `"typescript": true`. `vela-app gen` then writes `VelaApp.vela` with
  `lang="ts"` and `vela-app scaffold` writes TypeScript pages.
- **TS-8.2** `vela-app gen` writes the types of each route from the Go
  side, so the Go loader is the single source of truth:
  - `PageData` / `LayoutData` from the loader's result type, through the
    JSON view of L-2 of [APP.md](APP.md) (`json` tags, `omitempty` →
    optional, pointers → `| null`, `time.Time` → `string`, maps with
    string keys → `Record<string, T>`, `any` → `unknown`);
  - `Params` from the path (P-2): `[id]` → `string`, `[[lang]]` → optional
    `string`, `[...rest]` → `string`;
  - `Route<Params>` for the `route` prop and the form value for `form`,
    `unknown` unless the action's failure type is declared (087).
- **TS-8.3** Changing a Go result type changes the generated types, so
  `vela check --types` reports pages that use a removed or renamed field.
- **TS-8.4** The Go types of the route props stay as APP.md defines them
  (`data` is the JSON value of L-2); the TypeScript annotation of a route
  prop never changes its Go type.

## TS-9 JavaScript compatibility

- **TS-9.1** JavaScript remains first-class. Existing components compile
  to the same output; JSDoc types keep their role in SSR; `checkJs` and
  `// @ts-check` keep working in the language server and in `vela check
  --types`.
- **TS-9.2** JavaScript and TypeScript components and modules mix freely
  in one project and import each other.

## TS-10 Versions and tsconfig

- **TS-10.1** Building needs no TypeScript. Checking needs TypeScript 5.8
  or 6.x from the project (`node_modules`), the editor's TSDK, or
  `--tsdk`. TypeScript 7 (the native compiler) has no language service
  API yet; the checker supports it when it does. Without node or a
  usable TypeScript, `vela check --types` fails with that reason and
  plain `vela check` still works.
- **TS-10.2** The tsconfig of a Vela TypeScript project (the starter
  writes it; `vela check --types` warns about a setting that contradicts
  it):

  ```json
  {
    "compilerOptions": {
      "target": "ES2022",
      "module": "ESNext",
      "moduleResolution": "bundler",
      "strict": true,
      "noEmit": true,
      "isolatedModules": true,
      "verbatimModuleSyntax": true,
      "erasableSyntaxOnly": true,
      "allowImportingTsExtensions": true,
      "useDefineForClassFields": true,
      "lib": ["ES2022", "DOM", "DOM.Iterable"],
      "types": ["@govela/runtime/ambient"]
    },
    "include": ["src"]
  }
  ```

  `erasableSyntaxOnly` makes `tsc` reject what TS-2.4 rejects, and
  `verbatimModuleSyntax` what TS-3.3 rejects, so the editor and the
  compiler agree. The ambient entry name is fixed by 082.

## Diagnostics

| Code | Severity | Meaning | Rule |
| --- | --- | --- | --- |
| `VELA-TS-LANG` | error | `lang` other than `ts`, `typescript`, `js` | TS-4.1 |
| `VELA-TS-IN-JS` | error | TypeScript syntax in a JavaScript component or module | TS-2.5 |
| `VELA-TS-NON-ERASABLE` | error | `enum`, value namespace, parameter property, … | TS-2.4 |
| `VELA-TS-TYPE-IMPORT` | error | value import used only as a type | TS-3.3 |
| `VELA-TS-MODULE` | error | `.tsx`, `.jsx`, `.cts` module | TS-5.2 |
| `VELA-TS-GENERICS` | error | invalid `generics` attribute | TS-4.8 |
| `TS<number>` | from TypeScript | type errors of `vela check --types` and `vela-lsp`, mapped to the `.vela` source | TS-1.2 |

Syntax errors of TypeScript sources stay `VELA-JS-SYNTAX`.

## Rule ownership

| Task | Rules |
| --- | --- |
| [080](../tasks/080-jsast-typescript-syntax-and-erasure.md) parser and erasure | TS-2, TS-3.2 |
| [081](../tasks/081-compiler-typescript-components-and-modules.md) compiler | TS-1.1, TS-1.4, TS-3.3, TS-3.4, TS-4.1–TS-4.5, TS-4.9, TS-5 |
| [082](../tasks/082-published-type-declarations.md) package declarations | TS-6.1–TS-6.3 |
| [083](../tasks/083-component-type-model-and-dts.md) component types | TS-4.4, TS-4.6–TS-4.8, TS-4.10, TS-6.5 |
| [084](../tasks/084-type-checking-vela-check-types.md) `vela check --types` | TS-1.2, TS-1.3, TS-1.5, TS-6.4, TS-10 |
| [085](../tasks/085-lsp-and-editor-typescript.md) editors | TS-1.2, TS-6.4 |
| [086](../tasks/086-ssr-typescript.md) SSR | TS-7 |
| [087](../tasks/087-app-route-types.md) Vela App | TS-8 |
| [088](../tasks/088-typescript-starter-testing-eslint.md) starter, testing, ESLint | TS-1.3, TS-10.2 |
| [089](../tasks/089-typescript-docs-and-release.md) docs and release | TS-9, status |

## Open questions

1. **`enum` and parameter properties.** Proposed: rejected in v1
   (TS-2.4). They need generated code, a runtime meaning and an SSR
   translation; `const enum` cannot be inlined across files under
   TS-3.1 anyway. Revisit after 089 if projects ask for them.
2. **Trusting annotations in SSR.** Proposed: only prop annotations
   affect Go types (TS-7.2), because Go checks them at the boundary.
   Widening TS-7.3 would need either a checked conversion where an
   annotated value enters typed code, or `vela --ssr` refusing to trust
   annotations unless `vela check --types` passed for that build.
3. **Name of the ambient entry** (`@govela/runtime/ambient` above):
   fixed by 082.
