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

// RewriteImportPath rewrites an import path for the compiled JS output.
//
// Rules applied in order:
//  1. Node module (no leading ./ or /) → unchanged
//  2. .vela extension → replace with .js
//  3. Already has .js/.mjs/.cjs/.ts extension → unchanged
//  4. Extensionless relative path → append .js
//
// RewriteHydrationImportPath rewrites a local Vela component import to its
// generated hydration entry. Non-component imports retain the normal rewrite.
func RewriteHydrationImportPath(importPath string) string {
	if strings.HasSuffix(importPath, ".vela") {
		return strings.TrimSuffix(importPath, ".vela") + ".hydrate.js"
	}
	return RewriteImportPath(importPath)
}

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

// defaultRuntimeImport is the runtime specifier the generators emit. Project
// builds retarget it to the single runtime file at the output root.
const defaultRuntimeImport = "./runtime.js"

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

// retargetRuntimeImports points every import of the generated runtime at
// specifier, so all modules of one build share one runtime instance.
func retargetRuntimeImports(prog *JsProgram, specifier string) {
	if prog == nil || specifier == "" || specifier == defaultRuntimeImport {
		return
	}
	for _, stmt := range prog.Body {
		if imp, ok := stmt.(*JsImportDecl); ok && imp.Source == defaultRuntimeImport {
			imp.Source = specifier
		}
	}
}
