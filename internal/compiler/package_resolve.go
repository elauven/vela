package compiler

import (
	"github.com/elauven/vela/internal/codegen"
	"github.com/elauven/vela/internal/diag"
	"github.com/elauven/vela/internal/jsast"
	"github.com/elauven/vela/internal/sfcast"

	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// package_resolve.go — --check-deps (task 048): reports package imports that
// would not resolve in node_modules. It only reads package.json files; it
// never copies, rewrites or bundles a package.

// DepsCheckDiagnosticCode reports an import of a package that does not
// resolve.
const DepsCheckDiagnosticCode = "VELA-NPM-UNRESOLVED"

// CheckPackageSpecifier returns why spec, imported by a module in fromDir,
// does not resolve with the Node.js algorithm, or "" when it resolves.
func CheckPackageSpecifier(fromDir, spec string) string {
	if strings.HasPrefix(spec, "node:") {
		return fmt.Sprintf("%q is a Node.js built-in module; browsers cannot load it", spec)
	}
	name := PackageOfSpecifier(spec)
	sub := strings.TrimPrefix(spec, name)
	dir, err := filepath.Abs(fromDir)
	if err != nil {
		return ""
	}
	for {
		pkgDir := filepath.Join(dir, "node_modules", filepath.FromSlash(name))
		if data, err := os.ReadFile(filepath.Join(pkgDir, "package.json")); err == nil {
			return checkPackageSubpath(pkgDir, name, sub, data)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return fmt.Sprintf("package %q is not installed (no node_modules/%s from %s)", name, name, filepath.ToSlash(fromDir))
		}
		dir = parent
	}
}

// checkPackageSubpath checks sub ("" or "/path") against the package's
// exports, or against its files when it has no exports.
func checkPackageSubpath(pkgDir, name, sub string, manifest []byte) string {
	var pkg struct {
		Exports json.RawMessage `json:"exports"`
	}
	if err := json.Unmarshal(manifest, &pkg); err != nil {
		return fmt.Sprintf("package %q has an invalid package.json: %v", name, err)
	}
	if len(pkg.Exports) == 0 || string(pkg.Exports) == "null" {
		if sub == "" {
			return ""
		}
		file := filepath.Join(pkgDir, filepath.FromSlash(sub))
		for _, candidate := range []string{file, file + ".js", file + ".mjs", filepath.Join(file, "index.js")} {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return ""
			}
		}
		return fmt.Sprintf("%q does not exist in package %q", strings.TrimPrefix(sub, "/"), name)
	}
	key := "." + sub
	var exports map[string]json.RawMessage
	if json.Unmarshal(pkg.Exports, &exports) != nil || !hasSubpathKeys(exports) {
		// A string or a condition object exports the root only.
		if sub == "" {
			return ""
		}
		return fmt.Sprintf("subpath %q is not exported by package %q", key, name)
	}
	if _, ok := exports[key]; ok {
		return ""
	}
	for pattern := range exports {
		if star := strings.Index(pattern, "*"); star >= 0 && strings.HasPrefix(key, pattern[:star]) && strings.HasSuffix(key, pattern[star+1:]) {
			return ""
		}
	}
	return fmt.Sprintf("subpath %q is not exported by package %q", key, name)
}

func hasSubpathKeys(exports map[string]json.RawMessage) bool {
	for key := range exports {
		if strings.HasPrefix(key, ".") {
			return true
		}
	}
	return false
}

// checkScriptPackageImports warns about the unresolved package imports of a
// component script.
func checkScriptPackageImports(src string, script *sfcast.ScriptAST, ds *diag.DiagSet) {
	if script == nil || script.Program == nil {
		return
	}
	ds.PushOffset(script.Span.Start)
	defer ds.PopOffset()
	for _, stmt := range script.Program.Body {
		var spec string
		switch x := stmt.(type) {
		case *jsast.JsImportDecl:
			spec = x.Source
		case *jsast.JsExportDecl:
			if x.HasSource {
				spec = x.Source
			}
		}
		if !codegen.IsPackageSpecifier(spec) && !strings.HasPrefix(spec, "node:") {
			continue
		}
		if problem := CheckPackageSpecifier(filepath.Dir(src), spec); problem != "" {
			ds.Report(diag.SevWarning, stmt.NodePos(), stmt.NodeEnd(), DepsCheckDiagnosticCode, "install the package, or provide it through the import map or bundler of the application", "%s", problem)
		}
	}
}

// checkModulePackageImports warns about the unresolved package imports of a
// copied JavaScript module, in the compiler's diagnostic format.
func checkModulePackageImports(src, source string, opts CompileOptions) {
	for _, spec := range codegen.ModuleSpecifiers(source) {
		if !codegen.IsPackageSpecifier(spec.Value) && !strings.HasPrefix(spec.Value, "node:") {
			continue
		}
		problem := CheckPackageSpecifier(filepath.Dir(src), spec.Value)
		if problem == "" {
			continue
		}
		column := spec.Pos - strings.LastIndex(source[:spec.Pos], "\n")
		fmt.Fprintf(ErrorWriter(opts), "       %s:%d:%d: [warning] [%s] %s\n", filepath.ToSlash(src), spec.Line, column, DepsCheckDiagnosticCode, problem)
		opts.Report.add(&diag.Diagnostic{Sev: diag.SevWarning, File: filepath.ToSlash(src), Line: spec.Line, Col: column, Code: DepsCheckDiagnosticCode, Message: problem})
	}
}
