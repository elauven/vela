package ssr_runtime

import (
	"encoding/json"
	"math"
	"os"
	"strconv"
	"testing"
)

// TestFdlibmMatchesNode compares Math.cbrt and Math.pow with V8
// (testdata/math_fixture.json, from tools/gen-math-fixture.mjs). cbrt is
// fdlibm's algorithm and matches bit for bit. pow is correctly rounded;
// V8 uses the platform libm, whose pow is off by one unit in the last
// place in rare cases next to a rounding boundary (pow(1e21, 10)), so a
// handful of one-ulp differences are allowed.
func TestFdlibmMatchesNode(t *testing.T) {
	data, err := os.ReadFile("../testdata/math_fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Node string      `json:"node"`
		Pow  [][3]string `json:"pow"`
		Cbrt [][2]string `json:"cbrt"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	f := func(hex string) float64 {
		n, err := strconv.ParseUint(hex, 16, 64)
		if err != nil {
			t.Fatal(err)
		}
		return math.Float64frombits(n)
	}
	same := func(a, b float64) bool {
		return math.Float64bits(a) == math.Float64bits(b) || math.IsNaN(a) && math.IsNaN(b)
	}
	oneULP := func(a, b float64) bool {
		return !math.IsInf(a, 0) && !math.IsInf(b, 0) && (math.Nextafter(a, b) == b)
	}
	near := 0
	for _, row := range fixture.Pow {
		x, y, want := f(row[0]), f(row[1]), f(row[2])
		got := Pow(x, y)
		switch {
		case same(got, want):
		case oneULP(got, want):
			near++
		default:
			t.Errorf("pow(%v, %v) = %v, Node %s gives %v", x, y, got, fixture.Node, want)
		}
	}
	if near > len(fixture.Pow)/200 {
		t.Errorf("pow differs from Node by one ulp in %d of %d rows", near, len(fixture.Pow))
	}
	for _, row := range fixture.Cbrt {
		x, want := f(row[0]), f(row[1])
		if got := MathCbrt(x); !same(got, want) {
			t.Errorf("cbrt(%v) = %v, Node %s gives %v", x, got, fixture.Node, want)
		}
	}
}
