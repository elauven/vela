package ssr_runtime

import (
	"strings"
	"testing"
)

// TestCollectionChangesOnlyLocal: a Map or Set that a translated function
// did not create during the render (state, a module value) does not change.
func TestCollectionChangesOnlyLocal(t *testing.T) {
	for _, name := range []string{"set", "delete", "clear"} {
		js := NewJS()
		m := js.NewCollection("s", false, false, []any{[]any{1.0, 2.0}})
		js.Method("s", m, name, 1.0, 3.0)
		if js.Err() == nil || !strings.Contains(js.Err().Error(), "VELA-SSR-IMPURE") || m.size != 1 {
			t.Errorf("%s: err=%v size=%d", name, js.Err(), m.size)
		}
	}
	js := NewJS()
	s := js.NewCollection("s", true, true, nil)
	js.Method("s", s, "add", Func(func(any, ...any) any { return nil }))
	if js.Err() == nil || !strings.Contains(js.Err().Error(), "VELA-SSR-UNSUPPORTED") {
		t.Errorf("function key: %v", js.Err())
	}
}
