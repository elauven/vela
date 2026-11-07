package gen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The #app/* subpath import (docs/APP.md R-11, task 090): components
// import getPage from '#app/page.js', which package.json maps to the out
// directory. vela-app scaffold adds the entry; gen only reports it missing,
// because it never edits package.json.

// AppImportKey is the "imports" key of the generated directory.
const AppImportKey = "#app/*"

// appImportTarget is the target of AppImportKey: the out directory.
func appImportTarget(cfg Config) string {
	return "./" + strings.TrimPrefix(cfg.Out, "./") + "/*"
}

// AppImportEntry is the line to add to package.json.
func AppImportEntry(cfg Config) string {
	return fmt.Sprintf(`"imports": { %q: %q }`, AppImportKey, appImportTarget(cfg))
}

// appImportMissing reports whether the package.json of the project lacks
// the entry (or has another target for it). Without a package.json the
// entry is missing too.
func appImportMissing(cfg Config) bool {
	data, err := os.ReadFile(filepath.Join(cfg.Root, "package.json"))
	if err != nil {
		return true
	}
	var manifest struct {
		Imports map[string]any `json:"imports"`
	}
	if json.Unmarshal(data, &manifest) != nil {
		return true
	}
	target, ok := manifest.Imports[AppImportKey].(string)
	return !ok || target != appImportTarget(cfg)
}

// AddAppImport adds the entry to the package.json at the project root,
// keeping the rest of the file as written. It reports whether it changed
// the file; an entry with another target is an error, not replaced.
func AddAppImport(cfg Config) (bool, error) {
	file := filepath.Join(cfg.Root, "package.json")
	data, err := os.ReadFile(file)
	if err != nil {
		return false, fmt.Errorf("package.json: %w", err)
	}
	var manifest struct {
		Imports map[string]json.RawMessage `json:"imports"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return false, fmt.Errorf("package.json: %w", err)
	}
	target := appImportTarget(cfg)
	if raw, ok := manifest.Imports[AppImportKey]; ok {
		var existing string
		if json.Unmarshal(raw, &existing) == nil && existing == target {
			return false, nil
		}
		return false, fmt.Errorf("package.json maps %q to %s, not %q; change it by hand", AppImportKey, raw, target)
	}
	entry, _ := json.Marshal(AppImportKey)
	value, _ := json.Marshal(target)
	open, indent, ok := objectStart(data, manifest.Imports != nil)
	if !ok {
		return false, fmt.Errorf("package.json: cannot find where to add the %q entry", AppImportKey)
	}
	var insert string
	if manifest.Imports != nil {
		// Into the existing "imports" object.
		insert = "\n" + indent + indent + string(entry) + ": " + string(value)
		if len(manifest.Imports) > 0 {
			insert += ","
		} else if bytes.HasPrefix(bytes.TrimLeft(data[open+1:], " \t"), []byte("}")) {
			insert += "\n" + indent
		}
	} else {
		insert = "\n" + indent + `"imports": {` + "\n" + indent + indent + string(entry) + ": " + string(value) + "\n" + indent + "},"
	}
	out := append(append(append([]byte{}, data[:open+1]...), insert...), data[open+1:]...)
	if !json.Valid(out) {
		return false, fmt.Errorf("package.json: adding the %q entry would make it invalid; add %s by hand", AppImportKey, AppImportEntry(cfg))
	}
	return true, os.WriteFile(file, out, 0o644)
}

// objectStart returns the offset of the { of the root object, or of the
// value of its "imports" key with imports, and the indentation of the file.
func objectStart(data []byte, imports bool) (int, string, bool) {
	indent := "  "
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		rest := data[i+1:]
		n := 0
		for n < len(rest) && (rest[n] == ' ' || rest[n] == '\t') {
			n++
		}
		if n > 0 {
			indent = string(rest[:n])
		}
	}
	// frame is an open object (keyNext: the next string is a key) or array.
	type frame struct {
		object, keyNext bool
		key             string
	}
	var stack []frame
	valueDone := func() {
		if len(stack) > 0 && stack[len(stack)-1].object {
			stack[len(stack)-1].keyNext = true
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		offset := decoder.InputOffset()
		token, err := decoder.Token()
		if err != nil {
			return 0, indent, false
		}
		switch t := token.(type) {
		case json.Delim:
			switch t {
			case '{', '[':
				if t == '{' {
					at := int(offset) + bytes.IndexByte(data[offset:], '{')
					if len(stack) == 0 && !imports {
						return at, indent, true
					}
					if len(stack) == 1 && imports && stack[0].key == "imports" {
						return at, indent, true
					}
				}
				stack = append(stack, frame{object: t == '{', keyNext: t == '{'})
			case '}', ']':
				stack = stack[:len(stack)-1]
				valueDone()
			}
		case string:
			if top := len(stack) - 1; top >= 0 && stack[top].object && stack[top].keyNext {
				stack[top].key, stack[top].keyNext = t, false
				continue
			}
			valueDone()
		default:
			valueDone()
		}
	}
}
