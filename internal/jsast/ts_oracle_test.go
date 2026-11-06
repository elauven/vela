package jsast

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The differential oracle of task 080: TypeScript's own transpileModule
// (verbatimModuleSyntax, ESNext) erases every source too, and its output,
// parsed as JavaScript, must be the tree our parser builds from the
// TypeScript. Sources: the corpus, every .ts file of the repository's
// node_modules (and packages/*/node_modules), and VELA_TS_ORACLE_DIR (a directory of real projects).
//
// It needs node and the typescript package of the repository; without them
// it is skipped.

const tsOracleScript = `
const ts = require(process.argv[2]);
const inputs = JSON.parse(require('fs').readFileSync(process.argv[3], 'utf8'));
const options = {
  target: ts.ScriptTarget.ESNext, module: ts.ModuleKind.ESNext,
  verbatimModuleSyntax: true, isolatedModules: true, useDefineForClassFields: true,
  moduleDetection: ts.ModuleDetectionKind.Force,
};
const out = inputs.map(src => {
  const r = ts.transpileModule(src, { compilerOptions: options, reportDiagnostics: true, fileName: 'm.ts' });
  return { out: r.outputText, syntaxError: (r.diagnostics || []).length > 0 };
});
process.stdout.write(JSON.stringify(out));
`

type tsOracleResult struct {
	Out         string `json:"out"`
	SyntaxError bool   `json:"syntaxError"`
}

func repoRoot(t *testing.T) string {
	dir, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func typescriptPackage(t *testing.T) string {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	pkg := filepath.Join(repoRoot(t), "node_modules", "typescript")
	if _, err := os.Stat(filepath.Join(pkg, "package.json")); err != nil {
		t.Skip("node_modules/typescript is not installed (npm ci)")
	}
	return pkg
}

func runTSOracle(t *testing.T, pkg string, sources []string) []tsOracleResult {
	dir := t.TempDir()
	script := filepath.Join(dir, "oracle.cjs")
	input := filepath.Join(dir, "in.json")
	data, _ := json.Marshal(sources)
	if err := os.WriteFile(script, []byte(tsOracleScript), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, data, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", script, pkg, input)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("oracle: %v", err)
	}
	var results []tsOracleResult
	if err := json.Unmarshal(out, &results); err != nil {
		t.Fatalf("oracle output: %v", err)
	}
	return results
}

// tsSourceFiles lists TypeScript implementation files under the roots.
func tsSourceFiles(roots ...string) []string {
	var files []string
	for _, root := range roots {
		filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() && (info.Name() == ".vscode-test" || info.Name() == ".git") {
				return filepath.SkipDir
			}
			name := info.Name()
			if !info.IsDir() && (strings.HasSuffix(name, ".ts") || strings.HasSuffix(name, ".mts")) &&
				!strings.HasSuffix(name, ".d.ts") && !strings.HasSuffix(name, ".d.mts") && info.Size() < 400_000 {
				files = append(files, path)
			}
			return nil
		})
	}
	sort.Strings(files)
	return files
}

// numericSeparators matches `_` between digits: transpileModule prints
// numeric literals without separators.
var numericSeparators = regexp.MustCompile(`(\d)_`)

// withoutEmptyExports drops `export {}`, which TypeScript adds to keep an
// erased file a module.
func withoutEmptyExports(prog *JsProgram) *JsProgram {
	out := *prog
	out.Body = nil
	for _, s := range prog.Body {
		if e, ok := s.(*JsExportDecl); ok && e.Declaration == nil && len(e.Specifiers) == 0 && !e.HasSource {
			continue
		}
		out.Body = append(out.Body, s)
	}
	return &out
}

func TestTSOracleTranspileModule(t *testing.T) {
	pkg := typescriptPackage(t)
	type input struct{ name, src string }
	var inputs []input
	for _, c := range tsValidCases {
		inputs = append(inputs, input{"corpus " + c.group + "/" + c.name, c.ts})
	}
	roots := []string{filepath.Join(repoRoot(t), "node_modules")}
	pkgs, _ := filepath.Glob(filepath.Join(repoRoot(t), "packages", "*", "node_modules"))
	roots = append(roots, pkgs...)
	if extra := os.Getenv("VELA_TS_ORACLE_DIR"); extra != "" {
		roots = append(roots, filepath.SplitList(extra)...)
	}
	for _, f := range tsSourceFiles(roots...) {
		if b, err := os.ReadFile(f); err == nil {
			inputs = append(inputs, input{f, string(b)})
		}
	}
	sources := make([]string, len(inputs))
	for i, in := range inputs {
		sources[i] = in.src
	}
	results := runTSOracle(t, pkg, sources)

	compared, nonErasable, tsRejected := 0, 0, 0
	for i, in := range inputs {
		r := results[i]
		ours := tsParse(in.src)
		if ours.panic != nil {
			t.Errorf("%s: panic: %v", in.name, ours.panic)
			continue
		}
		if r.SyntaxError {
			tsRejected++
			continue
		}
		var syntax []string
		erasable := true
		for _, d := range ours.diags {
			if d.Code == DiagTSNonErasable {
				erasable = false
			} else {
				syntax = append(syntax, fmt.Sprintf("@%d %s", d.Pos, d.Message))
			}
		}
		if len(syntax) > 0 {
			t.Errorf("%s: TypeScript accepts the source, we report: %s", in.name, strings.Join(syntax, "; "))
			continue
		}
		if !erasable {
			nonErasable++ // TS-2.4: rejected on purpose, nothing to compare
			continue
		}
		theirs := ctParse(r.Out)
		if len(theirs.diags) > 0 {
			t.Errorf("%s: transpileModule output does not parse as JavaScript: %v", in.name, theirs.diags)
			continue
		}
		g := numericSeparators.ReplaceAllString(ctDump(withoutEmptyExports(ours.prog)), "$1")
		w := numericSeparators.ReplaceAllString(ctDump(withoutEmptyExports(theirs.prog)), "$1")
		if g != w {
			t.Errorf("%s: erasure differs from transpileModule\n  ours:   %.600s\n  theirs: %.600s\n  output: %.400q", in.name, g, w, r.Out)
		}
		compared++
	}
	t.Logf("oracle: %d sources compared, %d non-erasable (rejected on purpose), %d rejected by TypeScript", compared, nonErasable, tsRejected)
	if compared < len(tsValidCases) {
		t.Errorf("only %d sources compared", compared)
	}
}

// TestTSDeclarationFilesParse runs the type grammar over every .d.ts of the
// oracle's roots (lib.dom.d.ts, typescript.d.ts, …) as an
// ambient file: all of it must parse without a diagnostic and erase to
// nothing but `export {}`-like statements.
func TestTSDeclarationFilesParse(t *testing.T) {
	roots := []string{filepath.Join(repoRoot(t), "node_modules")}
	pkgs, _ := filepath.Glob(filepath.Join(repoRoot(t), "packages", "*", "node_modules"))
	roots = append(roots, pkgs...)
	if extra := os.Getenv("VELA_TS_ORACLE_DIR"); extra != "" {
		roots = append(roots, filepath.SplitList(extra)...)
	}
	var files []string
	for _, root := range roots {
		filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() && info.Name() == ".vscode-test" {
				return filepath.SkipDir
			}
			if !info.IsDir() && (strings.HasSuffix(path, ".d.ts") || strings.HasSuffix(path, ".d.mts")) {
				files = append(files, path)
			}
			return nil
		})
	}
	if len(files) == 0 {
		t.Skip("no declaration files")
	}
	failed := 0
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		p := newJSParserOpts(string(b), ParseOptions{TypeScript: true})
		p.ambient = 1
		var prog *JsProgram
		func() {
			defer func() {
				if e := recover(); e != nil {
					t.Errorf("%s: panic: %v", f, e)
				}
			}()
			prog = p.parseProgram()
		}()
		p.finish()
		if len(p.Diagnostics) > 0 {
			failed++
			if failed <= 25 {
				d := p.Diagnostics[0]
				line := string(b)[lineStartOf(string(b), d.Pos):]
				if i := strings.IndexByte(line, '\n'); i >= 0 {
					line = line[:i]
				}
				t.Errorf("%s: %s\n    %s", f, d.Message, strings.TrimSpace(line))
			}
			continue
		}
		if prog != nil {
			for _, bad := range ctInvariants(string(b), prog) {
				t.Errorf("%s: invariant: %s", f, bad)
				break
			}
		}
	}
	t.Logf("%d declaration files, %d with diagnostics", len(files), failed)
}

func lineStartOf(src string, pos int) int {
	if pos > len(src) {
		pos = len(src)
	}
	return strings.LastIndexByte(src[:pos], '\n') + 1
}
