package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Subpath imports (task 090). A specifier that starts with # resolves
// through the "imports" field of the nearest package.json, as Node does:
// an exact key, or the pattern key with one * whose prefix is the longest
// match ("#app/*": "./src/_app/*"). The module it names is a file of the
// project: Vela compiles and translates it like a relative import, and the
// generated JavaScript keeps the specifier as written for the bundler and
// TypeScript, which resolve it themselves. Vela resolves only string
// targets inside the package: a conditional or array entry, a package
// target and a target outside the package directory are errors that name
// the entry.

// SubpathImportCode is the diagnostic code of an unresolvable # import.
const SubpathImportCode = "VELA-SUBPATH-IMPORT"

// IsSubpathImport reports a # specifier.
func IsSubpathImport(spec string) bool {
	return strings.HasPrefix(spec, "#")
}

// SubpathImport is a resolved # specifier.
type SubpathImport struct {
	// PackageJSON is the package.json whose entry matched, Key the entry.
	PackageJSON string
	Key         string
	// File is the absolute file the specifier names.
	File string
}

// SubpathImportError explains why a # specifier does not resolve.
type SubpathImportError struct {
	Spec    string
	Message string
}

func (e *SubpathImportError) Error() string {
	return fmt.Sprintf("%s: %s", e.Spec, e.Message)
}

// ResolveSubpathImport resolves spec, imported by a file in dir.
func ResolveSubpathImport(dir, spec string) (SubpathImport, error) {
	fail := func(format string, args ...any) (SubpathImport, error) {
		return SubpathImport{}, &SubpathImportError{Spec: spec, Message: fmt.Sprintf(format, args...)}
	}
	if !IsSubpathImport(spec) || spec == "#" || strings.HasPrefix(spec, "#/") {
		return fail("is not a valid subpath import")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fail("%v", err)
	}
	pkgFile := ""
	for current := abs; ; {
		candidate := filepath.Join(current, "package.json")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			pkgFile = candidate
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	if pkgFile == "" {
		return fail("no package.json above %s declares \"imports\"", displayPath(abs))
	}
	data, err := os.ReadFile(pkgFile)
	if err != nil {
		return fail("cannot read %s: %v", filepath.ToSlash(pkgFile), err)
	}
	var manifest struct {
		Imports map[string]json.RawMessage `json:"imports"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fail("%s is not valid JSON: %v", filepath.ToSlash(pkgFile), err)
	}
	where := displayPath(pkgFile)
	key, match := "", ""
	if _, ok := manifest.Imports[spec]; ok && !strings.Contains(spec, "*") {
		key = spec
	} else {
		var keys []string
		for candidate := range manifest.Imports {
			keys = append(keys, candidate)
		}
		sort.Strings(keys)
		for _, candidate := range keys {
			star := strings.Index(candidate, "*")
			if star < 0 || strings.LastIndex(candidate, "*") != star {
				continue
			}
			prefix, suffix := candidate[:star], candidate[star+1:]
			if len(spec) < len(prefix)+len(suffix) || !strings.HasPrefix(spec, prefix) || !strings.HasSuffix(spec, suffix) {
				continue
			}
			// The longest prefix wins, then the longest key (Node's order).
			if key == "" || star > strings.Index(key, "*") || (star == strings.Index(key, "*") && len(candidate) > len(key)) {
				key, match = candidate, spec[len(prefix):len(spec)-len(suffix)]
			}
		}
	}
	if key == "" {
		return fail("no entry of \"imports\" in %s matches it", where)
	}
	raw := manifest.Imports[key]
	var target string
	if err := json.Unmarshal(raw, &target); err != nil {
		kind := "not a string"
		trimmed := strings.TrimSpace(string(raw))
		switch {
		case strings.HasPrefix(trimmed, "{"):
			kind = "conditional (an object of conditions such as \"browser\" or \"node\")"
		case strings.HasPrefix(trimmed, "["):
			kind = "an array of fallbacks"
		case trimmed == "null":
			kind = "null"
		}
		return fail("the entry %q of \"imports\" in %s is %s; Vela resolves only a string target such as \"./src/_app/*\"", key, where, kind)
	}
	if !strings.HasPrefix(target, "./") {
		return fail("the entry %q of \"imports\" in %s maps to %q, outside the package; Vela resolves only targets that start with ./", key, where, target)
	}
	if strings.Contains(key, "*") {
		target = strings.ReplaceAll(target, "*", match)
	}
	pkgDir := filepath.Dir(pkgFile)
	file := filepath.Join(pkgDir, filepath.FromSlash(target))
	rel, err := filepath.Rel(pkgDir, file)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fail("the entry %q of \"imports\" in %s resolves to %s, outside the package", key, where, displayPath(file))
	}
	return SubpathImport{PackageJSON: pkgFile, Key: key, File: file}, nil
}

// displayPath is file relative to the working directory when it is inside
// it, with slashes.
func displayPath(file string) string {
	if wd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(wd, file); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(file)
}
