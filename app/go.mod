module github.com/elauven/vela/app

go 1.25.0

require github.com/elauven/vela/ssr_runtime v0.4.7

require golang.org/x/text v0.41.0 // indirect

// ssr_runtime/pkgpath (task 069) is not in a tagged runtime yet. Before the
// first app/vX.Y.Z tag, tag the runtime, require it and drop this line:
// go install refuses a module with replace directives (docs/RELEASING.md).
replace github.com/elauven/vela/ssr_runtime => ../ssr_runtime
