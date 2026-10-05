package ssr_runtime

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

type viewUser struct {
	Name    string    `json:"name"`
	Nick    string    `json:"nick,omitempty"`
	Secret  string    `json:"-"`
	Created time.Time `json:"created"`
	Plain   int
	Embedded
}

type Embedded struct {
	Role string `json:"role"`
}

func TestGetReadsTheJSONViewAndThrowsOnNullish(t *testing.T) {
	js := NewJS()
	user := viewUser{Name: "Ann", Secret: "x", Created: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Plain: 7, Embedded: Embedded{Role: "admin"}}
	for key, want := range map[string]any{
		"name": "Ann", "nick": Undefined(), "Secret": Undefined(), "created": "2026-01-02T03:04:05Z",
		"Plain": 7, "role": "admin", "missing": Undefined(),
	} {
		if got := js.Get(&user, key, "s"); !StrictEqual(got, want) {
			t.Errorf("user.%s = %#v, want %#v", key, got, want)
		}
	}
	object := NewObject().Set("b", 1.0).Set("a", []any{"x", "y"})
	if got := js.Length(js.Get(object, "a", "s"), "s"); got != 2.0 {
		t.Errorf("object.a.length = %v", got)
	}
	if got := js.Index([]any{"x", "y"}, 1.0, "s"); got != "y" {
		t.Errorf("array[1] = %v", got)
	}
	if got := js.Index([]any{"x"}, 5.0, "s"); !StrictEqual(got, Undefined()) {
		t.Errorf("array[5] = %v", got)
	}
	if got := js.Get("😀a", "length", "s"); got != 3.0 {
		t.Errorf("string length = %v, want 3 UTF-16 units", got)
	}
	if js.Err() != nil {
		t.Fatalf("unexpected exception: %v", js.Err())
	}
	if got := js.Get(nil, "name", "Page.vela:3:9"); !StrictEqual(got, Undefined()) {
		t.Fatalf("null.name = %v", got)
	}
	js.Get(Undefined(), "later", "other")
	err, ok := js.Err().(*RenderError)
	if !ok || err.Kind != "TypeError" || err.Site != "Page.vela:3:9" || err.Message != "Cannot read properties of null (reading 'name')" {
		t.Fatalf("first exception = %#v", js.Err())
	}
}

func TestObjectKeepsJavaScriptKeyOrder(t *testing.T) {
	object := NewObject().Set("b", 1.0).Set("10", 2.0).Set("a", 3.0).Set("2", 4.0).Set("b", 5.0)
	if got := strings.Join(object.Keys(), ","); got != "2,10,b,a" {
		t.Fatalf("keys = %s", got)
	}
	encoded, _ := json.Marshal(object)
	if string(encoded) != `{"2":4,"10":2,"b":5,"a":3}` {
		t.Fatalf("json = %s", encoded)
	}
	spread := NewObject().Set("x", 1.0).Spread(object).Spread(nil).Spread("hi")
	if got := strings.Join(spread.Keys(), ","); got != "0,1,2,10,x,b,a" {
		t.Fatalf("spread keys = %s", got)
	}
	if String(object) != "[object Object]" || !Truthy(NewObject()) || TypeOf(object) != "object" {
		t.Fatal("object conversions")
	}
}

func TestDynamicOperatorsFollowJavaScript(t *testing.T) {
	nan := math.NaN()
	numbers := []struct {
		name string
		got  float64
		want float64
	}{
		{"ToNumber('  12 ')", ToNumber("  12 "), 12},
		{"ToNumber('')", ToNumber(""), 0},
		{"ToNumber('0x1f')", ToNumber("0x1f"), 31},
		{"ToNumber('1e3')", ToNumber("1e3"), 1000},
		{"ToNumber('12px')", ToNumber("12px"), nan},
		{"ToNumber(null)", ToNumber(nil), 0},
		{"ToNumber(undefined)", ToNumber(Undefined()), nan},
		{"ToNumber(true)", ToNumber(true), 1},
		{"ToNumber([5])", ToNumber([]any{5.0}), 5},
		{"ToNumber([])", ToNumber([]any{}), 0},
		{"ToNumber({})", ToNumber(NewObject()), nan},
		{"'5' - 1", NumberSubtract("5", 1.0), 4},
		{"2 ** 10", Pow(2.0, 10.0), 1024},
		{"1 ** Infinity", Pow(1.0, math.Inf(1)), nan},
		{"(-8) % 3", NumberRemainder(-8.0, 3.0), -2},
	}
	for _, c := range numbers {
		if !(c.got == c.want || math.IsNaN(c.got) && math.IsNaN(c.want)) {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	values := []struct {
		name      string
		got, want any
	}{
		{"1 + '2'", Add(1.0, "2"), "12"},
		{"1 + 2", Add(1.0, 2.0), 3.0},
		{"true + 1", Add(true, 1.0), 2.0},
		{"null + 1", Add(nil, 1.0), 1.0},
		{"[1,2] + 3", Add([]any{1.0, 2.0}, 3.0), "1,23"},
		{"({}) + 1", Add(NewObject(), 1.0), "[object Object]1"},
		{"undefined + 'x'", Add(Undefined(), "x"), "undefinedx"},
		{"typeof null", TypeOf(nil), "object"},
		{"typeof undefined", TypeOf(Undefined()), "undefined"},
		{"typeof 1", TypeOf(3), "number"},
		{"typeof time", TypeOf(time.Unix(0, 0)), "string"},
	}
	for _, c := range values {
		if !StrictEqual(c.got, c.want) {
			t.Errorf("%s = %#v, want %#v", c.name, c.got, c.want)
		}
	}
	comparisons := []struct {
		name string
		got  bool
		want bool
	}{
		{"'10' < '9'", Less("10", "9"), true},
		{"'10' < 9", Less("10", 9.0), false},
		{"null < 1", Less(nil, 1.0), true},
		{"undefined < 1", Less(Undefined(), 1.0), false},
		{"undefined >= 1", GreaterEqual(Undefined(), 1.0), false},
		{"NaN <= NaN", LessEqual(nan, nan), false},
		{"2 <= 2", LessEqual(2.0, 2.0), true},
		{"'b' > 'a'", Greater("b", "a"), true},
		{"null == null", LooseNull(nil), true},
		{"undefined == null", LooseNull(Undefined()), true},
		{"0 == null", LooseNull(0.0), false},
	}
	for _, c := range comparisons {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestLazyHelpersEvaluateOnlyTheSelectedOperand(t *testing.T) {
	calls := 0
	right := func() any { calls++; return "r" }
	if NullishLazy(func() any { return 0.0 }, right) != 0.0 || calls != 0 {
		t.Fatal("0 ?? r must not evaluate r")
	}
	if NullishLazy(func() any { return nil }, right) != "r" || calls != 1 {
		t.Fatal("null ?? r")
	}
	rest := func(v any) any { calls++; return v }
	if !StrictEqual(OptionalChain(Undefined(), rest), Undefined()) || calls != 1 {
		t.Fatal("undefined?.x must not evaluate the chain")
	}
	if OptionalChain("v", rest) != "v" || calls != 2 {
		t.Fatal("v?.x")
	}
	js := NewJS()
	if got := JoinArrays([]any{1.0}, js.SpreadItems([]any{2.0, 3.0}, "s"), js.SpreadItems("😀a", "s")); len(got) != 5 || got[3] != "😀" {
		t.Fatalf("spread = %v", got)
	}
	js.SpreadItems(nil, "Page.vela:1:2")
	if err, ok := js.Err().(*RenderError); !ok || err.Message != "null is not iterable" {
		t.Fatalf("spread null = %v", js.Err())
	}
}
