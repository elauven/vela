module github.com/elauven/vela/ssr_runtime

go 1.25.0

require golang.org/x/text v0.41.0

// v0.3.0 computed some Math results and number formatting on arm64, ppc64le,
// s390x, riscv64 and loong64 differently from JavaScript (fused
// multiply-add); v0.3.1 fixes it.
retract v0.3.0
