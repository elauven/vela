// Package runtime embeds the browser runtime sources of runtime/ for the
// compiler and names the runtime npm package.
package runtime

import _ "embed"

// Core is runtime.js, the core runtime.
//
//go:embed runtime.js
var Core []byte

// Blocks is runtime-blocks.js, the block engine.
//
//go:embed runtime-blocks.js
var Blocks []byte

// Hydrate is runtime-hydrate.js, hydrate() of the block engine.
//
//go:embed runtime-hydrate.js
var Hydrate []byte

// DevelopmentFlag is the core runtime's development switch. The
// production variant replaces it with a constant so warnings and other
// development-only paths are disabled.
const DevelopmentFlag = "const DEV = typeof process !== 'undefined'\n  ? process.env.NODE_ENV !== 'production'\n  : !globalThis.__VELA_PROD__;"

// Version is the compiler version. The runtime npm package and the Go
// runtime module are released with the same version (docs/RELEASING.md).
const Version = "0.4.7"

// PackageName is the npm package of the JavaScript runtime.
const PackageName = "@govela/runtime"

// PackageSpecifiers maps the staged runtime files to the package
// exports that generated code imports with --runtime-import=bare.
var PackageSpecifiers = map[string]string{
	"runtime.js":         PackageName,
	"runtime-blocks.js":  PackageName + "/blocks",
	"runtime-hydrate.js": PackageName + "/hydrate",
}
