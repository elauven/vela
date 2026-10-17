package ssr_runtime

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// fusedOps are the fused multiply-add instructions of the architectures on
// which the Go compiler fuses x*y+z.
var fusedOps = regexp.MustCompile(`\s(FMADD[SD]?|FMSUB[SD]?|FNMADD[SD]?|FNMSUB[SD]?|WFMADB|WFMSDB|FMA[DS])\s`)

// TestNoFusedMultiplyAdd keeps the runtime's arithmetic identical to
// JavaScript on every architecture: a fused multiply-add rounds once where
// JavaScript rounds twice, which changed Math.cos, Math.log10, toString
// with a radix and others in the last bit on arm64. Wrap every
// floating-point product in float64(...), which the specification defines
// as preventing fusion.
func TestNoFusedMultiplyAdd(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the package for five architectures")
	}
	for _, arch := range []string{"arm64", "ppc64le", "s390x", "riscv64", "loong64"} {
		cmd := exec.Command("go", "build", "-o", os.DevNull, "-gcflags=-S", ".")
		cmd.Env = append(cmd.Environ(), "GOARCH="+arch, "GOOS=linux", "CGO_ENABLED=0")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s build: %v\n%s", arch, err, out)
		}
		var hits []string
		function := ""
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, " STEXT") {
				function = strings.Fields(line)[0]
			}
			if fusedOps.MatchString(line) {
				hits = append(hits, function)
			}
		}
		if len(hits) > 0 {
			t.Errorf("%s: %d fused multiply-add instructions, first in %s", arch, len(hits), hits[0])
		}
	}
}
