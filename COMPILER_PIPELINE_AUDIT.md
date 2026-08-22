# Compiler pipeline audit

Status: baseline inventory for Task 011. This document records the actual
component path as of 2026-08-23. It is descriptive, not a claim that the
target ComponentAST architecture already exists.

## Scope and evidence

The inventory covers production compilation in main.go, parser.go,
analyzer.go, astgen.go, astvalidate.go, jsprinter.go, source-map output, and
the direct compiler helpers used by tests. Watch and project builds ultimately
use the same compileFile path. Debug AST output and targeted unit tests are
listed separately because they do not represent an additional production
backend.

## Actual production component path

```text
Vela file
  -> compileFile
  -> raw source buffer and DiagSet
  -> parseSFC
  -> SFC section strings and body offsets
  -> parseTemplate
  -> template node tree with cached expression ASTs
  -> ParseJS and script scope/validation passes
  -> analyzeScript
  -> Analysis metadata and rewritten-script metadata
  -> buildProgramCSS
  -> JsProgram
  -> ValidateProgram
  -> PrintJS or PrintJSWithLocations
  -> JavaScript file and optional source map or CSS file
```

### Entry points

| Entry point | Role | Converges at |
| --- | --- | --- |
| CLI compile mode | Compiles one or more explicit Vela files | compilePrint then compileFile |
| Project build | Discovers Vela files and mirrors output paths | buildProject then compileFile |
| Watch mode | Rebuilds changed files or projects | compileFile |
| Browser, corpus, source-map tests | Exercise real file and project compilation | compileFile or buildProject |
| Unit tests | Exercise parser, analyzer, AST builder, validator, and printer directly | intentionally bypasses file I/O |
| Debug flags | Emit parsed AST JSON or generated JS AST JSON | compileFile, but output is diagnostic only |

### Representation and ownership map

| Stage | Current representation | Owner | Source positions | Notes |
| --- | --- | --- | --- | --- |
| File input | raw string | compileFile and DiagSet | absolute file bytes | Retained for diagnostics and source-map sources. |
| SFC split | SFC with Script, Template, Style strings | parser | three body offsets | This is the main gap from the target ComponentAST. |
| Script parse | JsProgram | ParseJS caller | section-relative nodes | Parsed once by analyzeScript and again for scope diagnostics. |
| Template parse | node slice plus TemplateExpr cached JS ASTs | parseTemplate | template-relative positions | Template expressions already have cached JsProgram values. |
| Style | raw CSS string | SFC and CSS helpers | style body offset only | No CSS AST currently exists. |
| Analysis | Analysis metadata | analyzeScript and validators | mostly script-relative metadata | Contains reactive, prop, import, lifecycle, and dependency information. |
| Target generation | JsProgram | ASTGen | generated nodes plus selected user origins | Production client backend is direct AST-only. |
| Target validation | ValidationIssue slice | ValidateProgram | generated AST structure | Checks malformed nodes, references, lexical ordering, duplicates, and unreachable output. |
| Printer | JavaScript string | PrintJS or PrintJSWithLocations | printed locations | The only intended executable-JS serialization boundary. |

## Production branches

- Source-map mode builds the JsProgram explicitly, shifts user origins, then
  uses PrintJSWithLocations to collect mappings before writing the map.
- Generated-AST debug mode serializes the already-built JsProgram as JSON; it
  does not add a generator.
- Parsed-AST debug mode calls DumpAST, which currently re-enters SFC/template
  parsing for a diagnostic dump.
- Minify mode runs dead-code elimination over a script string and re-analyzes
  it before target generation. This is a deliberate audit hotspot because it
  crosses a text boundary after parsing.
- External CSS mode keeps the client JsProgram path unchanged and writes
  scoped CSS to a sibling file after JavaScript output.

## Baseline findings for follow-up audit work

1. The client target has one production backend: direct construction of
   JsProgram followed by ValidateProgram and printing. No legacy generator is
   reachable from compileFile.
2. The component does not yet have one owning typed AST. SFC is a text-section
   container; only the template has a dedicated node tree, and CSS remains
   textual.
3. Script parsing is duplicated in the normal path: analyzeScript parses for
   reactive analysis, then compileFile parses again for scopes and diagnostics.
   This is both a performance and a consistency review point.
4. The generated target AST has strong structural validation, but source input
   validation is split among parser errors, script scope passes, specialized
   Vela validators, and template-reference validation.
5. The printer is the intended string boundary. The next audit item must
   catalogue other code-generation string construction and distinguish it from
   safe data literals such as HTML or CSS payloads.
6. Tests intentionally use lower-level helpers for focused coverage. The
   audit must ensure these helpers do not grow alternative production semantics.

## Target path to evaluate

```text
immutable source buffer
  -> ComponentAST
     - ScriptAST
     - TemplateAST
     - CssAST
  -> component analysis and diagnostics
  -> target-independent render or semantic plan when justified
  -> client JsProgram or future SSR target AST
  -> target validation and optional optimization passes
  -> one printer per target
```

Raw source in this model remains available for error excerpts and source maps,
but is not an inter-stage executable representation.


## ComponentAST baseline and target contract

### Current SFC boundary

SFC is a section splitter, not a component AST. It owns three trimmed strings
and three offsets. The template is parsed later into nodes; script is parsed
independently by analysis and diagnostics; CSS is parsed only when scoping is
needed. This shape is simple, but permits independent reparsing and lets later
stages accept source fragments as inputs.

The audited consumers are production compilation, source-map emission, CSS
extraction, AST debug output, and a large number of focused tests. The high
consumer count means the first migration must preserve an adapter rather than
changing every generator/test signature at once.

### Existing typed material

| Section | Existing typed data | Important gap |
| --- | --- | --- |
| Script | JsProgram, scopes, Analysis, ScriptPlan | Analysis records several expressions and bodies as strings; compileFile parses script twice. |
| Template | Node tree and cached TemplateExpr JsProgram values | Several node fields still retain expression source strings; node source-span coverage is incomplete. |
| CSS | CssSheet from ParseCSS during scoping | CSS tokens and rules do not preserve source offsets; the stylesheet is retained as raw text. |

### Target contract

The target is a ComponentAST backed by one immutable SourceFile. SourceFile
contains the complete original bytes and line index and is the only long-lived
textual source owner. AST nodes refer to byte spans in SourceFile. Literal
values may remain strings as data, but no stage may pass a source fragment as
an executable program to a later stage.

```text
ComponentAST
  Source: immutable SourceFile
  Script: ScriptSectionAST
    Span, JsProgram, parse diagnostics
  Template: TemplateSectionAST
    Span, root template nodes, expression ASTs
  Style: CssSectionAST
    Span, CssSheet, parse diagnostics
```

The required invariants are:

1. Each present section has one body span within SourceFile; spans never overlap
   and retain original whitespace positions.
2. Script has exactly one parsed JsProgram per component revision. Analysis,
   scope building, rewriting, and diagnostics consume that program.
3. Every dynamic template expression has one parsed expression AST and a span
   pointing at the original expression rather than a reconstructed string.
4. CSS scoping consumes CssSheet and source spans; printing CSS is the only
   CSS-string serialization boundary.
5. Analysis records node references or spans plus semantic facts, not copied
   executable source bodies. A deliberate source snippet may be requested only
   from SourceFile for diagnostics.
6. Target generators accept ComponentAST plus analysis and emit only typed
   target ASTs. Printer functions are the final code serialization boundary.

### Proposed Go-facing shape

The exact names remain an implementation decision, but the migration target is
conceptually equivalent to:

```text
type SourceFile struct { Path; Bytes; LineIndex }
type Span struct { Start; End }
type ComponentAST struct { Source; Script; Template; Style }
type ScriptSectionAST struct { Span; Program; Diagnostics }
type TemplateSectionAST struct { Span; Roots; Diagnostics }
type CssSectionAST struct { Span; Sheet; Diagnostics }
```

SourceFile should be immutable after construction. Individual ASTs are mutable
only during their owning parse/lower phase; once analysis begins, downstream
passes treat them as immutable. Optimization produces a new target AST or an
explicit transformation result rather than mutating source-level nodes.

### Staged migration

1. Introduce ComponentAST and SourceFile beside SFC, then add a parseComponent
   entry point that creates section ASTs and exposes a compatibility adapter.
2. Change compileFile to parse script once, pass the same JsProgram into
   analysis and scope diagnostics, and retain source spans through source-map
   output.
3. Change template nodes so directive and attribute expression fields carry
   typed expression nodes plus spans; retain text only for literal payloads.
4. Add CSS token/rule spans and make scoped-CSS emission consume CssSheet.
5. Move AST debug output to serialize ComponentAST directly, avoiding a second
   parse through DumpAST.
6. Change generator and test helpers to accept ComponentAST and analysis.
7. Remove the SFC string fields and every compatibility adapter only after
   corpus, source-map, browser, and AST-validation coverage prove equivalence.

### Risks and non-goals

This task does not require full JavaScript type checking, CSS semantic
validation, or a generic lossless parser. The first milestone is representation
ownership and parse-once semantics. The raw source buffer stays intentionally
available for diagnostics, source maps, and literal recovery; the prohibition
is against using it as an untracked executable intermediate representation.


## Template and CSS parser audit

### Template coverage and current guarantees

The template parser covers ordinary elements and attributes, text interpolation,
components and slots, event and bind directives, class and style directives,
conditional blocks, keyed blocks, each blocks with empty branches, and await
branches. Dynamic text and attributes use cached TemplateExpr values backed by
the JavaScript parser. Corpus, generated-JS, runtime, source-map, and fuzz
coverage exercise many supported compositions.

The parser is fail-fast at the top level: a parse error returns no template
node slice to compileFile. It is not a recovery parser and does not return a
partial AST plus diagnostics. This is appropriate for code generation today,
but limits editor integration and multi-error diagnostics.

Source-position coverage is strongest for dynamic content: TemplateExpr, Part,
Attr, block condition, each iterator/key, and await expression fields have
section-relative positions. Source-map tests validate a representative set of
these origins. Element, component, slot, text-node, selector, and stylesheet
nodes do not have uniform whole-node spans. Several block and directive fields
also retain a string alongside a cached expression AST.

### CSS coverage and current guarantees

CSS parsing supports qualified rules, selector lists, declarations, common
at-rules, nested media-like blocks, keyframes as opaque raw content, important
flags, global selectors, pseudo-elements, and custom properties. ScopeCSS
parses into CssStylesheet and prints transformed selectors; current tests cover
those supported scoping cases.

ParseCSS is deliberately best-effort. It returns a stylesheet rather than an
error result, skips unexpected declaration tokens, silently discards malformed
qualified rules, and preserves keyframe content as raw text. CssTok and all
CssNode types lack source positions. Therefore CSS syntax errors cannot yet be
reported precisely and CSS AST cannot satisfy the proposed ComponentAST span
contract without a parser upgrade.

### Confirmed gaps

| Area | Current state | Required direction |
| --- | --- | --- |
| Template node spans | Dynamic subexpressions are positioned; structural nodes are not uniformly positioned. | Add Span to every node, attribute, branch, and text part. |
| Template recovery | First parser error aborts the tree. | Keep fail-fast compiler mode initially; later expose a recovery result for tooling. |
| Template structural validation | Parser and specialized validators share responsibility; no standalone tree validator exists. | Add ValidateTemplateAST before semantic analysis and generation. |
| Template expression fields | Cached AST exists, but source strings remain in many block/directive fields. | Make typed expression node plus span canonical; retain literal text only when semantically required. |
| CSS diagnostics | Best-effort parser silently recovers. | Return stylesheet plus diagnostics, with spans for tokens/rules/declarations. |
| CSS source maps | CSS output has no source maps and nodes have no spans. | Defer CSS source maps, but establish spans now for diagnostics and future mapping. |
| Parser fuzzing | Fuzzing targets valid templates and generator robustness. | Add malformed template/CSS fuzzing with no panic, bounded recovery, and stable diagnostics. |

### Required validation hooks

ValidateTemplateAST should run after parsing and before semantic analysis. It
must reject nil children, invalid or missing spans, invalid block branch shapes,
unparsed dynamic expressions, dangling terminators, invalid directive payloads,
and illegal parent/child relationships. It must operate on typed nodes only and
produce diagnostics in original component coordinates.

ValidateCssAST should run after ParseCSS. It must reject nil rules/selectors/
declarations, malformed brace structure, empty property names, invalid spans,
and any raw opaque region outside explicitly supported constructs such as
keyframes. Compiler mode should fail on CSS errors; an editor mode may preserve
a partial stylesheet with recoverable diagnostics.

### Test plan for the parser boundary

1. Add AST-invariant tests for every template node kind and for every CSS node
   kind, including span containment and non-nil child invariants.
2. Add negative fixture tables for mismatched tags, unclosed interpolations,
   malformed block terminators, invalid directive values, unterminated CSS
   strings/comments, missing braces, and malformed declarations.
3. Add source-location assertions for structural template and CSS diagnostics,
   not only for expression source maps.
4. Add parser fuzz targets that accept arbitrary component/template/CSS input,
   never panic, and produce bounded diagnostics or a valid AST.
5. Keep existing corpus, generated-JS, jsdom, Chromium, and source-map suites
   as downstream proof that parser changes preserve generated behaviour.


## JavaScript pipeline audit

### Parser coverage

The repository has an executable ECMAScript audit. On 2026-08-23 it reports
87 passing feature probes across ES5 through ES2020, including declarations,
control flow, classes, destructuring, modules, async and generators, optional
chaining, nullish operators, dynamic import, and numeric forms. This proves a
substantial parser subset and useful AST shapes; it is not proof of complete
ECMAScript conformance or semantic correctness for arbitrary programs.

A critical boundary defect is that the public ParseJS function returns only a
JsProgram. JSParser.Errors remains on the discarded parser instance. The
normal compileFile path parses script both for analysis and for scope work but
does not directly convert syntax errors from either parse into DiagSet
messages. TemplateExpr uses a separate parseTemplateExpression path that does
retain parser errors, so template expressions have a stronger syntax-diagnostic
contract than the script section.

Required remediation: replace ParseJS at compiler boundaries with a parse
result containing Program and structured diagnostics, preserve byte spans, and
make syntax errors stop code generation before analysis or rewriting. Keep a
small internal convenience wrapper only for tests that deliberately construct
valid compiler-authored snippets.

### Existing semantic analysis

BuildScopes provides real lexical scope construction and diagnostics for
unresolved identifiers, implicit global writes, const and import writes,
duplicate lexical declarations, a source-order TDZ approximation, destructured
bindings, catch bindings, and hoisting. DetectUnreachable adds local flow
warnings. Analysis recognizes Vela reactive declarations, props, imports,
derived values, effects, lifecycle hooks, dependency information, and ordered
script steps. Template-reference validation extends the visible-name model
into template scopes such as each, await, and keyed blocks.

The guarantees are intentionally narrower than a JavaScript type checker:

- TDZ analysis does not fully model initializer self-reference or arbitrary
  interprocedural execution order.
- Name resolution does not prove a called value is callable, validate arity,
  infer object shapes, or validate member accesses.
- Imported module exports and values supplied by runtime data are not typed.
- The default global list suppresses undeclared-name diagnostics for both
  browser and Node names. It does not prove that a browser API, method, or
  capability exists in the deployment environment.
- Effects, asynchronous control flow, aliasing, mutation, and external I/O
  are analyzed only to the degree required for Vela reactivity diagnostics.

### Rewriter and target AST boundary

Assignment rewriters use parsed JavaScript positions to locate and transform
reactive writes, including handlers and selected nested closures. Dedicated
tests cover concise arrows, blocks, compound assignments, patterns, and common
false positives. However, Analysis and several rewrite paths still keep source
snippets such as bodies or expressions as strings. The ComponentAST migration
must replace those with node references or spans wherever a later stage needs
executable semantics.

Generated output is represented as JsProgram and checked by ValidateProgram.
The validator catches malformed node shapes, undeclared generated references,
lexical-order and TDZ errors, duplicate declarations, and unconditional
unreachable code. It is a strong generator safety net, but it validates
compiler output rather than arbitrary user-script semantics.

The printer has round-trip and source-map coverage. It is the correct final
serialization boundary. There is nevertheless a second high-risk class of
boundary: ASTGen helper functions named jsStmts, jsStmt, and jsExpr construct
compiler-authored JavaScript as strings and parse it back into AST nodes. This
is safer than printing raw output, but still duplicates grammar/escaping work
and can panic after a string-construction defect. It belongs to the structural
string-emission catalogue in the next audit task.

### Browser API policy recommendation

Keep browser API analysis optional. The baseline compiler should diagnose only
facts it can prove syntactically: unknown global names under an explicit target
profile and unsupported direct API forms when a capability is disabled. A
future capability profile may distinguish browser, worker, Node, and SSR
contexts and add configured globals. It must not claim to verify arbitrary DOM
method/property availability without type declarations and flow-sensitive type
analysis.

### Required JS follow-up tests

1. Syntax-error fixtures for script and template expressions must assert the
   same component-relative Diagnostic format and must prevent target generation.
2. Parser audit tests must assert zero parser errors, not merely a non-nil AST.
3. Add a parser-error recovery fuzz target with bounded diagnostics and no
   panic; keep valid-input AST/printer round-trip fuzzing separately.
4. Add scope tests for aliases, module imports, nested functions, TDZ edge
   cases, and capability-profile globals.
5. Add generated-AST tests that prohibit new structural jsStmt and jsExpr use
   outside explicitly reviewed transitional helpers, once the string catalogue
   is complete.


## User JavaScript diagnostic policy

### Policy goals

Diagnostics must be deterministic, source-located, machine-readable, and
truthful about their proof strength. Existing DiagSet already provides
component-relative UTF-16 positions, severity, stable code support, suggestions,
and JSON output. The missing work is to route script parser errors through it
and to extend stable codes to high-value generic JS diagnostics.

A diagnostic is emitted only when the parser or analysis can prove a fact from
the current component and selected target profile. The compiler must not infer
runtime object shapes, network availability, browser implementation details, or
application-defined API contracts without an explicit type/capability source.

### Severity and code taxonomy

| Category | Default severity | Proposed code family | Policy |
| --- | --- | --- | --- |
| Script or template expression syntax | error | VELA-JS-SYNTAX | Stop generation; report the first parser location and retain later recovery diagnostics when available. |
| Unsupported but recognized syntax | error | VELA-JS-UNSUPPORTED | Never silently lower or reinterpret it. Explain the supported alternative when one exists. |
| Unresolved read | warning | VELA-JS-UNRESOLVED | Warn in normal mode; strict mode may elevate to error. Explicit profile globals suppress it. |
| Implicit global write | error | VELA-JS-IMPLICIT-GLOBAL | Always error for modules and components. |
| Write to const or import | error | VELA-JS-IMMUTABLE-WRITE | Preserve existing hard error behaviour. |
| TDZ violation proved by local order | error | VELA-JS-TDZ | State that analysis is local and conservative. |
| Duplicate lexical declaration | error | VELA-JS-DUPLICATE-BINDING | Preserve existing hard error behaviour. |
| Unreachable statement | warning | VELA-JS-UNREACHABLE | Do not reject a component solely for flow hygiene. |
| Proven non-callable direct callee | error | VELA-JS-NONCALLABLE | Initially only syntactic literals or framework intrinsics with fixed forms. |
| Framework intrinsic misuse | error | existing VELA family | Continue Vela-specific codes and canonical suggestions. |
| Profile-forbidden global | error | VELA-TARGET-GLOBAL | Error only when the selected target definitely excludes the global. |
| Optional capability unavailable | warning | VELA-TARGET-CAPABILITY | Warn only for direct known capability use configured as absent. |

Warning and hint diagnostics should gain WarnWith and HintWith equivalents to
ErrorWith before the generic code families are introduced. Existing un-coded
messages remain backward compatible until each has a tested stable code.

### Target and capability profiles

The compiler should accept an explicit profile rather than treating every known
global as universally available. The baseline profiles are browser, worker,
Node, and future SSR. Each profile supplies allowed globals and optional
capabilities such as DOM, storage, fetch, timers, WebSocket, IndexedDB, and
animation frames. Profile selection must be explicit in project configuration
or CLI options; the legacy permissive set remains the compatibility default
until a migration release documents a stricter default.

Examples of sound diagnostics:

- An SSR profile reports direct window or document usage as VELA-TARGET-GLOBAL.
- A worker profile reports document usage because the global is absent.
- A browser profile does not warn merely because document.querySelector is
  called; method existence and selector validity require a DOM/type model.
- A configured no-network environment may warn for direct fetch use as
  VELA-TARGET-CAPABILITY, while a dynamic alias cannot be proven and is left
  alone.

### Invalid calls and non-goals

The first implementation may diagnose only calls that are statically
non-callable, such as a literal number invoked as a function, and framework
intrinsic forms whose argument contract is defined by Vela. It must not treat
missing or extra arguments to ordinary JavaScript functions as errors because
JavaScript permits them, and it must not infer whether arbitrary members are
functions. Member, DOM, and browser API validation needs a future declaration
database and flow-sensitive type layer; that is outside this compiler audit.

### Delivery order

1. Introduce a script parse result and VELA-JS-SYNTAX diagnostics.
2. Assign stable codes to existing scope and flow diagnostics without changing
   their default severities.
3. Add strictness and target-profile plumbing with the legacy permissive
   profile as default.
4. Add direct-global checks for browser, worker, Node, and SSR profiles.
5. Add only proven intrinsic and literal-call diagnostics; defer general call
   validation until a separate type-analysis task exists.

Each step needs fixtures for human output, DiagnosticsJSON, UTF-16 positions,
source-map compatibility, and strict versus permissive behavior.


## Executable JavaScript string-boundary catalogue

### Classification

A string is **structural executable emission** when it describes JavaScript
grammar (a declaration, statement, expression, function, import, or closure)
and is then parsed to `JsProgram`/`JsExpr`, or otherwise reaches output without
an equivalent typed node. Re-labelling a parsed result `OriginGenerated` does
not make string construction typed construction: quoting, precedence, scope,
and grammar have already been duplicated outside AST constructors.

| Category | Definition | Required treatment |
| --- | --- | --- |
| Structural emission | Compiler concatenates JavaScript grammar, then calls `jsStmt`, `jsStmts`, or `jsExpr`. | Replace with typed AST builders. |
| User-source reparse | A later stage parses a user source slice or rewritten string instead of keeping its parsed node. | Replace with ComponentAST ownership or AST transforms; keep original spans. |
| Textual transform/reparse | An AST locates ranges but a transform splices text and reparses or reanalyses it. | Transitional only; make AST-native before another backend. |
| Safe literal payload | A string is data in a typed JS literal, such as escaped static HTML/CSS or an attribute value. | Keep only with one decoded-value encoder and escaping tests. |
| Printer boundary | `PrintJS` serializes validated target AST to the output file. | Retain as the sole final JS serialization boundary. |

### Active AST generator paths

`astgen.go` centralizes the reparse gateways in `jsStmts` (82), `jsStmt` (89),
and `jsExpr` (98). They call `ParseJS`/`parseJSExpr`, mark the result generated,
and return it as target AST. `ParseJS` currently discards parser errors, so a
bad compiler fragment may become a partial AST before validation can explain
the generator defect. Every active call-site group is catalogued below.

| Location/group | Constructed as JavaScript text | Classification and priority |
| --- | --- | --- |
| `finalizeRuntimeHelpers`, `buildImportDecls` (1308–1467) | Runtime import and `$$dispatch` declaration. | Structural; medium. Build import specifiers, arrow, and call directly. |
| `templateExpr`, `rewrittenTemplateExpr`, `templateStmt` (613–690) | Fallback expression, rewritten handler source, and marker-shell statements. | Mixed; high for rewritten handlers because they originate in template source. Cached normal expressions are already typed. |
| Script-plan/state/derived/effect/lifecycle builders (1526–1889) | State initializers, proxy callback, derived updater, effect IIFE, lifecycle bodies, and `$$upd_*` calls. `Initial`, `Expr`, `Body`, `CleanScript`, and `ScriptPlan.Source` contain user code. | **Critical user-source reparse.** Store parsed initializers, expressions, bodies, and statements in ScriptAST/Analysis; wrap typed children in generated AST. |
| Setter cascade and deep setters (1922–2155) | Scheduler calls, proxy assignment, notify call. | Structural compiler-only; high. Replace with call, assignment, declaration, guard, and block builders. |
| `cloneStmt` (2176–2180) | `PrintJS` of a statement followed by `jsStmt`. | **Critical printer-as-transform.** Replace with typed clone or immutable sharing; never use printer for cloning. |
| DOM roots, hole subtrees, each reconciliation (2187–3138) | DOM calls, anchors, row closures, maps, keyed reconciliation, and teardown. | Structural compiler-only; high because it controls nested-block correctness. Migrate using common control-flow/DOM builders. |
| Await lowering (3139–3247) | Driver function, promise chain, branch locals/updates. | Structural compiler-only; high after common builders. |
| `stmtsToText`, `nullCaptured`, `destroyComps`, `buildIfChainStructured` (3249–3497) | Existing AST statements are printed, joined into if/key/slot shells, then parsed again. | **Critical nested round trip.** Build blocks/functions/teardown as nested AST directly. |
| Element, child, text, slot, component lowering (3502–4185) | Element creation, append/write, fallback content, component props and slot factories. | Structural. `buildSlotClosures` is high priority because it combines printed captured AST with `strings.Builder`. |
| Attributes, events, bindings, HTML fill, return object (4194–4510) | Listeners, handler arrows, bindings, cleanup, template setup, prop setters. | Mixed. User handler rewriting is high priority; static HTML is a safe payload but its surrounding JS must be AST-built. |

`template_codegen_helpers.go` still has string-returning `bindAttrStmt`,
`classDirStmt`, and `styleDirStmt`. The AST production path uses
`attrWriteStmt`, `classDirStmtAST`, and `styleDirStmtAST` instead, so these are
historical/test helpers rather than active lowering. Deprecate and remove or
convert them during the builder migration to prevent a new backdoor.

### Other user-source and reparse boundaries

| Location | Current boundary | Required remediation |
| --- | --- | --- |
| `main.go: compileFile`, `analyzer.go` | Script text is parsed for analysis and parsed again for scopes and target planning. | Parse once into ScriptAST; analysis consumes the same immutable nodes. |
| `analyzer.go` derived/effect/lifecycle dependencies | `Expr` and `Body` strings are separately parsed for deps and later parsed again by ASTGen. | Store expression/body node references plus dependency metadata. |
| `RewriteAssignments`, `rewriteTemplateHandlerAssignments` in `rewriter.go` | AST positions choose edits, but replacements are strings and handlers are reparsed. | Implement AST assignment rewrite to setter-call nodes, preserving source origins. |
| `rewriteItemRef`, `rewriteIdentInExpr` | Identifier substitution splices source then handler lowering reparses it; one path is manual lexical scanning. | Use scoped AST substitution; no fallback-to-original after syntax diagnostics are available. |
| `EliminateDeadCode` in `dce.go` | AST ranges are textually removed, then code is reparsed/reanalysed to a fixed point. | Transitional minify-only path; replace with AST transform before SSR/HMR. |
| `imports.go` | Import specifier/path source is transformed textually and reparsed. | Modify/copy typed import nodes after ScriptAST ownership exists. |

### Literal payloads and valid serialization

`emitStaticHTML`, `buildHoleTemplate`, and `emitStaticHTMLNode` create HTML
data consumed by `innerHTML` or `<template>`; they are not JS grammar, but must
escape text and attribute values exactly once and receive DOM/XSS-shaped tests.
Scoped CSS and runtime style injection are likewise CSS data placed in a JS
literal. The surrounding assignments, calls, and declarations must still be
typed target AST nodes.

`strLit` currently accepts already-escaped content and stores raw quoted text;
alongside `jsonQuote`, `escapeJSString`, `backtickString`, `escapeAttr`, and
`escapeHTML*`, this is an encoding hot spot. The target literal builder accepts
decoded values and lets the JS printer own JavaScript escaping. `PrintJS` and
`PrintJSWithLocations` in `compileFile` are the intended final boundary only;
minification must operate on AST rather than create a second raw-JS path.

### Migration order and enforcement

1. Add typed builders plus an error-returning construction API; migrate
   compiler-only declarations, calls, assignments, blocks, DOM writes, cleanup,
   imports, and literals.
2. Retain parsed ScriptAST/template handler/effect/lifecycle nodes and insert
   them as typed children rather than interpolating source.
3. Remove `cloneStmt`, `stmtsToText`, and slot/if-chain print-reparse paths
   before growing nested blocks or adding SSR.
4. Convert assignment, item, import, and DCE transforms to AST-native passes.
5. Delete production `jsStmt`, `jsStmts`, `jsExpr`, and historical
   string-returning template helpers; test-only valid-input conveniences may
   remain under `_test.go`.

Add an architectural CI test that scans production Go files for `jsStmt`,
`jsStmts`, `jsExpr`, `ParseJS(PrintJS(`, and `stmtsToText`. It uses a small
explicit allowlist that must shrink with every migration. Pair it with AST
validation, malformed-literal tests, nested-block runtime fixtures, source-map
assertions for retained user nodes, and no-panic tests for each transitional
helper.


## Typed target-AST builder and validation contract

### Builder model

Task 012 must introduce a small composable builder layer rather than a second
large text-based DSL. Builders create existing `JsNode` structs directly and
are grouped by semantic purpose:

| Builder group | Required operations | Invariants at construction |
| --- | --- | --- |
| Values and references | decoded string/number/boolean/null literals, identifiers, arrays, objects, template literals, members | Identifier and property forms are explicit; literal builders receive decoded data; template `quasis == exprs + 1`; no pre-escaped JS source. |
| Expressions | call, construct, unary/binary/logical/conditional, assign, await, arrow/function | Required operands are non-nil, operator belongs to a fixed supported set, arrow body is expression or block, assignment target is assignable. |
| Statements and declarations | expression, variable, import, return, throw, if, loop, try, block, function/class | Declaration kind/name and import forms are valid; required bodies/tests exist; empty blocks and optional branches are explicit rather than represented by an empty string. |
| DOM and component operations | create element/comment/text/template, append/before/remove, attribute/property/class/style write, component mount/set/destroy | DOM names and data arguments are decoded literals; generated identifiers come from one allocator; no selector/tag/attribute is interpolated into JS grammar. |
| Events, bindings, cleanup | handler arrow, listener add/remove pair, `bind:*` read/write, cleanup registration | Add/remove use the same target/event/handler identity; handler body has template origin; cleanup ownership is explicit and emitted exactly once. |
| Reactive scheduling | state setter, deep-proxy notifier, derived updater, effect runner, structural update/defer queue | Setter declaration precedes every generated read, update statements are cloned/shared safely, and structural updates are scheduled before interior DOM writes. |

A `GenBuilder`/`BuildContext` owns the current lexical target scope, a
monotonic deterministic generated-name allocator, current source-origin
context, runtime-helper requirements, and cleanup/update collections. It must
not own mutable source text. A builder returns `(JsNode, error)` or records a
structured internal construction diagnostic; `panic` is reserved for violated
compiler assertions after the diagnostic path has been exhausted.

Generated wrapper nodes use `OriginGenerated`. A retained user subtree keeps
its original `OriginUser` and original section-relative span; a wrapper or
DOM/event operation caused by template syntax receives the exact template
source origin. A child is either immutable and safely shared or deep-cloned by
an AST-native clone utility. It is never cloned by printing and reparsing.

### Literal and ownership conventions

`JsLiteral.Raw` is an insufficient long-term generated-literal API because it
requires callers to know printer quoting rules. Add a decoded literal value
representation (or a generated-only literal constructor whose printer encodes
that value) before migrating call sites. Raw source remains permitted only for
parser-owned user literals and explicitly documented opaque syntax, never for
new generated values.

User AST is immutable after parsing. Analysis may attach side metadata keyed by
node identity/span, but must not rewrite parser nodes in place. A transform
returns a new tree that shares unchanged user subtrees and creates generated
replacement/wrapper nodes. This preserves source maps, makes repeated use of a
template expression safe, and lets client and future SSR generators consume one
ComponentAST without hidden mutation.

### Validation layers

1. **Construction validation** is local and immediate: nil required child,
   illegal operator/kind, invalid identifier/import form, impossible
   template-literal arity, duplicate cleanup token, or scope/name-allocation
   misuse returns a builder diagnostic at the operation that created it.
2. **Target-AST invariant validation** runs after each generator stage. Extend
   `ValidateProgram` beyond its present empty-node checks to validate every
   statement/expression form, program/import/module constraints, literal
   encoding state, block/branch shape, source-origin consistency, and
   generated-versus-user ownership rules.
3. **Whole-program semantic validation** retains undeclared generated
   references, lexical order/TDZ, duplicate declarations, and unreachable
   generated output. It runs after the complete target program is assembled
   and before either normal printing or source-map printing.

Validation must report structured internal errors rather than only build a
string and panic in `buildProgramWithInfo`. Compilation surfaces those errors
as compiler failures with the involved builder operation and source origin;
tests may still fail immediately with the rendered diagnostic. `PrintJS` is
called only after all three layers succeed.

### Required test matrix and migration guardrails

- `codegen_string_boundary_test.go` is the initial architectural baseline. It
  parses `astgen.go`, snapshots every `jsStmt`/`jsStmts`/`jsExpr` call by
  owning function, and forbids such calls from any other production Go file.
  Task 012 must lower this inventory monotonically and finally replace it with
  an empty-inventory assertion.
- Add focused unit tables for every builder: valid node shape, decoded-literal
  escaping, each rejected invariant, generated name/scope lifetime, event
  add/remove identity, cleanup exactly-once, and scheduling order.
- Keep malformed manually-built AST tests for every validator rule, then add
  a builder test proving invalid structures cannot normally be constructed.
- Compile corpus and fuzz fixtures through builder stages; assert a validated
  AST, deterministic printer output, no panic, and retained source-map origins.
- Exercise nested each/if/key/await/slot fixtures in jsdom and Chromium after
  every control-flow migration, including branch teardown, route transition,
  handler assignment, and deep-state updates.

### Staged implementation boundary

The first implementation should not redesign all `JsNode` types at once.
Start with decoded literals, references/calls/assignments/blocks/declarations,
then DOM/event/cleanup/scheduler builders. Migrate compiler-only structures
first. Only after the builders and validator contract are stable should Task
013 insert retained user ScriptAST/template-handler children and replace
textual transformations. This keeps each change observable through the
inventory test and avoids mixing a semantic source rewrite with a structural
codegen rewrite.


## Optional compiler trace design

### Decision

A trace is an opt-in observation of the existing `compileFile` pipeline, not a
new compilation path, cache, AST dump, or diagnostic format. It is disabled by
default: the normal compiler performs no trace allocation, clock read, source
retention, or output write. When enabled, it reports one bounded record per
component compilation and does not change generated JS, CSS, source maps,
diagnostic order, or failure semantics.

The current stage boundaries are stable enough for an initial contract:

```text
read-source
  -> parse-sfc
  -> parse-template
  -> parse-script-and-analyze
  -> validate-input
  -> optimize              (skipped unless enabled)
  -> build-target-ast
  -> validate-target-ast
  -> print-target
  -> build-source-map      (skipped unless enabled)
  -> write-output
```

`parse-script-and-analyze` remains one trace stage until Task 013 supplies a
single owned ScriptAST. The trace may expose its internal parse count as a
metric today, but must not pretend that duplicate parsing is a cache hit.
`optimize`, source-map, and debug-AST steps are explicitly marked skipped when
the corresponding option is off. This makes comparisons between normal,
minified, and source-map builds meaningful.

### Public data model

Use a versioned, machine-readable JSON Lines format: one self-contained
`CompilationTraceV1` object per source component. JSONL avoids cross-file
ordering assumptions in parallel project builds and permits watch mode to
append a new record for each rebuild.

```text
CompilationTraceV1
  schemaVersion: 1
  component: { id, displayName }
  outcome: success | failed | internal-error
  options: { minify, sourceMap, cssMode, debugAst }
  stages: [ StageTrace ]
  diagnostics: { errors, warnings, hints, byCode? }
  metrics: { sourceBytes, templateNodes, scriptNodes, cssNodes,
             targetNodes, outputBytes, cssBytes, sourceMapBytes,
             scriptParseCount }

StageTrace
  name: stable stage name above
  status: complete | skipped | failed
  durationNs: non-negative integer
  metrics: optional stage-local numeric aggregates
  cache: { decision: not-applicable | hit | miss | bypassed,
           reason?: stable reason code }
```

`id` is a stable, project-relative slash-normalized component identifier when a
project root is known. File-mode uses a cleaned configured source identifier,
not an absolute resolved path. `displayName` is the component name only. The
trace contains no raw source, source snippets, AST JSON, generated JavaScript,
CSS text, user identifier inventory, diagnostic message text, absolute paths,
wall-clock timestamps, or hash of source content. Diagnostic codes may be
counted only after the stable-code migration; before that, report severities
without fabricating code keys.

Durations use monotonic elapsed `time.Duration` values serialized as integer
nanoseconds. The record has no start timestamp, avoiding both privacy leakage
and meaningless wall-clock comparison. Node/output counts are aggregates only;
missing AST classes report zero and a stage that failed before creating its AST
omits the corresponding metric rather than retaining a partial tree.

### API and CLI shape

Add an optional `TraceSink` to `CompileOptions`, not a package-global collector:

```go
type TraceSink interface { Record(CompilationTraceV1) error }
```

`compileFile` creates a local recorder only when `opts.TraceSink != nil`, emits
its finalized record with `defer`, and ignores sink failures only if the caller
explicitly requests best-effort tracing. CLI `--trace=<file>` opens a
mutex-protected append-only JSONL sink once per command; a sink failure is a
separate tooling error and must not corrupt the compiled component output.
`--trace=-` writes JSONL to stderr so normal status lines on stdout remain
readable. Watch mode appends a record for every recompile. A future in-memory
sink supports unit tests and embedding APIs without filesystem writes.

Timing helpers are local start/finish calls at the named boundaries. They must
not wrap individual AST nodes, retain section strings, or change branch order.
A trace therefore adds O(number of stages) allocations and clock reads only
when enabled; the disabled branch is a nil check.

### Cache decisions and future use

There is no component-AST, analyzer, or target-AST cache today. Every stage
must therefore record `not-applicable`, never a synthetic miss. Once a cache
exists, its boundary must record `hit`, `miss`, or `bypassed` with a stable
reason code such as `source-unchanged`, `option-changed`, `dependency-changed`,
or `cache-disabled`. The record may reveal that a cache was used, but not cache
keys, source hashes, or source-derived contents.

This trace supports later comparison of template complexity, parser work,
target-AST size, print time, and output size across client and SSR backends.
It is not a profiler and must not be used to decide optimization heuristics in
the same compilation; decisions remain deterministic inputs to trace metrics.

### Test and compatibility contract

1. With tracing disabled, compile output, source map, diagnostics, and normal
   API results are byte-for-byte unchanged.
2. With an in-memory sink, every successful compilation has exactly one record,
   stable stage order, one terminal outcome, non-negative durations, and
   internally consistent counts.
3. Failure fixtures stop at the actual failing stage, retain prior completed
   stages, have no later complete stages, and contain diagnostic counts but no
   source text.
4. Parallel project and watch tests prove records are individually valid JSON
   and no records interleave; their global order is deliberately unspecified.
5. Snapshot tests validate schema version and stable enum values, while timing
   values are checked only for range/non-negativity.

The next subtask may prototype this behind the disabled-by-default sink API.
It should begin with an in-memory test sink and single-file JSONL CLI output;
no cache instrumentation is included until cache ownership exists.


### Prototype status (2026-08-23)

The disabled-by-default API prototype is implemented. `CompileOptions.TraceSink`
receives one `CompilationTraceV1` value per `compileFile` invocation. It records
source read, SFC/template/script analysis, input validation, optional
optimization, separately measured target construction and target validation,
printing, optional source-map construction, and output write. A stage that is
not enabled is retained with `skipped` status and a stable reason; there are no
synthetic cache misses.

The prototype uses no trace recorder when `TraceSink` is nil. With a sink, it
records aggregate byte/node counts, the currently observed two script parses,
severity/code counts, non-negative monotonic durations, and success/failed/
internal-error outcome. Its in-memory tests prove generated JavaScript is
unchanged, success records have the documented stage order, failures stop at
the failing stage, and JSON serialization contains neither source text nor the
temporary absolute path.

The public CLI flag and mutex-protected append-only JSONL file sink are
intentionally deferred. The API gives embedding tools a stable observation
point first; a later tooling task can add `--trace=<file>` or `--trace=-`
without changing compiler semantics or the record schema.


## Test matrix and confirmed remediation

`COMPILER_TEST_MATRIX.md` is the maintained executable-coverage map for this
audit. It links every current compiler stage to focused unit/invariant tests,
corpus/generated-JS proof, jsdom runtime fixtures, Chromium checks, and
source-map tests. It also records where an external tool causes a deliberate
local skip rather than falsely claiming coverage.

The audit has produced the following independently executable remediation
sequence:

1. **Task 012** replaces structural executable-JS string emission, print/reparse
   cloning, and nested string assembly with typed target-AST builders.
2. **Task 013** introduces parse-once ScriptAST ownership and AST-native
   assignment, handler, item, import, and DCE transforms.
3. **Task 014** completes template/CSS AST spans, diagnostics, and validation
   around the outer ComponentAST boundary.
4. **Task 015** implements optional target-profile JavaScript diagnostics after
   the parse-once script path is available.
5. **Task 016** exposes the completed trace API through a CLI JSONL sink.

Tasks 007 (SSR/hydration) and 008 (dev server/HMR) now depend on 012, 013, and
014 as well as this audit. The first three are mandatory architecture work:
without them, a second backend or persistent incremental compiler state would
inherit the same text intermediates and unvalidated structural paths. Target
profiles and trace CLI delivery remain useful but intentionally non-blocking.


### Final audit validation

The trace prototype code phase passed `go test ./... -count=1` in 55.931s.
The final explicit Chromium runtime and browser-source-map selection passed
`go test ./... -run "TestBrowser(Runtime|SourceMap)" -count=1` in 10.195s.
TaskMD validation accepted all 16 tasks. Fuzzing remains a bounded manual/CI
job documented in the test matrix rather than an implicit requirement of the
ordinary Go test command.
