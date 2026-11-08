module example.com/site

go 1.25.0

require (
	github.com/elauven/vela/app v0.4.0
	github.com/elauven/vela/ssr_runtime v0.8.0
)

require golang.org/x/text v0.41.0 // indirect

// app/v0.4.0 is tagged but not in the module proxy until the tag is
// pushed: drop this line then, and go mod tidy (docs/RELEASING.md).
replace github.com/elauven/vela/app => ../../app
