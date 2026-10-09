# Vela

> A UI framework compiled by Go — from the backend to the frontend.

Write breathtakingly concise components in HTML, CSS, and JavaScript you
already know. A Go compiler turns them into surgical, minimal vanilla JS —
no virtual DOM, no runtime overhead, just direct DOM mutations determined
entirely at compile time.

```
your .vela file          →  Go compiler  →  tiny .js module
html + css + js              (offline)       no framework in sight
```

---

## Quick start

```bash
# Build the compiler
git clone https://github.com/yourname/vela
cd vela
go build -o vela .

# Compile a component
./vela --out dist examples/Counter.vela

# Watch mode
./vela --out dist --watch examples/*.vela
```

Serve with any static file server — no bundler required.

---

## Single-File Component syntax

Every `.vela` file has three sections:

```html
<script>
  <!-- Reactive JavaScript -->
</script>

<style>
  /* Scoped CSS — automatically namespaced, never leaks */
</style>

<template>
  <!-- Your HTML with a thin layer of reactivity sugar -->
</template>
```

---

## State, derived & effects

### `$state(initial)` — reactive variable

```js
let count  = $state(0);
let name   = $state('world');
let active = $state(false);
let items  = $state([]);
```

The compiler replaces every **assignment** to a state variable with a call to
a generated setter that surgically updates every DOM node that depends on it.

```js
// you write:
count++;
count = 42;
count += step;

// compiler emits something like:
$$set_count(count + 1);
$$set_count(42);
$$set_count(count + step);
```

### `$derived(expr)` — computed values

```js
let doubled   = $derived(count * 2);
let fullName  = $derived(`${first} ${last}`);
let filtered  = $derived(items.filter(i => !i.done));
```

Derived values are recomputed **inline** inside each setter that touches their
dependencies — zero overhead, zero subscription machinery.

### `$effect(() => { ... })` — side effects

```js
$effect(() => {
  document.title = `${count} items`;
  localStorage.setItem('count', count);
});
```

Effects are wrapped in a named function and called from every setter whose
state variables appear in the effect body. Dependency tracking is done at
compile time via identifier analysis.

---

## Template syntax

### Text interpolation `{expr}`

```html
<p>Hello, {name}!</p>
<p>You have {remaining} {remaining === 1 ? 'item' : 'items'}</p>
```

Every `{expr}` compiles to a **`Text` node** with a direct `.data` update in
the setter — no innerHTML, no re-renders.

### Dynamic attributes `:attr={expr}`

```html
<input :value={count} />
<div :class={active ? 'on' : 'off'}>...</div>
<a :href={url}>link</a>
```

### Event listeners `@event={handler}`

```html
<button @click={increment}>+</button>
<input @input={(e) => name = e.target.value} />
<form @submit={(e) => { e.preventDefault(); submit(); }}>
```

### Conditional rendering `{#if expression}`

```html
{#if count > 0}
  <p>You have {count} items</p>
{:else if error}
  <div class="alert">{error}</div>
{/if}
```

The element is created/destroyed by an anchor comment node. The condition is
re-evaluated inside every setter whose variables appear in the expression.

### List rendering `{#each array as item, index (key)}`

```html
{#each todos as todo, i (todo.id)}
  <li>{i + 1}. {todo.text}</li>
{:else}
  <li>No todos.</li>
{/each}
```

The index and key are optional. Use a stable key for reorderable lists.
Element-level `#if`, `#elseif`, `#else`, `#each`, and `#key` directives are
not part of Vela; control flow is expressed only with blocks.

---

## Stores

Stores let you share reactive state between components.

```js
import { writable, readable, derived } from 'vela/store';
```

### `writable(initial)`

```js
// store.js
export const count = writable(0);

// ComponentA.vela
import { count } from './store.js';
count.subscribe(v => { /* ... */ });
count.set(42);
count.update(n => n + 1);
```

### `readable(initial, start?)`

```js
const time = readable(new Date(), set => {
  const id = setInterval(() => set(new Date()), 1000);
  return () => clearInterval(id);
});
```

### `derived(stores, fn)`

```js
const doubled = derived(count, n => n * 2);
const sum     = derived([a, b], ([a, b]) => a + b);
```

---

## Mounting components

```html
<script type="module">
  import { Counter } from './dist/Counter.js';

  // Mount returns a handle
  const app = Counter(document.getElementById('app'));

  // Update props programmatically
  app.set({ count: 10 });

  // Tear down cleanly
  app.destroy();
</script>
```

---

## What the compiler produces

Given this component:

```html
<script>
  let count = $state(0);
  let doubled = $derived(count * 2);
  function increment() { count++; }
</script>

<template>
  <div>
    <p>{count} × 2 = {doubled}</p>
    <button @click="increment">+</button>
  </div>
</template>
```

The compiler emits **zero-overhead vanilla JS**:

```js
export function MyComp($$target, $$props) {
  let count = 0;
  let doubled;
  const $$upd_doubled = () => { doubled = count * 2; };

  // DOM created once, never recreated
  const $$root = document.createElement('div');
  const $$p    = document.createElement('p');
  const $$txt  = document.createTextNode('');   // ← only this node updates
  const $$btn  = document.createElement('button');
  $$p.appendChild($$txt);
  $$btn.appendChild(document.createTextNode('+'));
  $$root.appendChild($$p);
  $$root.appendChild($$btn);

  function increment() { $$set_count(count + 1); }

  // Setter knows at compile time: update doubled, then update $$txt
  function $$set_count($$v) {
    count = $$v;
    $$upd_doubled();
    $$txt.data = `${count} × 2 = ${doubled}`;
  }

  $$btn.addEventListener('click', increment);

  $$upd_doubled();
  $$txt.data = `${count} × 2 = ${doubled}`;
  $$target.appendChild($$root);

  return {
    destroy() {
      $$btn.removeEventListener('click', increment);
      $$root.remove();
    }
  };
}
```

**No framework runtime. No virtual DOM. No diffing. No reactivity system.**
Every update path is a straight line of direct DOM mutations, known at build
time.

---

## Scoped CSS

All CSS is automatically scoped to the component using a unique attribute
selector derived from a hash of the source path:

```css
/* you write */
button { background: blue; }

/* compiler emits */
button[vela-4a1b2c] { background: blue; }
```

The matching attribute `vela-4a1b2c` is stamped on every element the
component creates. Styles from one component can never bleed into another.

---

## Architecture

```
vela/
├── main.go        CLI entry point, file loop, watch mode
├── parser.go      SFC extractor + recursive-descent HTML parser → AST
├── analyzer.go    Script analysis: $state / $derived / $effect / imports
├── astgen.go      direct JsProgram builder
├── jsprinter.go   validated JavaScript AST printer
├── template_codegen_helpers.go shared template lowering helpers
├── runtime/
│   └── runtime.js Browser runtime: scheduler, stores, hydration, and compiler helpers
└── docs/          Documentation (see the Documentation section)
```

The compiler pipeline:

```
.vela source
    │
    ▼
parseSFC()          extract <script> <style> <template>
    │
    ├──► parseTemplate()   recursive-descent HTML → []Node AST
    │        TextNode / ElementNode (#if #each :bind @event)
    │
    ├──► analyzeScript()   regex-based extraction of reactive primitives
    │        StateVar / DerivedVar / Effect / Import
    │        + dependency tracking via identifier analysis
    │
    └──► buildProgram()      direct JsProgram construction
             validation — generated AST invariants
             printing    — ES module output with source origins
             setters     — direct DOM updates for reactive bindings
```

---

## Comparison

| Feature           | Vela        | Svelte      | Vue         | React       |
|-------------------|-------------|-------------|-------------|-------------|
| Compiler          | Go (fast)   | JS/TS       | JS/TS       | Babel/SWC   |
| Runtime JS        | ~120 lines  | ~15 kB      | ~45 kB      | ~42 kB      |
| Virtual DOM       | ✗           | ✗           | ✗ (v3+)     | ✓           |
| Scoped CSS        | ✓           | ✓           | ✓           | CSS Modules |
| Reactive stores   | ✓           | ✓           | Pinia       | Zustand     |
| SFC format        | ✓           | ✓           | ✓           | JSX         |
| No build on edit  | watch mode  | watch mode  | watch mode  | HMR         |

---

## Documentation

| Document | Contents |
| --- | --- |
| [docs/FEATURES.md](docs/FEATURES.md) | Current feature status and known limits |
| [docs/GUIDE.md](docs/GUIDE.md) | Authoring guide with examples |
| [AGENTS.md](AGENTS.md) | Authoring rules for people and AI agents |
| [docs/VELA.md](docs/VELA.md) | Compiler and runtime reference |
| [docs/SSR.md](docs/SSR.md) | SSR product design |
| [docs/SSR_TRANSLATION_CONTRACT.md](docs/SSR_TRANSLATION_CONTRACT.md) | JavaScript-to-Go translation and Go host implementations ([ru](docs/SSR_TRANSLATION_CONTRACT.ru.md)) |
| [docs/SSR_TRANSLATION_MATRIX.md](docs/SSR_TRANSLATION_MATRIX.md) | Per-construct translation status |
| [docs/HYDRATION_ARCHITECTURE.md](docs/HYDRATION_ARCHITECTURE.md) | Block runtime and universal hydration ([ru](docs/HYDRATION_ARCHITECTURE.ru.md)) |
| [docs/archive/](docs/archive/) | Historical documents |

Planned work is tracked as [taskmd](https://github.com/driangle/taskmd)
tasks in [tasks/](tasks/).

## License

[MIT](LICENSE). The Go SSR runtime (`ssr_runtime/`) and the npm package
`@vela/runtime` are released under the same license.

---

*This is an attempt to rethink what SSR could be like*
