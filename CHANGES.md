# Vela Compiler — Patch Set

All changes are backward-compatible drop-ins.  
Copy files into the compiler directory and rebuild: `go build -o vela .`

---

## Files

| File | Action | Fixes |
|---|---|---|
| `codegen.go` | **Replace** your existing `codegen.go` | #1 #2 #3 |
| `imports.go` | **New file** — drop into compiler directory | #4 |
| `cssscoper.go` | **New file** — drop into compiler directory | #5 |
| `cssscoper_test.go` | **New file** — tests for cssscoper | — |
| `patch_cssparser.py` | **Run once** to fix `cssparser.go` | #5 |

---

## Fix #1 — Duplicate imports in generated JS

**Problem:** Import statements appeared twice in compiled output — once at the
top of the file (correct) and again inside the component function body
(syntax error: `import declarations may only appear at top level`).

**Root cause:** `CleanScript` (the script section with reactive declarations
stripped) still contained import lines. The `// component logic` loop was
emitting them verbatim inside `export function`.

**Fix in `codegen.go`:** The `CleanScript` emission loop now skips any line
whose trimmed form starts with `import ` or `import{`:

```go
if strings.HasPrefix(trimmed, "import ") || strings.HasPrefix(trimmed, "import{") {
    continue
}
```

---

## Fix #2 — Component props not updated inside `{#if}` branches

**Problem:** A component inside `{#if isAuthenticated}` like
`<Dashboard :sidebarOpen="sidebarOpen" :user="user" />` never received prop
updates when `sidebarOpen` or `user` changed. Only re-mounting (condition
change) would show fresh values.

**Root cause:** `emitBranchComponent` created the component with a local
`const wrapVar` and discarded the constructor return value. No `g.addUpdate`
registrations were made, so setters had no way to call `.set()` on the instance.

**Fix in `codegen.go`:** `emitBranchComponent` now:
1. Declares `let wrapVar = null` and `let instVar = null` at **component scope** (`g.create`)
2. Stores the constructor return: `instVar = Dashboard(wrapVar, props, {})`
3. Registers `g.addUpdate` for every `:bind` prop:
   `if (instVar) instVar.set({ sidebarOpen: sidebarOpen })`
4. On branch removal, calls `instVar.destroy()` before nulling
5. Returns the declared var names so `walkIfChain` nulls them on deactivation

Generated before/after for `<Dashboard :sidebarOpen="sidebarOpen" />`:

```js
// BEFORE — prop never updates
const $$n13 = document.createElement('div');
Dashboard($$n13, { sidebarOpen: sidebarOpen }, {});

// AFTER — prop updates reactively
let $$n13 = null;           // component scope
let $$n13_comp = null;      // component scope
// in branch creation:
$$n13 = document.createElement('div');
$$n13_comp = Dashboard($$n13, { sidebarOpen: sidebarOpen }, {});
// in $$set_sidebarOpen:
$$defer(() => { if ($$n13_comp) $$n13_comp.set({ sidebarOpen: sidebarOpen }); });
// in branch removal:
if ($$n13_comp) { $$n13_comp.destroy(); $$n13_comp = null; }
$$n13 = null;
```

---

## Fix #3 — Slot components recreated on every modal reopen

**Problem:** Opening a Modal, interacting with a form inside it, closing, and
reopening — the form was reset. Every reopen called the slot function which
built a fresh `DocumentFragment`, creating new child component instances.

**Root cause:** Slot functions had no caching — every call rebuilt from scratch.

**Fix in `codegen.go`:** Each slot function now has a companion cache variable
`$$sc_slotName_N` declared at component scope (null = not yet built):

```js
// Generated slot function (pseudo-code):
"default": () => {
  if ($$sc_default_n5) {
    // Reopen: re-run chain/each inits so detached branches re-insert
    $$chain_$$n7();
    return $$sc_default_n5;   // same DOM nodes — component state preserved
  }
  // First open: build fragment, cache it
  const $$sf = document.createDocumentFragment();
  $$sf.appendChild($$n6);
  $$chain_$$n7();
  $$sc_default_n5 = $$sf;
  return $$sc_default_n5;
}
```

---

## Fix #4 — Import path rewriting (`imports.go`)

**New file.** Provides:

- `ParseImportRecords(script, lineOffset)` — parses all import statements
- `RewriteImportPath(path)` — `.vela` → `.js`, extensionless → `.js`, modules unchanged
- `RewriteImportLine(line)` — rewrites one import line in-place
- `RewriteScriptImports(script)` — rewrites all imports in a script section

Integration in `codegen.go` / `analyzer.go`:

```go
// In analyzeScript() — add to ScriptAnalysis:
ImportRecords []ImportRecord

// Populate:
a.ImportRecords = ParseImportRecords(src, 0)

// Already handled: CleanScript loop skips import lines (Fix #1)
// Imports are emitted at top of file via g.analysis.Imports
```

---

## Fix #5 — CSS scoping bugs (`cssscoper.go` + `patch_cssparser.py`)

**Problems:**
- `:root { --var: value }` compiled to `[vela-x]:root { ... }` — unmatchable, CSS vars never applied
- `body { ... }` scoped when it should be global
- `.card:hover` produced `.card:hover[vela-x]` (attribute after pseudo) instead of `.card[vela-x]:hover`

**New file `cssscoper.go`** provides `scopeOneSel(selector, scopeID)` which:
- Passes `:root`, `html`, `body`, `*`, `@keyframes` etc. through unchanged
- Unwraps `:global(body.dark)` → `body.dark` (unscoped)
- Handles inline `:global()` in compound selectors
- Inserts `[scope]` before the first pseudo (`:hover`, `::before`) not inside `()`

**Apply fix:** run `patch_cssparser.py` once from the compiler directory:

```bash
python3 patch_cssparser.py cssparser.go main.go
go build -o vela .
```

The script appends the helpers and replaces the scoping expression in `cssparser.go`
with a call to `scopeOneSel()`. Manual instructions are printed if auto-patch fails.

**Scope ID portability:** The script also prints instructions for changing the
hash input from the absolute path to a relative path, so scope IDs are stable
across machines and deployment environments.

---

## Rebuild

```bash
# Copy files into your compiler directory
cp codegen.go imports.go cssscoper.go cssscoper_test.go /path/to/vela/

# Apply CSS fix
cd /path/to/vela
python3 patch_cssparser.py

# Build
go build -o vela .

# Run tests
go test ./...
```
