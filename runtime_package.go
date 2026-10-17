package main

// runtime_package.go — the @govela/runtime npm package (contract R-12.2f,
// task 048), generated from runtime/ into packages/runtime. Applications
// that bundle their client install it and compile with
// --runtime-import=bare; the compiler never resolves or bundles it.

import (
	_ "embed"
	"encoding/json"
	"strings"
)

// licenseText is the repository license, shipped with the npm package.
//
//go:embed LICENSE
var licenseText []byte

// Version is the compiler version. The runtime npm package and the Go
// runtime module are released with the same version (docs/RELEASING.md).
const Version = "0.3.1"

// RuntimePackageName is the npm package of the JavaScript runtime.
const RuntimePackageName = "@govela/runtime"

// runtimePackageDir is where the generated package lives in the repository.
const runtimePackageDir = "packages/runtime"

// runtimePackageSpecifiers maps the staged runtime files to the package
// exports that generated code imports with --runtime-import=bare.
var runtimePackageSpecifiers = map[string]string{
	"runtime.js":         RuntimePackageName,
	"runtime-blocks.js":  RuntimePackageName + "/blocks",
	"runtime-hydrate.js": RuntimePackageName + "/hydrate",
}

// bundlerDevelopmentFlag replaces runtimeDevelopmentFlag in the package. A
// bundler replaces process.env.NODE_ENV with a string, so the check becomes a
// constant and a production bundle drops the development code. The staged
// copy keeps the self-detecting check that works without a bundler.
const bundlerDevelopmentFlag = "const DEV = process.env.NODE_ENV !== 'production';"

// runtimePackageFiles returns the files of packages/runtime.
func runtimePackageFiles() (map[string][]byte, error) {
	core := string(embeddedRuntime)
	if !strings.Contains(core, runtimeDevelopmentFlag) {
		panic("vela: runtime.js no longer contains the development flag")
	}
	core = strings.Replace(core, runtimeDevelopmentFlag, bundlerDevelopmentFlag, 1)
	// The core runtime registers shared state on globalThis when it is
	// evaluated; the block and hydration modules have no import effects.
	manifest := struct {
		Name        string            `json:"name"`
		Version     string            `json:"version"`
		Description string            `json:"description"`
		License     string            `json:"license"`
		Type        string            `json:"type"`
		SideEffects []string          `json:"sideEffects"`
		Exports     map[string]string `json:"exports"`
		Files       []string          `json:"files"`
	}{
		Name:        RuntimePackageName,
		Version:     Version,
		Description: "Runtime of components compiled by the Vela compiler. Generated from runtime/; do not edit.",
		License:     "MIT",
		Type:        "module",
		SideEffects: []string{"./runtime.js"},
		Exports: map[string]string{
			".":         "./runtime.js",
			"./blocks":  "./runtime-blocks.js",
			"./hydrate": "./runtime-hydrate.js",
		},
		Files: []string{"runtime.js", "runtime-blocks.js", "runtime-hydrate.js", "LICENSE"},
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	readme := "# " + RuntimePackageName + "\n\n" +
		"Runtime of components compiled by the Vela compiler, for applications that\n" +
		"bundle their client (esbuild, Vite, Rollup).\n\n" +
		"Compile with `vela --runtime-import=bare` so generated modules import\n" +
		"`" + RuntimePackageName + "`, `" + RuntimePackageName + "/blocks` and `" + RuntimePackageName + "/hydrate` instead of\n" +
		"copies next to the output. The package version must equal the compiler\n" +
		"version; a mismatch fails when the bundler links the modules\n" +
		"(missing `$$runtimeContract…` export).\n\n" +
		"Development checks are removed when the bundler replaces\n" +
		"`process.env.NODE_ENV` with `\"production\"` (esbuild and Vite do this for\n" +
		"production builds; Rollup needs `@rollup/plugin-replace`).\n\n" +
		"Generated from `runtime/` of the compiler repository; do not edit.\n\n" +
		"License: MIT.\n"
	return map[string][]byte{
		"package.json":       append(encoded, '\n'),
		"README.md":          []byte(readme),
		"LICENSE":            licenseText,
		"runtime.js":         []byte(core),
		"runtime-blocks.js":  embeddedBlocksRuntime,
		"runtime-hydrate.js": embeddedHydrateRuntime,
	}, nil
}
