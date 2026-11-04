module example.com/site

go 1.25.0

require (
	github.com/elauven/vela/app v0.0.0
	github.com/elauven/vela/ssr_runtime v0.0.0
)

require golang.org/x/text v0.41.0 // indirect

replace (
	github.com/elauven/vela/app => ../../app
	github.com/elauven/vela/ssr_runtime => ../../ssr_runtime
)
