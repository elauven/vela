package main

// imports.go — import statement processing for the Vela compiler.
//
// Responsibilities:
//   1. Parse all import statements from the script section
//   2. Rewrite .vela import paths → .js (compiled output paths)
//   3. Emit the rewritten imports into the generated JS output

import (
	"path/filepath"
	"regexp"
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

var (
	// Named imports — separate patterns for single and double quotes (Go RE2 has no backreferences)
	reImportNamedD      = regexp.MustCompile(`^(\s*)import\s*\{([^}]+)\}\s*from\s*"([^"]+)"`)
	reImportNamedS      = regexp.MustCompile(`^(\s*)import\s*\{([^}]+)\}\s*from\s*'([^']+)'`)
	reImportDefaultD    = regexp.MustCompile(`^(\s*)import\s+(\w+)\s+from\s*"([^"]+)"`)
	reImportDefaultS    = regexp.MustCompile(`^(\s*)import\s+(\w+)\s+from\s*'([^']+)'`)
	reImportStarD       = regexp.MustCompile(`^(\s*)import\s+\*\s+as\s+(\w+)\s+from\s*"([^"]+)"`)
	reImportStarS       = regexp.MustCompile(`^(\s*)import\s+\*\s+as\s+(\w+)\s+from\s*'([^']+)'`)
	reImportSideEffectD = regexp.MustCompile(`^(\s*)import\s+"([^"]+)"`)
	reImportSideEffectS = regexp.MustCompile(`^(\s*)import\s+'([^']+)'`)
)

// ParseImportRecords extracts all import statements from a script string.
// lineOffset is the 0-based line number where the script begins in the file.
func ParseImportRecords(script string, lineOffset int) []ImportRecord {
	var recs []ImportRecord
	lines := strings.Split(script, "\n")
	for i, line := range lines {
		absLine := lineOffset + i
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "import") {
			continue
		}

		// Named: import { Foo, Bar } from '...' or "..."
		if m := reImportNamedD.FindStringSubmatch(line); m != nil {
			recs = append(recs, ImportRecord{Raw: line, Names: parseNameList(m[2]), Path: m[3], Line: absLine})
			continue
		}
		if m := reImportNamedS.FindStringSubmatch(line); m != nil {
			recs = append(recs, ImportRecord{Raw: line, Names: parseNameList(m[2]), Path: m[3], Line: absLine})
			continue
		}
		// Default: import Foo from '...'
		if m := reImportDefaultD.FindStringSubmatch(line); m != nil {
			recs = append(recs, ImportRecord{Raw: line, Names: []string{m[2]}, Path: m[3], Line: absLine})
			continue
		}
		if m := reImportDefaultS.FindStringSubmatch(line); m != nil {
			recs = append(recs, ImportRecord{Raw: line, Names: []string{m[2]}, Path: m[3], Line: absLine})
			continue
		}
		// Namespace: import * as Ns from '...'
		if m := reImportStarD.FindStringSubmatch(line); m != nil {
			recs = append(recs, ImportRecord{Raw: line, Names: []string{"* as " + m[2]}, Path: m[3], Line: absLine})
			continue
		}
		if m := reImportStarS.FindStringSubmatch(line); m != nil {
			recs = append(recs, ImportRecord{Raw: line, Names: []string{"* as " + m[2]}, Path: m[3], Line: absLine})
			continue
		}
		// Side-effect: import '...'
		if m := reImportSideEffectD.FindStringSubmatch(line); m != nil {
			recs = append(recs, ImportRecord{Raw: line, Path: m[2], Line: absLine})
			continue
		}
		if m := reImportSideEffectS.FindStringSubmatch(line); m != nil {
			recs = append(recs, ImportRecord{Raw: line, Path: m[2], Line: absLine})
			continue
		}
	}
	return recs
}

func parseNameList(s string) []string {
	var names []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		// Handle 'Foo as F' alias — use local alias name
		fields := strings.Fields(part)
		switch {
		case len(fields) == 3 && fields[1] == "as":
			names = append(names, fields[2])
		case len(fields) > 0:
			names = append(names, fields[0])
		}
	}
	return names
}

// ─────────────────────────────────────────────
//  Path rewriting
// ─────────────────────────────────────────────

// RewriteImportPath rewrites an import path for the compiled JS output.
//
// Rules applied in order:
//   1. Node module (no leading ./ or /) → unchanged
//   2. .vela extension → replace with .js
//   3. Already has .js/.mjs/.cjs/.ts extension → unchanged
//   4. Extensionless relative path → append .js
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

// RewriteImportLine rewrites a single import source line.
// Returns the rewritten line (unchanged if no path change is needed).
func RewriteImportLine(line string) string {
	// Try each pattern pair; path is always the last capture group
	patterns := []*regexp.Regexp{
		reImportNamedD, reImportNamedS,
		reImportDefaultD, reImportDefaultS,
		reImportStarD, reImportStarS,
		reImportSideEffectD, reImportSideEffectS,
	}
	for _, re := range patterns {
		groups := re.FindStringSubmatch(line)
		if groups == nil {
			continue
		}
		origPath := groups[len(groups)-1]
		newPath := RewriteImportPath(origPath)
		if newPath == origPath {
			return line
		}
		// Replace first occurrence of the quoted path (try both quote styles)
		for _, q := range []string{`"`, `'`} {
			old := q + origPath + q
			if strings.Contains(line, old) {
				return strings.Replace(line, old, q+newPath+q, 1)
			}
		}
		return line
	}
	return line
}

// RewriteScriptImports rewrites all import lines in a script section string.
func RewriteScriptImports(script string) string {
	lines := strings.Split(script, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "import") {
			lines[i] = RewriteImportLine(line)
		}
	}
	return strings.Join(lines, "\n")
}
