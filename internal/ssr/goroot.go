package ssr

import (
	"go/build"
	"go/importer"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

var findGOROOT sync.Once

// goImporter is the importer of the standard library for type-checking
// generated Go. It reads the GOROOT of build.Default, which a binary built
// with -trimpath (the release binaries) does not know unless GOROOT is set:
// then the GOROOT of the go command on PATH is used, the toolchain that
// builds the generated code.
func goImporter() types.Importer {
	findGOROOT.Do(func() {
		if build.Default.GOROOT != "" {
			if _, err := os.Stat(filepath.Join(build.Default.GOROOT, "src")); err == nil {
				return
			}
		}
		out, err := exec.Command("go", "env", "GOROOT").Output()
		if err != nil {
			return
		}
		if goroot := strings.TrimSpace(string(out)); goroot != "" {
			build.Default.GOROOT = goroot
		}
	})
	return importer.Default()
}

// goToolchainHint explains an import failure of the standard library when
// no GOROOT was found.
func goToolchainHint() string {
	if build.Default.GOROOT != "" {
		return ""
	}
	return "; vela --ssr needs the Go toolchain: put go on PATH or set GOROOT"
}
