package ssr_runtime

// ContractV6 is the contract marker of this runtime version (R-12.1d):
// ContractV5 plus error boundaries and the document head of task 091
// (BeginBoundary, EndBoundary, CatchBoundary, WriteHeadTag,
// WriteScriptText).
// Every file the compiler generates references it, so generated code and a
// runtime of another contract fail go build instead of misbehaving. The
// compiler's SSRRuntimeContract names it; change both together whenever
// generated code starts to depend on a new or changed runtime API.
const ContractV6 = true
