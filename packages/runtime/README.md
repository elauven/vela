# @govela/runtime

Runtime of components compiled by the Vela compiler, for applications that
bundle their client (esbuild, Vite, Rollup).

Compile with `vela --runtime-import=bare` so generated modules import
`@govela/runtime`, `@govela/runtime/blocks` and `@govela/runtime/hydrate` instead of
copies next to the output. The package version must equal the compiler
version; a mismatch fails when the bundler links the modules
(missing `$$runtimeContract…` export).

Development checks are removed when the bundler replaces
`process.env.NODE_ENV` with `"production"` (esbuild and Vite do this for
production builds; Rollup needs `@rollup/plugin-replace`).

Generated from `runtime/` of the compiler repository; do not edit.

License: MIT.
