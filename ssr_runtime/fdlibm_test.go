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

// TestTranscendentalMatchesNode checks the transcendental Math functions
// (ports of V8's fdlibm) and toString(radix) against V8 bit for bit.
func TestTranscendentalMatchesNode(t *testing.T) {
	data, err := os.ReadFile("../testdata/math_fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Node  string      `json:"node"`
		Unary [][3]string `json:"unary"`
		Atan2 [][3]string `json:"atan2"`
		Radix [][3]any    `json:"radix"`
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
	close := func(a, b float64) bool {
		return math.Float64bits(a) == math.Float64bits(b) || math.IsNaN(a) && math.IsNaN(b)
	}
	functions := map[string]func(any) float64{
		"log": MathLog, "log2": MathLog2, "log10": MathLog10, "log1p": MathLog1p, "exp": MathExp, "expm1": MathExpm1,
		"sin": MathSin, "cos": MathCos, "tan": MathTan, "asin": MathAsin, "acos": MathAcos, "atan": MathAtan,
		"sinh": MathSinh, "cosh": MathCosh, "tanh": MathTanh, "asinh": MathAsinh, "acosh": MathAcosh, "atanh": MathAtanh,
	}
	mismatches := map[string]int{}
	for _, row := range fixture.Unary {
		x, want := f(row[1]), f(row[2])
		if got := functions[row[0]](x); !close(got, want) {
			if mismatches[row[0]] < 3 {
				t.Errorf("Math.%s(%v) = %v, Node %s gives %v", row[0], x, got, fixture.Node, want)
			}
			mismatches[row[0]]++
		}
	}
	if len(mismatches) > 0 {
		t.Errorf("mismatches by function: %v", mismatches)
	}
	for _, row := range fixture.Atan2 {
		y, x, want := f(row[0]), f(row[1]), f(row[2])
		if got := MathAtan2(y, x); !close(got, want) {
			t.Errorf("Math.atan2(%v, %v) = %v, Node gives %v", y, x, got, want)
		}
	}
	for _, row := range fixture.Radix {
		x := f(row[0].(string))
		radix := int(row[1].(float64))
		if got := radixString(x, radix); got != row[2].(string) {
			t.Errorf("(%v).toString(%d) = %s, Node gives %s", x, radix, got, row[2])
		}
	}
}
