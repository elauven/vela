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
