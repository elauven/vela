module example.com/ssrhostdemo

go 1.25.0

require github.com/elauven/vela/ssr_runtime v0.0.0

require golang.org/x/text v0.41.0 // indirect

// The Go runtime module is not published yet; until its first tag
// (ssr_runtime/v0.x.y) the example uses the module of this repository.
// After publication, drop this line and require the released version.
replace github.com/elauven/vela/ssr_runtime => ../../ssr_runtime
