package main

import (
	"os"
	"path/filepath"
)

// Generated projects in tests write their own go.mod requiring the
// compiler module through a replace directive; the runtime's dependencies
// (golang.org/x/text) are resolved from the module cache when those
// projects build.
func init() {
	if os.Getenv("GOFLAGS") == "" {
		os.Setenv("GOFLAGS", "-mod=mod")
	}
	if os.Getenv("GONOSUMDB") == "" {
		os.Setenv("GONOSUMDB", "golang.org/x")
	}
}

// ssrRuntimeRequire is the end of a generated test project's go.mod: the Go
// runtime module of this repository, not the compiler module (task 049).
func ssrRuntimeRequire(compilerRoot string) string {
	return "require " + SSRRuntimeImportPath + " v0.0.0\n\nreplace " + SSRRuntimeImportPath + " => " + filepath.ToSlash(filepath.Join(compilerRoot, "ssr_runtime")) + "\n"
}
