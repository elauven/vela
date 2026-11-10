package ssr_runtime

// ContractV8 is the contract marker of this runtime version (R-12.1d):
// ContractV7 (ContractV6 plus the client-only component values of task
// 092, ClientComponent) plus the {@html} tag of task 100 (WriteHTMLTag).
// Every file the compiler generates references it, so generated code and a
// runtime of another contract fail go build instead of misbehaving. The
// compiler's SSRRuntimeContract names it; change both together whenever
// generated code starts to depend on a new or changed runtime API.
const ContractV8 = true
