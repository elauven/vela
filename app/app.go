// Package app is the Vela App layer: routes from files, Go loaders, server
// HTML with hydration and partial navigation (docs/APP.md).
//
// The package depends on the compiler only through its public interfaces
// (docs/APP.md B-1): the vela CLI and its outputs, the module
// github.com/elauven/vela/ssr_runtime and the public entry points of
// @govela/runtime. TestAppLayerIsSeparable in the root module enforces it.
//
// This is the skeleton of task 073. The handler, requests and loaders follow
// in tasks 074–078.
package app

// Version is the version of the app layer. It is tagged app/vX.Y.Z and is
// independent of the compiler version (docs/APP.md B-6).
const Version = "0.1.0-dev"

// RuntimeContract names the runtime contract the layer is written for
// (docs/APP.md B-6). From task 075 on the package references
// ssr_runtime's marker of the same name, so a runtime of another contract
// fails go build.
const RuntimeContract = "ContractV4"
