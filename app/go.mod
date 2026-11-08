module github.com/elauven/vela/app

go 1.25.0

require github.com/elauven/vela/ssr_runtime v0.8.0

// ContractV7 (task 092) is not tagged yet: drop this line after
// ssr_runtime/v0.9.0 is published, require it, then go mod tidy
// (docs/RELEASING.md, the app layer).
replace github.com/elauven/vela/ssr_runtime => ../ssr_runtime

require golang.org/x/text v0.41.0 // indirect
