package ssr_runtime

import (
	"io"
	"math"
	"strings"
	"testing"
)

func TestRendererFuncAndWriteString(t *testing.T) {
	renderer := RendererFunc(func(writer io.Writer) error { return WriteString(writer, "rendered") })
	var out strings.Builder
	if err := renderer.Render(&out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "rendered" {
		t.Fatalf("output=%q", out.String())
	}
}

func TestStringAndEscapeString(t *testing.T) {
	if got := String(42); got != "42" {
		t.Fatalf("String=%q", got)
	}
	if got := EscapeString(`<tag attr="x">`); got != "&lt;tag attr=&#34;x&#34;&gt;" {
		t.Fatalf("EscapeString=%q", got)
	}
	for _, tc := range []struct {
		value any
		want  string
	}{
		{nil, "null"},
		{Undefined(), "undefined"},
		{[]any{1, 2}, "1,2"},
		{[]any{1, nil, 3}, "1,,3"},
		{[]any{1, Undefined(), 3}, "1,,3"},
		{[]any{1, []any{2, nil}, 3}, "1,2,,3"},
		{map[string]any{"x": 1}, "[object Object]"},
	} {
		if got := String(tc.value); got != tc.want {
			t.Fatalf("String(%#v)=%q want %q", tc.value, got, tc.want)
		}
	}
	for _, tc := range []struct {
		value any
		want  string
	}{
		{nil, ""},
		{Undefined(), "undefined"},
		{false, "false"},
		{0, "0"},
		{"ready", "ready"},
	} {
		if got := FormValueString(tc.value); got != tc.want {
			t.Fatalf("FormValueString(%#v)=%q want %q", tc.value, got, tc.want)
		}
	}
	for _, tc := range []struct {
		value any
		want  string
	}{
		{nil, ""},
		{Undefined(), ""},
		{"ready", "ready"},
	} {
		if got := ArrayJoinString(tc.value); got != tc.want {
			t.Fatalf("ArrayJoinString(%#v)=%q want %q", tc.value, got, tc.want)
		}
	}
	if got := Length("Привет"); got != 6 {
		t.Fatalf("Length Cyrillic=%d want 6", got)
	}
	if got := Length("😀"); got != 2 {
		t.Fatalf("Length astral=%d want 2 UTF-16 units", got)
	}
	if got := Length([]int{1, 2}); got != 2 {
		t.Fatalf("Length array=%d want 2", got)
	}
	if got := TemplateString("Hello ", "Vela", " #", 7, " ", true, " ", nil); got != "Hello Vela #7 true null" {
		t.Fatalf("TemplateString=%q", got)
	}
	if got := TemplateString("missing=", Undefined()); got != "missing=undefined" {
		t.Fatalf("undefined TemplateString=%q", got)
	}
}

func TestTruthyAndLazyOperators(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  bool
	}{
		{nil, false}, {Undefined(), false}, {false, false}, {"", false}, {0, false}, {"ok", true}, {[]any{}, true}, {map[string]any{}, true},
	} {
		if got := Truthy(tc.value); got != tc.want {
			t.Fatalf("Truthy(%#v)=%v want %v", tc.value, got, tc.want)
		}
	}
	if got := TernaryLazy(func() any { return 0 }, func() any { return "yes" }, func() any { return "no" }); got != "no" {
		t.Fatalf("false ternary=%#v", got)
	}
	if got := TernaryLazy(func() any { return "ready" }, func() any { return "yes" }, func() any { return "no" }); got != "yes" {
		t.Fatalf("true ternary=%#v", got)
	}
	if got := AndLazy(func() any { return "" }, func() any { return "next" }); got != "" {
		t.Fatalf("false && result=%#v", got)
	}
	if got := AndLazy(func() any { return "ready" }, func() any { return "next" }); got != "next" {
		t.Fatalf("true && result=%#v", got)
	}
	if got := OrLazy(func() any { return "" }, func() any { return "fallback" }); got != "fallback" {
		t.Fatalf("false || result=%#v", got)
	}
	if got := OrLazy(func() any { return "ready" }, func() any { return "fallback" }); got != "ready" {
		t.Fatalf("true || result=%#v", got)
	}
	called := 0
	conditionCalls := 0
	if got := TernaryLazy(func() any { conditionCalls++; return false }, func() any { called++; return "unsafe" }, func() any { return "safe" }); got != "safe" || called != 0 || conditionCalls != 1 {
		t.Fatalf("ternary lazy evaluation result=%#v branchCalls=%d conditionCalls=%d", got, called, conditionCalls)
	}
	leftCalls := 0
	if got := AndLazy(func() any { leftCalls++; return 0 }, func() any { called++; return "unsafe" }); got != 0 || called != 0 || leftCalls != 1 {
		t.Fatalf("&& lazy evaluation result=%#v rightCalls=%d leftCalls=%d", got, called, leftCalls)
	}
	leftCalls = 0
	if got := OrLazy(func() any { leftCalls++; return true }, func() any { called++; return "unsafe" }); got != true || called != 0 || leftCalls != 1 {
		t.Fatalf("|| lazy evaluation result=%#v rightCalls=%d leftCalls=%d", got, called, leftCalls)
	}
}

func TestStrictEqualUsesJavaScriptPrimitiveAndReferenceSemantics(t *testing.T) {
	object := map[string]any{"id": 1}
	list := []any{1}
	for _, tc := range []struct {
		name        string
		left, right any
		want        bool
	}{
		{"null", nil, nil, true},
		{"null-undefined", nil, Undefined(), false},
		{"undefined", Undefined(), Undefined(), true},
		{"number-domain", 1, 1.0, true},
		{"nan", math.NaN(), math.NaN(), false},
		{"positive-negative-zero", 0.0, math.Copysign(0, -1), true},
		{"cross-type", "1", 1, false},
		{"boolean", false, false, true},
		{"boolean-number", false, 0, false},
		{"same-map", object, object, true},
		{"different-map", object, map[string]any{"id": 1}, false},
		{"same-slice", list, list, true},
		{"different-slice", list, []any{1}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := StrictEqual(tc.left, tc.right); got != tc.want {
				t.Fatalf("StrictEqual(%#v, %#v)=%v want %v", tc.left, tc.right, got, tc.want)
			}
		})
	}
}

func TestPropProvidedDistinguishesAbsentAndExplicitZeroValue(t *testing.T) {
	type props struct {
		Set   map[string]bool
		Title string
	}

	if !PropProvided(props{}, "title") {
		t.Fatal("a nil presence map must preserve legacy struct-literal semantics")
	}
	if PropProvided(props{Set: map[string]bool{}}, "title") {
		t.Fatal("an initialized map without title must mark the prop absent")
	}
	if !PropProvided(props{Set: map[string]bool{"title": true}}, "title") {
		t.Fatal("an explicit empty prop value must remain present")
	}
	if !PropProvided(struct{ Title string }{Title: "legacy"}, "title") {
		t.Fatal("non-generated props values must preserve legacy provided semantics")
	}
}

func TestMarshalHydrationPayloadClassifiesNonSerializableProps(t *testing.T) {
	encoded, err := MarshalHydrationPayload(struct {
		Title string `json:"title"`
	}{Title: "ready"})
	if err != nil || string(encoded) != `{"title":"ready"}` {
		t.Fatalf("serializable payload = %q, %v", encoded, err)
	}
	_, err = MarshalHydrationPayload(struct {
		Handler any `json:"handler"`
	}{Handler: func() {}})
	if err == nil || !strings.Contains(err.Error(), "VELA-SSR-PROPS") || !strings.Contains(err.Error(), "unsupported type") {
		t.Fatalf("non-serializable prop error = %v", err)
	}
}

func TestContainsStringUsesSelectMultipleMembership(t *testing.T) {
	if !ContainsString([]any{"one", 2, true}, "2") {
		t.Fatal("multi-select membership must use browser-visible string values")
	}
	if ContainsString([]string{"one", "two"}, "three") {
		t.Fatal("multi-select membership must reject absent values")
	}
	if ContainsString("two", "two") {
		t.Fatal("a scalar select model must not masquerade as a multi-select array")
	}
}

func TestIterPreservesArrayOrderAndRejectsNonArrays(t *testing.T) {
	if got := Iter([]string{"one", "two"}); len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("Iter slice = %#v", got)
	}
	if got := Iter([2]int{3, 4}); len(got) != 2 || got[0] != 3 || got[1] != 4 {
		t.Fatalf("Iter array = %#v", got)
	}
	if got := Iter(map[string]any{"id": "row"}); got != nil {
		t.Fatalf("Iter object = %#v, want nil", got)
	}
}

func TestGet(t *testing.T) {
	if got := Get(map[string]any{"title": "ready"}, "title"); got != "ready" {
		t.Fatalf("Get map value = %#v", got)
	}
	if got := Get(map[string]any{}, "missing"); got != Undefined() {
		t.Fatalf("Get missing value = %#v, want undefined", got)
	}
	if got := Get("not-an-object", "title"); got != Undefined() {
		t.Fatalf("Get scalar value = %#v, want undefined", got)
	}
	if got := String(Get(map[string]any{}, "missing")); got != "undefined" {
		t.Fatalf("String missing value = %q", got)
	}
}
