module example.com/site

go 1.25.0

require (
	github.com/elauven/vela/app v0.5.0
	github.com/elauven/vela/ssr_runtime v0.9.0
)

require golang.org/x/text v0.41.0 // indirect

// Prerender, Cache, RenderClient and vela.ClientComponent (task 092) are not
// in the module proxy yet: drop this line after app/v0.5.0 is published, then
// go mod tidy (docs/RELEASING.md).
replace github.com/elauven/vela/app => ../../app
