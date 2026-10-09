package main

// imports.go — import statement processing for the Vela compiler.
//
// Responsibilities:
//   1. Parse all import statements from the script section
//   2. Rewrite .vela import paths → .js (compiled output paths)
//   3. Emit the rewritten imports into the generated JS output

import (
	"path/filepath"
	"strings"
)

// ─────────────────────────────────────────────
//  Import types
// ─────────────────────────────────────────────

// ImportRecord represents one parsed import statement.
type ImportRecord struct {
	Raw   string   // original source line verbatim
	Names []string // exported names being imported
	Path  string   // import path as written
	Line  int      // 0-based line in the document
}

// ─────────────────────────────────────────────
//  Parsing
// ─────────────────────────────────────────────

// ParseImportRecords extracts all import statements from a script string.
// lineOffset is the 0-based line number where the script begins in the file.
//
// Uses the real JS parser (ParseJS) — this handles multi-line imports,
// mixed default-and-named forms, aliases, and imports inside/following
// comments, all of which the previous regex-based implementation either
// missed silently or tagged with the wrong `Raw` content.
func ParseImportRecords(script string, lineOffset int) []ImportRecord {
	return importRecordsFromProgram(script, ParseJS(script), lineOffset)
}

// importRecordsFromProgram derives import metadata from an already-owned
// program. The component pipeline calls this variant so import diagnostics and
// project staging do not parse the <script> section a second time.
func importRecordsFromProgram(script string, prog *JsProgram, lineOffset int) []ImportRecord {
	if prog == nil {
		return nil
	}
	var recs []ImportRecord
	for _, stmt := range prog.Body {
		d, ok := stmt.(*JsImportDecl)
		if !ok {
			continue
		}

		// Line number: count newlines in script before d.Pos.
		line := lineOffset
		for i := 0; i < d.Pos && i < len(script); i++ {
			if script[i] == '\n' {
				line++
			}
		}

		// Raw text (for downstream passes that want the exact source).
		end := d.End
		if end > len(script) {
			end = len(script)
		}
		raw := script[d.Pos:end]

		// Build the Names list as bound local names: default first, then
		// namespace (if any), then each specifier's local alias.  This
		// mirrors Import.Names (analyzer.go) so both sources of truth
		// agree; the old regex parser encoded namespaces as "* as X",
		// but no remaining consumer relies on that shape.
		var names []string
		if d.DefaultName != "" {
			names = append(names, d.DefaultName)
		}
		if d.Namespace != "" {
			names = append(names, d.Namespace)
		}
		for _, s := range d.Specifiers {
			names = append(names, s.Local)
		}

		recs = append(recs, ImportRecord{
			Raw:   raw,
			Names: names,
			Path:  d.Source,
			Line:  line,
		})
	}
	return recs
}

// ─────────────────────────────────────────────
//  Path rewriting
// ─────────────────────────────────────────────

func RewriteImportPath(importPath string) string {
	// Node module — pass through unchanged
	if !strings.HasPrefix(importPath, ".") && !strings.HasPrefix(importPath, "/") {
		return importPath
	}

	// .vela → .js
	if strings.HasSuffix(importPath, ".vela") {
		return strings.TrimSuffix(importPath, ".vela") + ".js"
	}

	// Already has a JS-compatible extension
	ext := strings.ToLower(filepath.Ext(importPath))
	if ext == ".js" || ext == ".mjs" || ext == ".cjs" || ext == ".ts" || ext == ".jsx" || ext == ".tsx" {
		return importPath
	}

	// Extensionless relative path → add .js
	if ext == "" {
		return importPath + ".js"
	}

	return importPath
}

// RewriteScriptImports parses every import statement in the script and
// rewrites its source path via RewriteImportPath.  Returns the script
// with only the path strings changed — whitespace, comments, and line
// breaks are preserved verbatim because we operate as a string splice
// rather than parse→emit.
//
// Handles multi-line imports, mixed default+named forms, aliases, and
// side-effect imports — all the shapes the old regex-based rewriter
// could not reach.
func RewriteScriptImports(script string) string {
	prog := ParseJS(script)
	if prog == nil {
		return script
	}
	// Collect (start, end, replacement) tuples for every path that needs
	// changing, then apply them right-to-left so earlier offsets stay
	// valid after later splices.
	type splice struct {
		start, end int
		text       string
	}
	var splices []splice
	for _, stmt := range prog.Body {
		d, ok := stmt.(*JsImportDecl)
		if !ok {
			continue
		}
		newPath := RewriteImportPath(d.Source)
		if newPath == d.Source {
			continue
		}
		// Find the source string literal within the statement's span.
		// It's the last quoted string between d.Pos and d.End.
		sPos, sEnd, ok := findLastStringLit(script, d.Pos, d.End)
		if !ok {
			continue // shouldn't happen for a well-formed import
		}
		quote := script[sPos : sPos+1] // preserve original quote style
		splices = append(splices, splice{
			start: sPos, end: sEnd,
			text: quote + newPath + quote,
		})
	}
	if len(splices) == 0 {
		return script
	}
	// Apply from the end so earlier offsets stay valid.
	var out strings.Builder
	out.Grow(len(script) + 16)
	prev := 0
	for _, s := range splices {
		out.WriteString(script[prev:s.start])
		out.WriteString(s.text)
		prev = s.end
	}
	out.WriteString(script[prev:])
	return out.String()
}

// findLastStringLit scans script[start:end] for the last quoted string
// literal and returns its full span (including the opening and closing
// quote).  Respects single-quote, double-quote, and backtick strings,
// plus backslash escapes.  Skips // line comments and /* block */
// comments so an import whose path lives after a trailing comment is
// still located correctly.
//
// Returns ok=false if no string literal is found in the span.
func findLastStringLit(script string, start, end int) (int, int, bool) {
	if end > len(script) {
		end = len(script)
	}
	bestStart, bestEnd := -1, -1
	i := start
	for i < end {
		c := script[i]
		switch {
		case c == '/' && i+1 < end && script[i+1] == '/':
			// line comment — skip to newline
			for i < end && script[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < end && script[i+1] == '*':
			// block comment — skip to */
			i += 2
			for i+1 < end && !(script[i] == '*' && script[i+1] == '/') {
				i++
			}
			i += 2
		case c == '"' || c == '\'' || c == '`':
			q := c
			lit := i
			i++
			for i < end {
				if script[i] == '\\' && i+1 < end {
					i += 2
					continue
				}
				if script[i] == q {
					i++
					bestStart, bestEnd = lit, i
					break
				}
				i++
			}
		default:
			i++
		}
	}
	if bestStart < 0 {
		return 0, 0, false
	}
	return bestStart, bestEnd, true
}

// Runtime module specifiers the generators emit. Project builds retarget
// them to the single copies at the output root (runtimeImportFor).
const defaultRuntimeImport = "./runtime.js"

// runtimeContractExport is the contract marker every generated module that
// uses the runtime imports (R-12.2d). Change its name whenever generated code
// starts to depend on a new or changed runtime API, so a stale runtime fails
// at module load with a missing-export error.
const runtimeContractExport = "$$runtimeContract1"

// runtimeExportNames returns the names a runtime module exports with
// `export function name` or `export const name`.
func runtimeExportNames(source []byte) map[string]bool {
	names := map[string]bool{}
	for _, line := range strings.Split(string(source), "\n") {
		for _, prefix := range []string{"export function ", "export const "} {
			if !strings.HasPrefix(line, prefix) {
				continue
			}
			rest := line[len(prefix):]
			end := strings.IndexFunc(rest, func(r rune) bool {
				return !(r == '$' || r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
			})
			if end > 0 {
				names[rest[:end]] = true
			}
		}
	}
	return names
}

// runtimeImportDecls splits runtime helper imports between the core runtime
// and the block runtime, keeping their order.
func runtimeImportDecls(specs []JsImportSpec) []JsStmt {
	var core, blocks []JsImportSpec
	for _, spec := range specs {
		if blocksRuntimeExports[spec.Imported] {
			blocks = append(blocks, spec)
		} else {
			core = append(core, spec)
		}
	}
	var decls []JsStmt
	for _, group := range []struct {
		specs  []JsImportSpec
		source string
	}{{core, defaultRuntimeImport}, {blocks, blocksRuntimeImport}} {
		if len(group.specs) == 0 {
			continue
		}
		decls = append(decls, &JsImportDecl{
			BaseStmt:   BaseStmt{BaseNode: genNode()},
			Specifiers: group.specs,
			Source:     group.source,
		})
	}
	return decls
}

// runtimeImportFor returns the relative module specifier from a generated
// module in fromDir to the runtime written at outRoot/runtime.js.
func runtimeImportFor(fromDir, outRoot string) string {
	rel, err := filepath.Rel(fromDir, filepath.Join(outRoot, "runtime.js"))
	if err != nil {
		return defaultRuntimeImport
	}
	rel = filepath.ToSlash(rel)
	if !strings.HasPrefix(rel, "../") {
		rel = "./" + rel
	}
	return rel
}

// finalizeRuntimeImports prepares the runtime imports of a generated module
// for output: a module that uses the runtime also imports the contract
// marker, and both runtime specifiers are retargeted to the copies next to
// runtimeSpecifier (empty means "./runtime.js"), so all modules of one build
// share one runtime instance.
func finalizeRuntimeImports(prog *JsProgram, runtimeSpecifier string) {
	if prog == nil {
		return
	}
	if runtimeSpecifier == "" {
		runtimeSpecifier = defaultRuntimeImport
	}
	dir := strings.TrimSuffix(runtimeSpecifier, "runtime.js")
	blocksSpecifier := dir + "runtime-blocks.js"
	var core *JsImportDecl
	usesRuntime := false
	for _, stmt := range prog.Body {
		imp, ok := stmt.(*JsImportDecl)
		if !ok {
			continue
		}
		switch imp.Source {
		case defaultRuntimeImport:
			usesRuntime = true
			if core == nil {
				core = imp
			}
			imp.Source = runtimeSpecifier
		case blocksRuntimeImport:
			usesRuntime = true
			imp.Source = blocksSpecifier
		}
	}
	if !usesRuntime {
		return
	}
	if core == nil {
		core = &JsImportDecl{BaseStmt: BaseStmt{BaseNode: genNode()}, Source: runtimeSpecifier}
		prog.Body = append([]JsStmt{core}, prog.Body...)
	}
	for _, spec := range core.Specifiers {
		if spec.Imported == runtimeContractExport {
			return
		}
	}
	core.Specifiers = append(core.Specifiers, JsImportSpec{Imported: runtimeContractExport})
}

// rewriteVelaSpecifiers rewrites `.vela` module specifiers of a plain
// JavaScript module to `.js`: static `import`/`export … from`, side-effect
// imports, and `import('…')` with a string literal. Only the specifier text
// changes; every other byte of the module is kept, so a project's .js files
// (an entry point importing App.vela) reach a bundler or the browser with
// imports that resolve to the compiled components.
func rewriteVelaSpecifiers(src string) string {
	lexer := NewLexer(src)
	var prev, prev2 string
	var out strings.Builder
	last := 0
	for {
		tok := lexer.Next()
		if tok.Kind == TokEOF {
			break
		}
		if tok.Kind == TokString && (prev == "from" || prev == "import" || (prev == "(" && prev2 == "import")) {
			raw := tok.Val
			for _, quote := range []string{"'", "\""} {
				if strings.HasSuffix(raw, ".vela"+quote) {
					out.WriteString(src[last:tok.Pos])
					out.WriteString(strings.TrimSuffix(raw, ".vela"+quote) + ".js" + quote)
					last = tok.End
					break
				}
			}
		}
		prev2, prev = prev, tok.Val
	}
	if last == 0 {
		return src
	}
	out.WriteString(src[last:])
	return out.String()
}
