# Vela feature matrix

This document is the source of truth for the implemented client-side
compiler. It describes the single direct AST code-generation pipeline.

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
| JavaScript parsing | **Stable** | ES2020 script and expression subset | The compiler ships its own parser and performs static analysis. TypeScript is not supported. |
| Shallow reactive state | **Stable** | `let count = $state(0)` | Assignments and updates to the binding schedule direct DOM updates. |
| Deep reactive state | **Stable** | `let todos = $state.deep([])` | A runtime `Proxy` observes nested object and array mutations. |
| Derived values | **Stable** | `let doubled = $derived(count * 2)` | Expression form; no `$derived.by(...)` callback form. |
| Effects and cleanup | **Stable** | `$effect(() => { ...; return cleanup })` | Runs after mount and on statically discovered reactive dependencies. |
| Lifecycle hooks | **Stable** | `onMount(fn)`, `onDestroy(fn)` | Cleanup is included in component teardown. |
| Props | **Stable** | `export let title = ''` | Components expose `instance.set({ title })`; `$props()` destructuring is unsupported. |
| Bindable props | **Stable** | `export let value = $bindable('')` | Enables parent-to-child two-way component bindings. |
| Imports | **Stable** | `import { Card } from './Card.vela'` | Local `.vela` imports are rewritten to `.js`. |
| Store helpers | **Stable** | `writable`, `readable`, `derived` from `vela/store` | Runtime store utilities; application packaging must provide their import path. |

## Runtime packaging and reactive scheduling

`runtime.js` at the project root is the canonical runtime implementation. Project compilation stages a byte-identical runtime module beside every emitted component directory, so generated `./runtime.js` imports resolve without a manual copy step.

The compiler imports `$$defer` directly from that module for every component with state or props. Reactive DOM work therefore uses the shared microtask scheduler regardless of template helpers or module evaluation order.

The scheduler currently coalesces the time at which synchronous work flushes,
but not duplicate component update closures within that flush. Likewise, an
effect that depends on two state values can currently run once for each setter
in a synchronous update burst. This is a tracked performance limitation
(`tasks/022-coalesce-component-reactive-flush-work.md`), not a correctness
guarantee about the number of writes.

Store subscriptions are synchronous. `readable` starts on its first subscriber and stops after its last; `derived` subscribes lazily and its `destroy()` method is final and idempotent. Subscriber and deferred-callback failures are reported after later and reentrant callbacks have drained, so one bad callback does not leave queued DOM work stranded.

`$state.deep` proxies ordinary objects and arrays recursively. Repeated reads preserve proxy identity, cyclic plain-object graphs are supported, and built-ins that require internal slots (for example `Date`, `Promise`, `Map`, and typed arrays) deliberately remain unwrapped.

Deep-state notifications are currently root-granular: a nested mutation
invalidates consumers of the whole binding. The Proxy suppresses equal writes
and missing-key deletes; built-in mutating array methods publish at most one
notification per logical operation. Per-row invalidation of large lists remains
a tracked runtime-cost limitation (`tasks/023-make-each-row-updates-incremental.md`).

## Template and component features

| Feature | Status | Example | Notes |
| --- | --- | --- | --- |
| HTML elements and text | **Stable** | `<p>Hello</p>` | Multiple roots are wrapped with `display: contents`. |
| Interpolation | **Stable** | `<p>{name}</p>` | Expressions update only the affected text node. |
| Dynamic attributes | **Stable** | `:href={url}`, `:disabled={!ready}` | Plain braced attribute values are also recognised as dynamic where applicable. |
| DOM events | **Stable** | `@click={increment}` | Vela uses `@event`, not Svelte's `onclick` or legacy `on:event`. |
| DOM bindings | **Stable** | `bind:value={name}`, `bind:checked={done}`, `bind:this={input}` | Includes common input, checkbox, select, textarea, and element-reference cases. |
| Conditional rendering | **Stable** | `{#if open}...{:else if other}...{:else}...{/if}` | Branches mount and tear down around comment anchors without wrapper elements. |
| List rendering | **Stable** | `{#each items as item, i}...{:else}...{/each}` | Unkeyed rows update in place where possible. Known issue: the default (legacy) DOM engine does not render the `{:else}` branch; `--dom-engine=blocks` does. |
| Keyed lists | **Stable** | `{#each items as item (item.id)}` | Reconciles DOM rows by key. Existing rows are currently revisited on a list invalidation; precise changed-row updates are tracked in task 023. |
| Key blocks | **Stable** | `{#key userId}<Card />{/key}` | Recreates its subtree when the key changes. |
| Await blocks | **Stable** | `{#await promise}...{:then value}...{:catch err}{/await}` | Pending, fulfilled, rejected, stale-promise handling, and `{#await promise then value}` / `{#await promise catch err}` shortcuts are supported. |
| Components | **Stable** | `<Card title={title} />` | Components are compiled ES-module functions with `set()` and `destroy()`. |
| Component bindings | **Stable** | `<Counter bind:count={count} />` | The child prop must be declared with `$bindable`. |
| Component events | **Stable** | `<Modal @on:close={close} />` | Custom events are forwarded through the component wrapper. |
| Slots | **Stable** | `<slot name="actions" />`, `<Card><button slot="actions" /></Card>` | Uses legacy-style named/default slots. |
| Slot fragments | **Stable** | `<svelte:fragment slot="actions">...</svelte:fragment>` | Allows several nodes without a wrapper element. |
| Class directives | **Stable** | `class:active={open}`, `class:active` | Preserves static classes. |
| Style directives | **Stable** | `style:color={theme}`, `style:--accent={color}` | Supports hyphenated names and CSS custom properties. |

## CSS, compiler, and diagnostics

| Feature | Status | Example / API | Notes |
| --- | --- | --- | --- |
| Scoped CSS | **Stable** | `<style>.card { ... }</style>` | Rules and generated elements share a `vela-XXXXXX` scope attribute. |
| CSS delivery | **Stable** | `--css=inject` or `--css=external` | Default injects a style element; external mode emits a sibling `.css` file. |
| Project compilation | **Stable** | `--project src --out dist` | Mirrors the source directory tree; parallel compilation is enabled by default. |
| Watch mode | **Experimental** | `--watch` | Recompiles changed `.vela` files; it is not HMR. |
| Minification and DCE | **Stable** | `--minify` | Includes conservative dead-binding elimination in addition to JS minification. |
| Build mode | **Stable** | `--mode=development` (default) or `--mode=production` | Production builds stage a runtime with development warnings disabled. Independent of `--minify`. |
| DOM engine | **Experimental** | `--dom-engine=blocks` (default `legacy`) | Builds CSR components with the block runtime (`runtime-blocks.js`, docs/HYDRATION_ARCHITECTURE.md). It lowers every template form, including child components and slots, and interoperates with components compiled by the legacy engine. Known legacy-engine defects it does not have: no `{#each}` `{:else}`, slot content inside an `{#each}` row ignores state and item changes, a `<slot>` inside `{#if}` in a child fails at mount, and a row whose root is a component gets an extra `<div>` wrapper. `VELA_DOM_ENGINE=blocks` selects it for a whole process, for example a test run. |
| Input AST dump | **Stable** | `--ast` | Emits `<name>.ast.json` for the parsed SFC/template AST. |
| Generated JS AST dump | **Stable** | `--emit-gen-ast` | Emits `<name>.genast.json` for AST-codegen inspection. |
| Compiler trace | **Experimental** | `--trace=build.trace.jsonl`, `--trace=-` | Appends one source-free `CompilationTraceV1` JSON record per component. File output is safe for parallel project builds. With `--trace=-`, stdout is JSONL only and human progress moves to stderr. Schema version 1 is additive-only within Vela 1.x; consumers must ignore unknown fields. Disabled by default, so it adds no timing, allocation, or I/O cost to ordinary compilation. |
| Scope diagnostics | **Stable** | undeclared names, TDZ, redeclarations, invalid writes | Diagnostics use one-based UTF-16 positions; repeated template attributes keep their own source location. |
| Actionable Vela diagnostics | **Stable** | <code>VELA-*</code> code, <code>help:</code> correction, DiagnosticsJSON(diags) | Stable codes cover common Svelte-like mistakes; JSON is compact for editor and AI tooling. |
| Reactivity diagnostics | **Stable** | invalid derived writes, effects without reactive deps | Template references are also checked for undeclared names. |
| Flow diagnostics | **Stable** | code after `return`, `throw`, `break`, `continue` | Reports unreachable statements. |

## Not implemented

| Feature | Status | Notes |
| --- | --- | --- |
| SSR and hydration | **Experimental** | `--ssr` emits Go SSR renderers, marker/payload entries, and a narrow DOM-adopting client subset. Hydration currently supports one root element with recursively nested text interpolation, ordinary dynamic attributes, `class:` / `style:` directives, and events; static-branch `#if` / `#key` / `#each` at any element depth; unkeyed static/direct-text `#each`; a restricted keyed direct-text row; `<input>` / `<textarea>` `bind:value={exportedProp}`, checkbox `bind:checked={exportedBool}`, single-select `bind:value={exportedProp}` with explicit static option values, and `bind:this={reactiveRef}`; direct local `.vela` child components with ordinary one-way props, `@on:*` component events, `bind:value` to a `$bindable` child prop, a static fallback, and parent-projected direct text and recursively nested ordinary element slot content, plus top-level `#if` / `#key` / `#each` blocks within the existing static/direct-text subset are also adopted through their own hydrate entry when every child in the local component graph supports hydration. Nested or unsupported structural slot content, other component bindings, `<select multiple>`, `#await`, and general keyed rows remain unsupported. See `SSR.md`. |
| Source maps | **Experimental** | `--sourcemap` writes an external v3 `.js.map` for generated JS. Stable template expressions map directly to their individual source offsets; CSS mappings are not emitted. `--minify` is intentionally rejected. |
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
