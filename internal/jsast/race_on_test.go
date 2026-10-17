//go:build race

package jsast

// raceEnabled is true when the tests run with the race detector, which
// slows parsing several times.
const raceEnabled = true
