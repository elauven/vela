package ssr_runtime

import (
	"bytes"
	"encoding/json"
	"math"
	"math/rand/v2"
	"strings"
	"testing"
)

// appendJSON must write the bytes json.Marshal writes (task 058).
func TestAppendJSONMatchesEncodingJSON(t *testing.T) {
	var strs []string
	for c := 0; c < 256; c++ {
		strs = append(strs, string(rune(c)), string([]byte{byte(c)}), "a"+string(rune(c))+"b")
	}
	strs = append(strs, "", "</script>", "<!--", "a&b", "\u2028\u2029", "é€😀", "\xff\xfe", "\xed\xa0\x80", "tab\tnew\nline")
	for _, s := range strs {
		same(t, s)
	}
	for _, f := range []float64{0, math.Copysign(0, -1), 1, -1, 0.1, 1e-6, 1e-7, 9.99e-7, 1e20, 1e21, 123456789.125, 1 / 3.0, -2.5e-300, math.MaxFloat64, math.SmallestNonzeroFloat64} {
		same(t, f)
	}
	r := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 2000; i++ {
		same(t, randomValue(r, 4))
	}
	same(t, []any{int64(5), int(-3), uint16(7), nil, Undefined(), Hole(), true, false, map[string]any{"b": 1.5, "a": "x"}, json.RawMessage(` { "x" : [1, 2] } `)})
	same(t, struct {
		Card     any `json:"card"`
		Dragging any `json:"dragging"`
	}{Card: NewObject().Set("id", 1.0).Set("t", "<b>"), Dragging: false})
	same(t, struct {
		A any `json:"a,omitempty"`
		B any `json:"-"`
		c any
		D any
	}{D: 1.0})
	same(t, struct{}{})
	if _, err := appendJSON(nil, math.NaN()); err == nil {
		t.Error("NaN must fail as in encoding/json")
	}
}

func same(t *testing.T, value any) {
	t.Helper()
	want, werr := json.Marshal(value)
	got, gerr := appendJSON(nil, value)
	if (werr == nil) != (gerr == nil) || !bytes.Equal(want, got) {
		t.Fatalf("value %#v:\n  encoding/json %s (%v)\n  appendJSON    %s (%v)", value, want, werr, got, gerr)
	}
}

func randomValue(r *rand.Rand, depth int) any {
	switch k := r.IntN(8); {
	case depth == 0 || k == 0:
		return r.Float64() * math.Pow(10, float64(r.IntN(40)-20)) * float64(1-2*r.IntN(2))
	case k == 1:
		var b strings.Builder
		for n := r.IntN(12); n > 0; n-- {
			b.WriteRune(rune(r.IntN(0x2100)))
		}
		return b.String()
	case k == 2:
		return r.IntN(2) == 0
	case k == 3:
		return nil
	case k == 4:
		items := make([]any, r.IntN(5))
		for i := range items {
			items[i] = randomValue(r, depth-1)
		}
		return items
	case k == 5:
		m := map[string]any{}
		for n := r.IntN(5); n > 0; n-- {
			m[string(rune('a'+r.IntN(26)))+"<&>"] = randomValue(r, depth-1)
		}
		return m
	default:
		o := NewObject()
		for n := r.IntN(5); n > 0; n-- {
			o.Set([]string{"id", "name", "10", "2", "z", "é"}[r.IntN(6)], randomValue(r, depth-1))
		}
		return o
	}
}
