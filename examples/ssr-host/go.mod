module example.com/ssrhostdemo

go 1.25.0

require github.com/elauven/vela/ssr_runtime v0.9.0

require golang.org/x/text v0.41.0 // indirect

// ContractV8 is not tagged yet: drop this line after the ssr_runtime with
// it is published, require it, then go mod tidy (docs/RELEASING.md).
replace github.com/elauven/vela/ssr_runtime => ../../ssr_runtime
