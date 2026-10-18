package ssr_runtime

import (
	"strings"
	"testing"
)

// TestMutateShared: a boxed array changes in place for every holder; an
// array that is not boxed (state, props, a module value) does not change.
func TestMutateShared(t *testing.T) {
	js := NewJS()
	box := Box([]any{1.0}).(*Array)
	holder := NewObject().Set("xs", box)
	js.MutateShared("s", property(holder, "xs"), "push", 2.0)
	if got := String(box); got != "1,2" || js.Err() != nil {
		t.Fatalf("boxed push = %q, %v", got, js.Err())
	}
	if StrictEqual(property(holder, "xs"), box) != true || Length(box) != 2 || len(Iter(box)) != 2 {
		t.Fatal("box identity or length")
	}
	raw := []any{1.0}
	js.MutateShared("s", raw, "push", 2.0)
	if js.Err() == nil || !strings.Contains(js.Err().Error(), "VELA-SSR-IMPURE") || len(raw) != 1 {
		t.Fatalf("raw push: %v", js.Err())
	}
	js = NewJS()
	pushed := 0.0
	own := NewObject().Set("push", Func(func(_ any, args ...any) any { pushed = args[0].(float64); return 7.0 }))
	if got := js.MutateShared("s", own, "push", 3.0); got != 7.0 || pushed != 3 {
		t.Fatalf("own push = %v, %v", got, pushed)
	}
	if encoded, _ := MarshalHydrationPayload(NewObject().Set("xs", box)); string(encoded) != `{"xs":[1,2]}` {
		t.Fatalf("payload = %s", encoded)
	}
}
