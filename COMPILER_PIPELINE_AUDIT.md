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
