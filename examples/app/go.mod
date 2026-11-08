module example.com/site

go 1.25.0

require (
	github.com/elauven/vela/app v0.4.0
	github.com/elauven/vela/ssr_runtime v0.8.0
)

require golang.org/x/text v0.41.0 // indirect

// Prerender, Cache, RenderClient and vela.ClientComponent (task 092) are not
// tagged yet: drop these lines after app/v0.5.0 and ssr_runtime/v0.9.0 are
// published, then go mod tidy (docs/RELEASING.md).
replace (
	github.com/elauven/vela/app => ../../app
	github.com/elauven/vela/ssr_runtime => ../../ssr_runtime
)
