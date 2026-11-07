package gen

import (
	"fmt"
	"sort"
	"strings"
)

// Diagnostic codes of the generator. They are part of its output, like the
// VELA-* codes of the compiler.
const (
	CodeConfig    = "VELA-APP-CONFIG"    // vela.app.json, vela.config.json, go.mod
	CodeDirectory = "VELA-APP-DIRECTORY" // a directory name pkgpath cannot encode (P-3)
	CodeFile      = "VELA-APP-FILE"      // a file that does not belong in the routes tree (P-1, P-17)
	CodeRest      = "VELA-APP-REST"      // [...rest] not last (P-4)
	CodeParam     = "VELA-APP-PARAM"     // a parameter name repeated along a path (P-5)
	CodeRank      = "VELA-APP-RANK"      // two candidates of the same rank at one level (P-8)
	CodeDuplicate = "VELA-APP-DUPLICATE" // two routes with the same URL pattern (P-9)
	CodeMatcher   = "VELA-APP-MATCHER"   // unknown matcher, missing half, shadowing (P-14)
	CodeStale     = "VELA-APP-STALE"     // a mirrored directory without a route directory (P-17)
	CodeLoader    = "VELA-APP-LOADER"    // a loader without a component, a wrong signature (L-1, L-7)
	CodeGuard     = "VELA-APP-GUARD"     // a guard without a component, a wrong signature (G-1)
)

// Diagnostic is one generator error. File is relative to the project root;
// Line and Col are 1-based, and 0 when the error concerns a directory.
type Diagnostic struct {
	File    string
	Line    int
	Col     int
	Code    string
	Message string
	Help    string
}

// String formats the diagnostic as the compiler does:
// "file:line:col: [error] [CODE] message", then "help: …".
func (d Diagnostic) String() string {
	loc := ""
	switch {
	case d.File != "" && d.Line > 0:
		loc = fmt.Sprintf("%s:%d:%d: ", d.File, d.Line, max(d.Col, 1))
	case d.File != "":
		loc = d.File + ": "
	}
	s := fmt.Sprintf("%s[error] [%s] %s", loc, d.Code, d.Message)
	if d.Help != "" {
		s += "\n  help: " + d.Help
	}
	return s
}

// Error joins diagnostics into one error.
type Error []Diagnostic

func (e Error) Error() string {
	lines := make([]string, len(e))
	for i, d := range e {
		lines[i] = d.String()
	}
	return strings.Join(lines, "\n")
}

// sortDiagnostics orders diagnostics by file and position, so the output
// does not depend on the directory walk.
func sortDiagnostics(diags []Diagnostic) {
	sort.SliceStable(diags, func(i, j int) bool {
		a, b := diags[i], diags[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Col < b.Col
	})
}
