package main

import "os"

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
