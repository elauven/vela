package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elauven/vela/runtime"
)

// TestPackageTypes runs the type tests of testdata/types (task 082,
// docs/TYPESCRIPT.md TS-6) against the declarations of the npm packages as
// they are published, with the oldest (5.8, `typescript-5.8` in
// package.json) and the newest supported TypeScript (6.x), in the strict
// configuration of TS-10.2 plus noUncheckedIndexedAccess and
// exactOptionalPropertyTypes.
func TestPackageTypes(t *testing.T) {
	compilers := typeTestCompilers(t)
	app := typeTestApp(t)
	buildTypeTestLibrary(t, compilers[len(compilers)-1], app)
	for _, tsc := range compilers {
		for _, config := range []string{"tsconfig.json", "tsconfig.unplugin.json"} {
			if out, err := runTSC(tsc, app, config); err != nil {
				t.Errorf("%s -p %s:\n%s", tsc.version, config, out)
			}
		}
	}

	// The tests can fail: a declaration that loses a prop type is caught.
	broken := filepath.Join(app, "node_modules", "@govela", "runtime", "runtime.d.ts")
	data, err := os.ReadFile(broken)
	if err != nil {
		t.Fatal(err)
	}
	weakened := strings.Replace(string(data), "options: MountOptions<NoInfer<P>>", "options: MountOptions<any>", 1)
	if weakened == string(data) {
		t.Fatal("runtime.d.ts no longer declares mount() as the probe expects")
	}
	if err := os.WriteFile(broken, []byte(weakened), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runTSC(compilers[0], app, "tsconfig.json"); err == nil || !strings.Contains(out, "Unused '@ts-expect-error'") {
		t.Errorf("weakened mount() types passed the type tests:\n%s", out)
	}
}

type typeTestCompiler struct{ version, script string }

// typeTestCompilers are the TypeScript compilers of node_modules: 5.8 and 6.x.
func typeTestCompilers(t *testing.T) []typeTestCompiler {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		skipBundlers(t, "node not available")
	}
	var out []typeTestCompiler
	for _, dir := range []string{"typescript-5.8", "typescript"} {
		script, err := filepath.Abs(filepath.Join("node_modules", dir, "bin", "tsc"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(script); err != nil {
			skipBundlers(t, "%s not installed; run npm ci", dir)
		}
		version, err := exec.Command("node", script, "--version").Output()
		if err != nil {
			t.Fatalf("%s --version: %v", dir, err)
		}
		out = append(out, typeTestCompiler{version: strings.TrimSpace(string(version)), script: script})
	}
	return out
}

// typeTestApp is a temporary application: testdata/types, and node_modules
// with the packages and unplugin (whose types @govela/unplugin uses).
func typeTestApp(t *testing.T) string {
	t.Helper()
	app := t.TempDir()
	copyTree(t, filepath.Join("testdata", "types"), app)
	scope := filepath.Join(app, "node_modules", "@govela")
	for _, dir := range []string{runtimePackageDir, compilerPackageDir, unpluginPackageDir, "packages/app"} {
		copyTree(t, filepath.FromSlash(dir), filepath.Join(scope, filepath.Base(dir)))
	}
	unplugin, err := filepath.Abs(filepath.Join("node_modules", "unplugin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(unplugin, filepath.Join(app, "node_modules", "unplugin")); err != nil {
		skipBundlers(t, "symlink to unplugin: %v", err)
	}
	return app
}

func runTSC(tsc typeTestCompiler, dir, config string) (string, error) {
	cmd := exec.Command("node", tsc.script, "-p", config)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// buildTypeTestLibrary builds testdata/dts/lib into node_modules/ui-lib of
// app as a component library is published (task 083): `vela --dts` for the
// components, `tsc --declaration --emitDeclarationOnly` for its .ts module.
func buildTypeTestLibrary(t *testing.T, tsc typeTestCompiler, app string) {
	t.Helper()
	srcRoot := filepath.Join("testdata", "dts", "lib")
	out := filepath.Join(app, "node_modules", "ui-lib")
	opts := CompileOptions{StatusWriter: io.Discard, BareRuntimeImports: true, RuntimeImport: runtime.PackageName, DTS: true}
	if errors := BuildProject(CollectVela(srcRoot), srcRoot, out, opts, false); errors != 0 {
		t.Fatalf("build of %s failed with %d error(s)", srcRoot, errors)
	}
	pkg := `{"name": "ui-lib", "type": "module", "exports": {"./*": "./*"}, "peerDependencies": {"@govela/runtime": "*"}}`
	if err := os.WriteFile(filepath.Join(out, "package.json"), []byte(pkg+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	module, err := filepath.Abs(filepath.Join(srcRoot, "format.ts"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", tsc.script, "--declaration", "--emitDeclarationOnly", "--strict",
		"--target", "ES2022", "--module", "ESNext", "--moduleResolution", "bundler", "--outDir", out, module)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tsc --emitDeclarationOnly %s: %v\n%s", module, err, b)
	}
}
