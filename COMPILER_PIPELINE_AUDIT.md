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
