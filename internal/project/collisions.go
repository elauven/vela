package project

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// OutputCollisionCode reports two sources of a project whose outputs are one
// file: Card.vela and Card.js both become Card.js, and Card.vela and
// card.js become one file on a file system that ignores case (macOS,
// Windows), where the second silently overwrites the first.
const OutputCollisionCode = "VELA-OUTPUT-COLLISION"

// OutputCollision is a source (Second) whose output is the output of an
// earlier source (First). Output is the output path of Second, relative to
// the output root and slash-separated; Exact is true when the two paths are
// equal, not only equal without case.
type OutputCollision struct {
	First, Second string
	Output        string
	Exact         bool
}

// OutputPath is the output path of a source of a project relative to the
// output root, slash-separated: a component becomes .js, a TypeScript
// module .js, and any other module keeps its name.
func OutputPath(src, srcRoot string) string {
	rel, err := filepath.Rel(srcRoot, src)
	if err != nil {
		rel = filepath.Base(src)
	}
	if strings.EqualFold(filepath.Ext(rel), ".vela") || IsTypeScriptModule(rel) {
		rel = strings.TrimSuffix(rel, filepath.Ext(rel)) + ".js"
	}
	return filepath.ToSlash(rel)
}

// OutputCollisions lists the sources whose output is already the output of
// another source, compared without case. Sources are taken in path order,
// so the report does not depend on the order of the list.
func OutputCollisions(sources []string, srcRoot string) []OutputCollision {
	sorted := append([]string(nil), sources...)
	sort.Strings(sorted)
	taken := map[string]string{} // lower-cased output → its source
	var out []OutputCollision
	for _, src := range sorted {
		output := OutputPath(src, srcRoot)
		key := strings.ToLower(output)
		if first, ok := taken[key]; ok {
			if first == src {
				continue
			}
			out = append(out, OutputCollision{First: first, Second: src, Output: output, Exact: OutputPath(first, srcRoot) == output})
			continue
		}
		taken[key] = src
	}
	return out
}

// SameFile reports whether a and b name one existing file, as two spellings
// of a path do on a file system that ignores case.
func SameFile(a, b string) bool {
	ia, err := os.Stat(a)
	if err != nil {
		return false
	}
	ib, err := os.Stat(b)
	return err == nil && os.SameFile(ia, ib)
}
