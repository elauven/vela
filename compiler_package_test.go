package main

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The manifests of packages/compiler, packages/compiler-* and
// packages/unplugin are generated
// with the compiler version; regenerate with
// VELA_UPDATE_RUNTIME_PACKAGE=1 go test -run TestCompilerPackagesAreGenerated .
func TestCompilerPackagesAreGenerated(t *testing.T) {
	files, err := compilerPackageFiles()
	if err != nil {
		t.Fatal(err)
	}
	update := os.Getenv("VELA_UPDATE_RUNTIME_PACKAGE") == "1"
	var names []string
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.FromSlash(name)
		if update {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, files[name], 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, files[name]) {
			t.Errorf("%s is not up to date; run VELA_UPDATE_RUNTIME_PACKAGE=1 go test -run TestCompilerPackagesAreGenerated .", path)
		}
	}

	// Nothing else is in the packages: the hand-written sources of the main
	// package, and the binary the release workflow adds to a platform
	// package (ignored by git).
	allowed := map[string]bool{}
	for name := range files {
		allowed[filepath.FromSlash(name)] = true
	}
	for _, name := range compilerPackageSources {
		allowed[filepath.Join(filepath.FromSlash(compilerPackageDir), filepath.FromSlash(name))] = true
	}
	for _, name := range unpluginPackageSources {
		allowed[filepath.Join(filepath.FromSlash(unpluginPackageDir), name)] = true
	}
	for _, p := range compilerPlatforms {
		allowed[filepath.Join(filepath.FromSlash(p.Dir()), filepath.FromSlash(p.Binary()))] = true
	}
	dirs := []string{compilerPackageDir, unpluginPackageDir}
	for _, p := range compilerPlatforms {
		dirs = append(dirs, p.Dir())
	}
	for _, dir := range dirs {
		_ = filepath.Walk(filepath.FromSlash(dir), func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && !allowed[path] {
				t.Errorf("%s is neither generated nor a package source; remove it", path)
			}
			return nil
		})
	}
	for _, name := range compilerPackageSources {
		if _, err := os.Stat(filepath.Join(filepath.FromSlash(compilerPackageDir), filepath.FromSlash(name))); err != nil {
			t.Errorf("package source %s: %v", name, err)
		}
	}
	for _, name := range unpluginPackageSources {
		if _, err := os.Stat(filepath.Join(filepath.FromSlash(unpluginPackageDir), name)); err != nil {
			t.Errorf("package source %s: %v", name, err)
		}
	}
}

// TestReleaseWorkflowCoversCompilerPlatforms keeps the release workflow and
// compilerPlatforms in step: every platform is built and put into its
// package.
func TestReleaseWorkflowCoversCompilerPlatforms(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)
	for _, p := range compilerPlatforms {
		build := p.GOOS + "/" + p.GOARCH
		if !strings.Contains(workflow, build+" ") && !strings.Contains(workflow, build+";") {
			t.Errorf("release.yml does not build %s", build)
		}
		if stage := build + ":" + p.name(); !strings.Contains(workflow, stage) {
			t.Errorf("release.yml does not put the %s binary into %s (%s)", build, p.Dir(), stage)
		}
	}
	// The language server ships beside the compiler (task 065).
	for _, want := range []string{`-o "dist/vela-lsp-$os-$arch$ext" ./cmd/vela-lsp`, `"packages/compiler-$npm/bin/vela-lsp$ext"`} {
		if !strings.Contains(workflow, want) {
			t.Errorf("release.yml does not build and stage vela-lsp: %s", want)
		}
	}
	if !strings.Contains(workflow, "packages/compiler "+unpluginPackageDir+"; do") {
		t.Errorf("release.yml does not publish %s after the compiler", unpluginPackageDir)
	}
	// npm publish takes a bare "packages/x" for the GitHub repository packages/x.
	if !strings.Contains(workflow, `npm publish "./$dir"`) {
		t.Error(`release.yml must publish each package by a ./ path: npm publish "./$dir"`)
	}
}

// npm publish --provenance requires repository.url to name the repository
// the workflow runs in, which is the module's.
func TestPackageRepositoryIsTheModule(t *testing.T) {
	gomod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	module := strings.Fields(strings.SplitN(string(gomod), "\n", 2)[0])[1]
	if want := "git+https://" + module + ".git"; repositoryURL != want {
		t.Fatalf("repositoryURL = %q, want %q", repositoryURL, want)
	}
}
