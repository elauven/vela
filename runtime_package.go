package main

// runtime_package.go — the @govela/runtime npm package (contract R-12.2f,
// task 048), generated from runtime/ into packages/runtime. Applications
// that bundle their client install it and compile with
// --runtime-import=bare; the compiler never resolves or bundles it.

import (
	"github.com/elauven/vela/runtime"

	_ "embed"
	"encoding/json"
	"strings"
)

// licenseText is the repository license, shipped with the npm package.
//
//go:embed LICENSE
var licenseText []byte

// runtimePackageDir is where the generated package lives in the repository.
const runtimePackageDir = "packages/runtime"

// repositoryURL is the git repository of the npm packages. npm publish
// --provenance rejects a package whose repository.url is not the repository
// that built it.
const repositoryURL = "git+https://github.com/elauven/vela.git"

// packageRepository is the repository field of a generated package.json.
type packageRepository struct {
	Type      string `json:"type"`
	URL       string `json:"url"`
	Directory string `json:"directory"`
}

func repositoryOf(dir string) packageRepository {
	return packageRepository{Type: "git", URL: repositoryURL, Directory: dir}
}

// bundlerDevelopmentFlag replaces runtimeDevelopmentFlag in the package. A
// bundler replaces process.env.NODE_ENV with a string, so the check becomes a
// constant and a production bundle drops the development code. The staged
// copy keeps the self-detecting check that works without a bundler.
const bundlerDevelopmentFlag = "const DEV = process.env.NODE_ENV !== 'production';"

// runtimePackageFiles returns the files of packages/runtime.
func runtimePackageFiles() (map[string][]byte, error) {
	core := string(runtime.Core)
	if !strings.Contains(core, runtime.DevelopmentFlag) {
		panic("vela: runtime.js no longer contains the development flag")
	}
	core = strings.Replace(core, runtime.DevelopmentFlag, bundlerDevelopmentFlag, 1)
	// The core runtime registers shared state on globalThis when it is
	// evaluated; the block and hydration modules have no import effects.
	manifest := struct {
		Name        string            `json:"name"`
		Version     string            `json:"version"`
		Description string            `json:"description"`
		License     string            `json:"license"`
		Repository  packageRepository `json:"repository"`
		Type        string            `json:"type"`
		SideEffects []string          `json:"sideEffects"`
		Exports     map[string]string `json:"exports"`
		Files       []string          `json:"files"`
	}{
		Name:        runtime.PackageName,
		Version:     runtime.Version,
		Description: "Runtime of components compiled by the Vela compiler. Generated from runtime/; do not edit.",
		License:     "MIT",
		Repository:  repositoryOf(runtimePackageDir),
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
	readme := "# " + runtime.PackageName + "\n\n" +
		"Runtime of components compiled by the Vela compiler, for applications that\n" +
		"bundle their client (esbuild, Vite, Rollup).\n\n" +
		"Compile with `vela --runtime-import=bare` so generated modules import\n" +
		"`" + runtime.PackageName + "`, `" + runtime.PackageName + "/blocks` and `" + runtime.PackageName + "/hydrate` instead of\n" +
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
		"runtime-blocks.js":  runtime.Blocks,
		"runtime-hydrate.js": runtime.Hydrate,
	}, nil
}
