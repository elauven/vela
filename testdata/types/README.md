Type tests of the npm packages (task 082, docs/TYPESCRIPT.md TS-6):
`TestPackageTypes` copies the packages into a temporary application and runs
`tsc --noEmit` with each tsconfig here, on the oldest and the newest
supported TypeScript (TS-10.1). `expect.ts` has the assertions; a negative
case is a line under `// @ts-expect-error`.

`src/dts.test-d.ts` types the declarations `vela --dts` writes (task 083):
the test builds `testdata/dts/lib` into `node_modules/ui-lib` and declares
its `.ts` module with `tsc --emitDeclarationOnly`.
