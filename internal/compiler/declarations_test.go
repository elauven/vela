package compiler

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/elauven/vela/runtime"
)

// The declarations of the runtime package (task 082) name exactly what the
// runtime exports and what the compiler lets a component import from
// `vela`: a runtime export without a declaration, or a declaration of
// something that does not exist, fails here.

var declaredValue = regexp.MustCompile(`(?m)^\s*export (?:function|const) ([A-Za-z_$][\w$]*)`)

func declaredNames(dts string) []string {
	seen := map[string]bool{}
	var names []string
	for _, m := range declaredValue.FindAllStringSubmatch(dts, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			names = append(names, m[1])
		}
	}
	sort.Strings(names)
	return names
}

var exportedValue = regexp.MustCompile(`(?m)^export (?:async )?(?:function\*? |const |let )([A-Za-z_$][\w$]*)`)

// publicExports are the exports of a runtime module without the $$ helpers.
func publicExports(src []byte) []string {
	var names []string
	for _, m := range exportedValue.FindAllStringSubmatch(string(src), -1) {
		if !strings.HasPrefix(m[1], "$") {
			names = append(names, m[1])
		}
	}
	sort.Strings(names)
	return names
}

// moduleBody is the text of `declare module 'name' { … }` in ambient.
func moduleBody(t *testing.T, ambient, name string) string {
	t.Helper()
	start := strings.Index(ambient, "declare module '"+name+"' {\n")
	if start < 0 {
		t.Fatalf("ambient.d.ts declares no module %q", name)
	}
	end := strings.Index(ambient[start:], "\n}\n")
	return ambient[start : start+end]
}

func TestDeclarationsMatchTheRuntime(t *testing.T) {
	check := func(what string, got, want []string) {
		t.Helper()
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s declares %v, want %v", what, got, want)
		}
	}
	check("runtime.d.ts", declaredNames(runtime.RuntimeDeclarations()), publicExports(runtime.Core))
	check("runtime-hydrate.d.ts", declaredNames(runtime.HydrateDeclarations()), publicExports(runtime.Hydrate))
	ambient := runtime.AmbientDeclarations()
	check("module 'vela' of ambient.d.ts", declaredNames(moduleBody(t, ambient, "vela")), sortedVelaModuleExports())
	var stores []string
	for _, name := range publicExports(runtime.Core) {
		if name == "writable" || name == "readable" || name == "derived" {
			stores = append(stores, name)
		}
	}
	check("module 'vela/store' of ambient.d.ts", declaredNames(moduleBody(t, ambient, "vela/store")), stores)
}
