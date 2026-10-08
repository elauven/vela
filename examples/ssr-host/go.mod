module example.com/ssrhostdemo

go 1.25.0

require github.com/elauven/vela v0.0.0

require golang.org/x/text v0.41.0 // indirect

// The Go runtime is not published as its own module yet (task 049), so the
// example uses the compiler sources of this repository.
replace github.com/elauven/vela => ../..
