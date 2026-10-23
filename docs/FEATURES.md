# Vela feature matrix

This document is the source of truth for what the compiler implements: the
client compiler, server rendering in Go, and hydration. When another document
disagrees with it, this matrix and the compiler diagnostics win.

## Status legend

| Status | Meaning |
| --- | --- |
| **Stable** | Implemented by the default generator and covered by automated tests. |
| **Partial** | Implemented, but a documented syntax or behavioural edge case remains. |
| **Experimental** | Available for use, but its public contract may still change. |
| **Planned** | Designed or listed as future work; not implemented. |
| **Unsupported** | Deliberately not accepted by the current compiler. |

`Stable` does not imply a semantic-versioning guarantee or a production SLA.

## Component format and script

| Feature | Status | Example / API | Notes |
| --- | --- | --- | --- |
| Single-file components | **Stable** | `<script>`, `<style>`, `<template>` | Sections may appear in any order. |
| JavaScript parsing | **Stable** | ES2025 modules: classes with fields, private members and static blocks, tagged templates, `for await`, dynamic `import()`, `import.meta`, import attributes, numeric separators, Unicode identifiers | The compiler ships its own parser and performs static analysis. Every generated module is reparsed before it is written, and early errors (`break` outside a loop, `await` outside an async function, undeclared private names, …) are positioned diagnostics, so unsupported syntax never compiles to broken output. A component script exports props only (`export let`); other exports are `VELA-SCRIPT-EXPORT`. Decorators and `using` are not supported. Coverage: docs/ES_COMPLIANCE.md. TypeScript is not supported. |
| Shallow reactive state | **Stable** | `let count = $state(0)` | Assignments and updates to the binding schedule direct DOM updates. |
| Deep reactive state | **Stable** | `let todos = $state.deep([])` | A runtime `Proxy` observes nested object and array mutations. |
| Derived values | **Stable** | `let doubled = $derived(count * 2)` | Expression form; no `$derived.by(...)` callback form. |
| Effects and cleanup | **Stable** | `$effect(() => { ...; return cleanup })` | Runs after mount and on statically discovered reactive dependencies. |
| Lifecycle hooks | **Stable** | `onMount(fn)`, `onDestroy(fn)` | Cleanup is included in component teardown. |
| Props | **Stable** | `export let title = ''` | Components expose `instance.set({ title })`; `$props()` destructuring is unsupported. |
| Bindable props | **Stable** | `export let value = $bindable('')` | Enables parent-to-child two-way component bindings. |
| Imports | **Stable** | `import { Card } from './Card.vela'` | Local `.vela` imports are rewritten to `.js`. Names imported from `vela` or `vela/store` must be runtime exports or lifecycle hooks (`VELA-UNKNOWN-IMPORT`). |
| Store helpers | **Stable** | `writable`, `readable`, `derived` from `vela/store` | Runtime store utilities. `vela` and `vela/store` imports in components and copied `.js` modules are rewritten to the runtime. |

## Runtime packaging and reactive scheduling

The runtime sources are `runtime/runtime.js` (scheduler, stores, deep-state
proxy, helpers), `runtime/runtime-blocks.js` (block engine) and
`runtime/runtime-hydrate.js` (`hydrate()`). With the default
`--runtime-import=relative`, every build (project or listed files) writes
them once at the output root and generated modules import them by relative
paths, so a page loads one runtime instance. With `bare`, modules import the
npm package `@govela/runtime` instead (see Runtime delivery below).

State and prop writes mark component-local dirty flags; one microtask flush
per synchronous burst runs structural reconciliation first, then each
affected DOM site and dependent effect once, with final values. A setter
called from an effect schedules a following flush.

Store subscriptions are synchronous. `readable` starts on its first subscriber and stops after its last; `derived` subscribes lazily and its `destroy()` method is final and idempotent. Subscriber and deferred-callback failures are reported after later and reentrant callbacks have drained, so one bad callback does not leave queued DOM work stranded.

`$state.deep` proxies ordinary objects and arrays recursively. Repeated reads preserve proxy identity, cyclic plain-object graphs are supported, and built-ins that require internal slots (for example `Date`, `Promise`, `Map`, and typed arrays) deliberately remain unwrapped.

The Proxy suppresses equal writes and missing-key deletes; built-in mutating
array methods publish at most one notification per logical operation. A
nested write to one row of a keyed `{#each}` over a `$state.deep` array
updates only that row; appends, removals, reorders and other shapes use the
full keyed reconciler.

## Template and component features

| Feature | Status | Example | Notes |
| --- | --- | --- | --- |
| HTML elements and text | **Stable** | `<p>Hello</p>` | Multiple roots are wrapped with `display: contents`. Whitespace follows HTML as Svelte applies it: one space between sibling nodes where the source has whitespace, none at the start and end of element content and block branches, runs collapsed; `<pre>` and `<textarea>` verbatim. |
| Interpolation | **Stable** | `<p>{name}</p>` | Expressions update only the affected text node. |
| Dynamic attributes | **Stable** | `:href={url}`, `:disabled={!ready}` | Plain braced attribute values are also recognised as dynamic where applicable. A static `class="a b"` together with `:class={expr}` is one class value, `` `a b ${expr}` ``, on the client and the server. Any other attribute given twice on one element is rejected (`VELA-DUPLICATE-ATTR`); braces inside a static value such as `title="x {y}"` are not interpolated and warn (`VELA-ATTR-BRACES`). |
| DOM events | **Stable** | `@click={increment}` | Vela uses `@event`, not Svelte's `onclick` or legacy `on:event`. |
| DOM bindings | **Stable** | `bind:value={name}`, `bind:checked={done}`, `bind:this={input}` | Includes common input, checkbox, select, textarea, and element-reference cases. |
| Conditional rendering | **Stable** | `{#if open}...{:else if other}...{:else}...{/if}` | Branches mount and tear down around comment anchors without wrapper elements. |
| List rendering | **Stable** | `{#each items as item, i}...{:else}...{/each}` | Unkeyed rows update in place where possible. The item and index bindings are plain identifiers; destructuring patterns are rejected with a positioned diagnostic. |
| Keyed lists | **Stable** | `{#each items as item (item.id)}` | Reconciles DOM rows by key; unchanged rows are not updated. |
| Key blocks | **Stable** | `{#key userId}<Card />{/key}` | Recreates its subtree when the key changes. |
| Await blocks | **Stable** | `{#await promise}...{:then value}...{:catch err}{/await}` | Pending, fulfilled, rejected, stale-promise handling, and `{#await promise then value}` / `{#await promise catch err}` shortcuts are supported. |
| Components | **Stable** | `<Card title={title} />` | Components are compiled ES-module functions with `set()` and `destroy()`. |
| Component bindings | **Stable** | `<Counter bind:count={count} />` | The child prop must be declared with `$bindable`. |
| Component events | **Stable** | `<Modal @on:close={close} />` | The child calls `$$dispatch(name, detail)`; the parent listens with `@on:name` (or `@name`) and reads `event.detail`. |
| Slots | **Stable** | `<slot name="actions" />`, `<Card><button slot="actions" /></Card>` | Default and named slots with fallback content (the Svelte 4 model; no snippets). |
| Slot fragments | **Stable** | `<svelte:fragment slot="actions">...</svelte:fragment>` | Allows several nodes without a wrapper element. |
| Class directives | **Stable** | `class:active={open}`, `class:active` | Preserves static classes; without static classes the attribute appears only while a class is on. |
| Style directives | **Stable** | `style:color={theme}`, `style:--accent={color}` | Supports hyphenated names and CSS custom properties. |

## CSS, compiler, and diagnostics

| Feature | Status | Example / API | Notes |
| --- | --- | --- | --- |
| Scoped CSS | **Stable** | `<style>.card { ... }</style>` | Rules and generated elements share a `vela-XXXXXX` scope attribute; components without styles write no scope attribute. |
| CSS delivery | **Stable** | `--css=inject` or `--css=external` | Default injects a style element; external mode emits a sibling `.css` file. |
| Project compilation | **Stable** | `--project src --out dist` | Mirrors the source directory tree; parallel compilation is enabled by default. Local `.js` modules are copied unchanged except that `.vela` import specifiers (static, re-export and literal `import()`) point to the compiled `.js` modules and `vela` / `vela/store` point to the runtime, so an entry point such as `main.js` importing `App.vela` loads in the browser or a bundler. Package imports (`date-fns`) are left as written for the bundler or import map of the application. `vela.manifest.json` lists the components and the package imports of every module; `--check-deps` warns about package imports that do not resolve in `node_modules`. |
| Watch mode | **Experimental** | `--watch` | Recompiles changed `.vela` files; it is not HMR. With `--ssr`, every change of a component, local module, `vela.ssr.json` or host file rebuilds the whole project and removes the outputs of removed components; each build without errors writes `out/.vela-ready`, the signal to restart the Go server. |
| SSR with Vite | **Stable** | `ssr_runtime/vite`: `vite.Dev(url)`, `vite.Load(manifest, base)`, `Assets.Tags(entries…)` | The Go server writes the client's tags: the Vite client and entry modules of the dev server, or the hashed scripts, modulepreload links and stylesheets of a `vite build` manifest. The client compiled by `@govela/unplugin` hydrates the HTML of the Go renderers when `vela` and Vite run in the same directory. Runs in CI in Chromium (`TestSSRWithVite`). |
| Minification and DCE | **Stable** | `--minify` | Includes conservative dead-binding elimination in addition to JS minification. |
| Build mode | **Stable** | `--mode=development` (default) or `--mode=production` | Production builds stage a runtime with development warnings and hydration value checks disabled. Independent of `--minify`. |
| DOM engine | **Stable** | block engine (`runtime-blocks.js`) | The block engine (docs/HYDRATION_ARCHITECTURE.md) builds CSR components, the Go SSR markup, and hydration from one render plan. It is the only engine: the legacy engine and the `--dom-engine` flag were removed in task 042. |
| Runtime delivery | **Stable** | `--runtime-import=relative` (default), `--runtime-import=bare` | `relative` writes the runtime beside the output and imports it by relative paths. `bare` imports the npm package `@govela/runtime` (same version as `vela --version`) for applications that bundle their client: one runtime instance per page, development checks removed by the bundler's `process.env.NODE_ENV` replacement, a version mismatch fails at bundle time. Recipes for esbuild, Vite, Rollup and import maps: docs/GUIDE.md "Building an application"; the esbuild recipe runs in CI. The compiler does not bundle. |
| Bundler plugin | **Stable** | `@govela/unplugin/vite`, `/rollup`, `/rolldown`, `/esbuild`, `/webpack`, `/rspack` | Compiles `.vela` modules inside the bundler through the compiler service: source maps, scoped CSS as a CSS module (`css: 'external'`, Vite and esbuild) or injected, diagnostics as bundler errors and warnings with locations, `vela` / `vela/store` resolved to `@govela/runtime`. Vite (dev server and builds), esbuild and Rolldown run in CI in Chromium; the webpack and Rspack entries are not run in CI. A changed component reloads the page; no HMR. |
| Language server | **Experimental** | `vela-lsp` (npm `@govela/compiler`, `go install …/cmd/vela-lsp`) | LSP over stdio for any editor (docs/EDITORS.md): diagnostics of the buffer while typing with ranges, SSR checks of the project on save, definition, hover and completion of components, props, events, directives, blocks and the script's names. JavaScript features through TypeScript 5.x/6.x (not bundled; TypeScript 7 has no language service API): members, types, references, rename, signature help, type errors with `// @ts-check`, in the script and the template. No rename of props and component imports; no VS Code extension yet (task 065d). |
| Source maps | **Experimental** | `--sourcemap` | Writes an external v3 `.js.map`; template expressions map to their source offsets. No CSS maps; cannot be combined with `--minify`. |
| Input AST dump | **Stable** | `--ast` | Emits `<name>.ast.json` for the parsed SFC/template AST. |
| Generated JS AST dump | **Stable** | `--emit-gen-ast` | Emits `<name>.genast.json` for AST-codegen inspection. |
| Compiler trace | **Experimental** | `--trace=build.trace.jsonl`, `--trace=-` | Appends one source-free `CompilationTraceV1` JSON record per component. File output is safe for parallel project builds. With `--trace=-`, stdout is JSONL only and human progress moves to stderr. Schema version 1 is additive-only within Vela 1.x; consumers must ignore unknown fields. Disabled by default, so it adds no timing, allocation, or I/O cost to ordinary compilation. |
| Scope diagnostics | **Stable** | undeclared names, TDZ, redeclarations, invalid writes | Diagnostics use one-based UTF-16 positions; repeated template attributes keep their own source location. |
| Actionable Vela diagnostics | **Stable** | <code>VELA-*</code> code, <code>help:</code> correction, DiagnosticsJSON(diags) | Stable codes cover common Svelte-like mistakes; JSON is compact for editor and AI tooling. |
| Reactivity diagnostics | **Stable** | invalid derived writes, effects without reactive deps | Template references are also checked for undeclared names. |
| Flow diagnostics | **Stable** | code after `return`, `throw`, `break`, `continue` | Reports unreachable statements. |

## Server rendering and hydration

Overview and usage: [SSR.md](SSR.md). Normative rules:
[SSR_TRANSLATION_CONTRACT.md](SSR_TRANSLATION_CONTRACT.md) and
[HYDRATION_ARCHITECTURE.md](HYDRATION_ARCHITECTURE.md).

| Feature | Status | Example / API | Notes |
| --- | --- | --- | --- |
| SSR and hydration | **Experimental** | `--ssr`; `hydrate(Component, container, options)` from `runtime-hydrate.js` | Go renderers write block markup and one payload stream per root (docs/HYDRATION_ARCHITECTURE.md §5–§6): `<!--[state-->…<!--]-->` ranges, no element markers or instance identifiers, and a `<script type="application/json" data-vela-root>` after the root. API: `RenderX`, `RenderXWithOptions` (root await snapshots), `RenderXStatic` (plain HTML), `X(props, slots)`; the `…At` instance APIs do not exist. Elements carry the component scope attribute, so scoped styles apply to server HTML. Rows written directly in `<table>` get an implicit `<tbody>` in both outputs; constructs the HTML parser rearranges are rejected with `VELA-HTML-NESTING`. The client hydrates with `hydrate()`: the same compiled fragments adopt the server DOM, component state is restored from the payload stream (only `$state` whose initializer is not a literal or a prop reference travels there, and not values JSON cannot restore: functions, promises, Dates, collections, objects with methods; the client computes the others), and mismatches follow the `repair` (development), `fail`, or production policy with rollback (`--hydration-verify=repair|fail|off`). Development builds report the source position, site ID, host symbol, and both values of a mismatch (`[vela] hydration mismatch at src/X.vela:14:9 (site 7, host formatCurrency)`); `--minify` builds omit the site table. Components without events, bindings, effects, or lifecycle hooks in their tree are static roots: plain HTML without JavaScript (`vela.manifest.json` lists modes and client modules). `{#client}…{:else}…{/client}` and `<script context="client">` components render their fallback (or nothing) on the server and mount on the client after hydration commits (contract §9). The generated Go code imports the runtime module `github.com/elauven/vela/ssr_runtime` (nested module in `ssr_runtime/`, tags `ssr_runtime/vX.Y.Z`); an application requires that module, not the compiler, and a runtime of another contract version fails `go build` (`vela.ContractV3`). `"runtime": {"module": …}` in `vela.ssr.json` selects a fork. A component whose child has no server renderer fails with `VELA-SSR-CHILD` instead of producing Go that does not build. The beta gate `TestSSRGate` checks the real corpus through the CLI, generated Go, HTTP and Chromium against recorded outcomes ([SSR_GATE.md](SSR_GATE.md)). |
| SSR report and diagnostics | **Experimental** | `--ssr --ssr-report=<file.json>` | Writes one record per server-evaluated site (template expressions, child props, prop defaults, `$state`/`$derived` declarations): outcome (`server-native`, `server-host` or `unsupported`), tier, type, and the bindings it reads (docs/SSR_TRANSLATION_CONTRACT.md §10.4). SSR diagnostics list every failed site of a component with position, reason chain, and a fix; event handlers, effects, and lifecycle hooks are never translated and never block SSR. Writes during rendering report `VELA-SSR-IMPURE`. A child prop whose value has no proven type is converted to the child's Go field type through its JSON view, or fails the render naming the prop. |
| SSR host implementations | **Experimental** | `//vela:host name` in `ssrhost/<mirrored path>/`, `vela.ssr.json`, `--ssr-scaffold-hosts` | A Go function stands in for a JavaScript function the compiler does not translate: a top-level function or value of a component, or an exported function of a local `.js` module (docs/SSR_TRANSLATION_CONTRACT.md §8). Arguments are converted by the rules of §8.4 (`vela.Optional[T]` for absent or null arguments), an optional first `context.Context` receives the context passed to `RenderXContext`, and an error result fails the render as `*vela.HostError`. Diagnostics name the host file and the Go signature; stale, duplicate, and mismatched directives fail the build. Development hydration compares host results with the JavaScript function and reports a disagreement with the host name (`TestSSRHostImplementationEndToEnd`). |
| SSR of script code | **Experimental** | `--ssr` | Component functions with several statements (`if`, loops including `for…in` and labelled `break`/`continue`, `switch`, `try`/`catch`/`finally`, `throw`, destructuring with rest elements and computed keys, closures with per-iteration `let` loop bindings, recursion), optional calls (`f?.()`, `a.b?.()`), the `in`, comma, `void` and `delete` operators, arrow and function values, object methods, getters and setters with `this`, calls of functions stored in properties, objects with their own `toString`, `valueOf` or `toJSON`, local `.js` modules (one Go package each under `server/modules/`, values created per render), and `async`/`await` with the synchronous model of docs/SSR_TRANSLATION_CONTRACT.md §7.4: an async call settles at once, `{#await}` renders its settled `then` or `catch` branch on the server, and the client adopts it without re-running the operand. Host errors reach `catch` as `Error("Internal error")` unless wrapped in `vela.PublicError`. Built-ins follow the catalogue of docs/SSR_TRANSLATION_MATRIX.md §2 (string, array, Math, Number, Object, and JSON functions, `structuredClone` and ``String.raw`…` `` with JavaScript semantics: UTF-16 positions, full case mapping, `toFixed` and `Math.round` rounding, holes; error objects convert to text as `Error.prototype.toString`; `Date` in UTC, `Map`, `Set`, `Object.groupBy` and `Map.groupBy`); methods that change an array or a collection are admitted only on values the function created, and arrays have identity, so aliases, containers and `this` see changes. What depends on the time zone (`new Date()`, local getters, text of a Date) is not translated. Functions have typed variants per argument types. `Math` matches V8 bit for bit (ports of its fdlibm functions), `normalize` uses `golang.org/x/text`, and `console.*` goes to `vela.WithConsoleObserver`. The Go runtime requires Go 1.25. Values only the browser can compute go into `{#client}` blocks; a structural site the server cannot evaluate is a build error whose fix says so (T-9.3). |

## Not implemented and unsupported

| Feature | Status | Notes |
| --- | --- | --- |
| Dev server / HMR | **Planned** | Watch mode recompiles only. |
| TypeScript | **Unsupported** | No `lang="ts"` pipeline or type checker. |
| Snippets / render props | **Unsupported** | Use current default/named slots instead. |
| Actions | **Unsupported** | No `use:action` directive. |
| Transitions and animations | **Unsupported** | No `transition:`, `in:`, or `out:` directives. |
| Dynamic components and tags | **Unsupported** | No `<svelte:component>` or `<svelte:element>`. |
| Svelte special elements | **Unsupported** | No `<svelte:head>`, `<svelte:window>`, `<svelte:document>`, `<svelte:body>`, or `<svelte:self>`. |
| Extended DOM bindings | **Unsupported** | `bind:group`, `bind:files`, dimensions, and `innerHTML` bindings are not implemented. |

## Runtime fixture catalog

Runtime fixtures compile `.vela` sources with the default AST generator, load
the generated ES modules in jsdom, stage declared local JavaScript utility modules, replay interactions, assert on DOM state,
and call `instance.destroy()`. The shared Go helpers are in
`runtime_behaviour_test.go`; the Node driver is `tools/runtime-harness.mjs`.

The shared fixture DSL supports text, HTML, value, storage, count, focus,
style, and fixture-owned `globalThis` metric assertions; focus, click,
double-click, submit, input, change, blur, keyboard, tick, and remount
actions. Both jsdom and Chromium harnesses replay the same steps. The jsdom
suite skips when Node or jsdom is unavailable; it complements, rather than
replaces, real-browser compatibility coverage.

| Fixture group | What it proves | Representative tests |
| --- | --- | --- |
| Core reactivity | Initial mount, event-driven state changes, derived values, effects, and dynamic attributes update rendered DOM. | `TestRuntimeCounter`, `TestRuntimeEffectReadsDerived`, `TestRuntimeIfBranchDynamicAttr` |
| Conditional lifecycle | Branch insertion/removal, reactive holes, repeated open/close, and inactive branches remain safe. | `TestRuntimeIfToggle`, `TestRuntimeIfHole`, `TestRuntimeIfToggleOffWithNullDerived`, `TestRuntimeNestedIfChainReactive` |
| Lists and DOM identity | Basic list rendering, nested lists, unkeyed append/remove reconciliation, and focus preservation. | `TestRuntimeEachRender`, `TestRuntimeEachNested`, `TestRuntimeUnkeyedEachPreservesFocus`, `TestRuntimeUnkeyedEachAppendRemoveRoundTrip` |
| Deep state | Proxy-triggered nested mutations rerender lists; proxy identity, cycles, built-in boundaries, and set/delete notification are covered directly. | `TestRuntimeDeepMutationRerendersEach`, `TestRuntimeStoreLifecycleContract` |
| Runtime-cost regression contracts | Chromium verifies that deep no-op writes produce no work and that one synchronous setter burst runs each affected DOM site and dependent effect once with final values. Keyed-row revisits, binding property writes, and deferred work after destroy remain explicit optimisation targets. | `TestBrowserRuntimeAuditDeepNoOpWrite`, `TestBrowserRuntimeAuditBurstSetterAndEffectWork`, `TestBrowserRuntimeCoalescesRepeatedStateAndDerivedFanout`, `TestBrowserRuntimeCoalescesNestedBlockFinalState`, `TestBrowserRuntimeCoalescedFlushKeepsReentrantUpdates`, `TestBrowserRuntimeAuditKeyedDeepEachUpdatesEveryRow`, `TestBrowserRuntimeAuditBindingRepeatsDOMValueWrite`, `TestBrowserRuntimeAuditDestroyBeforeDeferredFlush` |
| Form controls and textareas | Textarea children render and remain correct inside conditional branches; focus assertions exercise mutable DOM state. | `TestRuntimeTextareaChildInterpolation`, `TestRuntimeTextareaChildInIfBranch`, `TestRuntimeUnkeyedEachPreservesFocus` |
| Components, slots, and events | Components survive conditional/list nesting; slot content remains reactive; custom events and teardown do not throw. | `TestRuntimeSlotComponentEvent`, `TestRuntimeComponentInIf`, `TestRuntimeEachRowComponent`, `TestRuntimeNestingMatrix_SlotContentInEachReactive`, `TestRuntimeNestingMatrix_ComponentEventStillFires` |
| Block nesting matrix | Every pair of `if`, `each`, `key`, and `await` is exercised in nested positions, including rejection paths. | `TestRuntimeNestingMatrix_IfInIf`, `TestRuntimeNestingMatrix_KeyInEach`, `TestRuntimeNestingMatrix_AwaitInAwait`, `TestRuntimeNestingMatrix_AwaitRejection` |
| Component structure and scoped CSS | Components inserted into control-flow retain correct DOM shape and direct-child CSS selector semantics. | `TestRuntimeNestingMatrix_ComponentDirectChildSelector`, `TestRuntimeNestingMatrix_ComponentInEachInIf`, `TestRuntimeDashboardCorpusQwen` |
| AI application compatibility corpus | Corrected DeepSeek, GPT, and Qwen Todo applications plus the Qwen dashboard compile as projects and run their representative user flows. | `TestPositiveCorpusProjectsBuildAsProjects`, `TestTodoCorpusCompilation`, `TestRuntimeTodoCorpusQwen`, `TestRuntimeTodoCorpusDeepSeek`, `TestRuntimeTodoCorpusGPT`, `TestRuntimeDashboardCorpusQwen` |
| Chromium browser compatibility | Optional Playwright suite executes compiled project output over HTTP in real Chromium, including module loading, native input/focus behaviour, and scoped CSS selector semantics. | `TestBrowserRuntimeTodoCorpusQwen`, `TestBrowserRuntimeTodoCorpusDeepSeek`, `TestBrowserRuntimeTodoCorpusGPT`, `TestBrowserRuntimeDashboardCorpusQwen`, `TestBrowserRuntimeKeepsFocusAcrossDeepEachUpdate`, `TestBrowserRuntimeComponentKeepsDirectChildCSSSelector` |

The full matrix is intentionally split across `runtime_behaviour_test.go` and
`runtime_nesting_matrix_test.go`. Add a runtime regression to the closest
group whenever a change affects generated DOM lifecycle behaviour.
