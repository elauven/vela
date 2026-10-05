package ssr_runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type hostKey struct{}

type money struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

func TestCallHostConvertsArgumentsAndPassesContext(t *testing.T) {
	ctx := context.WithValue(context.Background(), hostKey{}, "ru")
	format := func(ctx context.Context, m money, digits int, at time.Time, label Optional[string], extra ...string) (string, error) {
		return strings.Join(append([]string{ctx.Value(hostKey{}).(string), m.Currency, String(m.Amount), String(digits), at.UTC().Format("15:04"), String(label.Absent)}, extra...), "|"), nil
	}
	js := NewJS()
	got := CallHost(js, ctx, "App.vela:3:9", "format", format,
		NewObject().Set("amount", 12.5).Set("currency", "RUB"), 2.0, "2026-10-05T10:30:00Z", Undefined(), "a", "b")
	if js.Err() != nil || got != "ru|RUB|12.5|2|10:30|true|a|b" {
		t.Fatalf("call = %v, %v", got, js.Err())
	}
	if got := CallHost(js, ctx, "s", "one", func(v any) any { return v }); !StrictEqual(got, Undefined()) {
		t.Fatalf("missing argument = %#v, want undefined", got)
	}
	pointer := func(p *money) bool { return p == nil }
	if got := CallHost(js, ctx, "s", "p", pointer, nil); got != true {
		t.Fatal("null to pointer")
	}
}

func TestCallHostReportsConversionAndHostErrors(t *testing.T) {
	cases := []struct {
		name string
		fn   any
		args []any
		want string
	}{
		{"string-from-number", func(s string) string { return s }, []any{1.0}, "TypeError: argument 1 of f: Go type string cannot hold the number 1"},
		{"int-fraction", func(n int) int { return n }, []any{1.5}, "RangeError: argument 1 of f: 1.5 is not an integer"},
		{"uint8-range", func(n uint8) uint8 { return n }, []any{300.0}, "RangeError: argument 1 of f: 300 is out of range for uint8"},
		{"time-format", func(at time.Time) bool { return true }, []any{"yesterday"}, "RangeError: argument 1 of f: \"yesterday\" is not an RFC 3339 time"},
		{"undefined-into-string", func(s string) string { return s }, []any{}, "TypeError: argument 1 of f: Go type string cannot hold undefined"},
		{"host-error", func() (string, error) { return "", errors.New("boom") }, nil, "App.vela:1:2: host f: boom"},
		{"not-implemented", func() (string, error) { return "", ErrHostNotImplemented }, nil, "App.vela:1:2: host f: vela: host function is not implemented"},
	}
	for _, c := range cases {
		js := NewJS()
		if got := CallHost(js, nil, "App.vela:1:2", "f", c.fn, c.args...); !StrictEqual(got, Undefined()) {
			t.Errorf("%s: result %v", c.name, got)
		}
		if js.Err() == nil || !strings.Contains(js.Err().Error(), c.want) {
			t.Errorf("%s: error %v, want %q", c.name, js.Err(), c.want)
		}
	}
	js := NewJS()
	CallHost(js, nil, "s", "f", func() (string, error) { return "", ErrHostNotImplemented })
	var host *HostError
	if !errors.As(js.Err(), &host) || !errors.Is(js.Err(), ErrHostNotImplemented) {
		t.Fatalf("host error chain: %v", js.Err())
	}
}

func TestContextTravelsWithTheWriter(t *testing.T) {
	ctx := context.WithValue(context.Background(), hostKey{}, "x")
	var out strings.Builder
	w := WithContext(&out, ctx)
	inner, root := BeginComponent(w, "Page", nil)
	if !root || ContextOf(inner).Value(hostKey{}) != "x" {
		t.Fatal("root render loses the context")
	}
	child, childRoot := BeginComponent(inner, "Child", nil)
	if childRoot || ContextOf(child).Value(hostKey{}) != "x" {
		t.Fatal("child render loses the context")
	}
	static := StaticWriter(WithContext(&out, ctx))
	if _, root := BeginComponent(static, "Page", nil); root || ContextOf(static).Value(hostKey{}) != "x" {
		t.Fatal("static render")
	}
	if ContextOf(&out) != context.Background() {
		t.Fatal("plain writer context")
	}
}
