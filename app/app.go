// Package app is the Vela App layer: routes from files, Go loaders, server
// HTML with hydration and partial navigation (docs/APP.md).
//
// The package depends on the compiler only through its public interfaces
// (docs/APP.md B-1): the vela CLI and its outputs, the module
// github.com/elauven/vela/ssr_runtime and the public entry points of
// @govela/runtime. TestAppLayerIsSeparable in the root module enforces it.
//
// The route table comes from vela-app gen (task 074); New serves documents
// (task 075), the data responses of the client router (task 076) and
// fragments (task 077). Forms follow in task 078.
package app

import vela "github.com/elauven/vela/ssr_runtime"

// The layer needs the runtime of RuntimeContract: another runtime fails to
// build here.
var _ = vela.ContractV7

// Version is the version of the app layer. It is tagged app/vX.Y.Z and is
// independent of the compiler version (docs/APP.md B-6).
const Version = "0.6.0"

// RuntimeContract names the runtime contract the layer is written for
// (docs/APP.md B-6); the package references ssr_runtime's marker of the
// same name, so a runtime of another contract fails go build.
const RuntimeContract = "ContractV7"
