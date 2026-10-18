package ssr_runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestDateTimeZoneDependentFailsRender: what depends on the time zone of
// the machine fails the render as unsupported, and catch does not receive
// it (the browser would compute another value).
func TestDateTimeZoneDependentFailsRender(t *testing.T) {
	cases := map[string]func(js *JS){
		"String":       func(js *JS) { String(js.NewDate("s", 0.0)) },
		"template":     func(js *JS) { TemplateString("at ", js.NewDate("s", 0.0)) },
		"plus":         func(js *JS) { Add(js.NewDate("s", 0.0), "") },
		"toString":     func(js *JS) { js.Method("s", js.NewDate("s", 0.0), "toString") },
		"local string": func(js *JS) { js.NewDate("s", "2026-10-18T10:00") },
		"other format": func(js *JS) { js.DateParse("s", "Oct 18, 2026") },
		"-000000":      func(js *JS) { js.DateParse("s", "-000000-01-01") },
	}
	for name, run := range cases {
		js := NewJS()
		caught := false
		Try(js, context.Background(), func() (any, bool) { run(js); return nil, false }, func(any) (any, bool) { caught = true; return nil, false }, nil)
		var unsupported *UnsupportedError
		if caught || !errors.As(js.Err(), &unsupported) || !strings.Contains(js.Err().Error(), "VELA-SSR-UNSUPPORTED") {
			t.Errorf("%s: caught=%v err=%v", name, caught, js.Err())
		}
	}
	js := NewJS()
	if n := ToNumber(js.NewDate("s", 5.0)); n != 5 || js.Err() != nil {
		t.Errorf("ToNumber(date) = %v, %v", n, js.Err())
	}
	if !Less(js.NewDate("s", 1.0), js.NewDate("s", 2.0)) || js.Err() != nil {
		t.Errorf("date comparison: %v", js.Err())
	}
}
