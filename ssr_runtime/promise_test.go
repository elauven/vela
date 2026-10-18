package ssr_runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPromiseChain(t *testing.T) {
	js := NewJS()
	ctx := context.Background()
	double := Func(func(_ any, args ...any) any { return Arg(args, 0).(float64) * 2 })
	fail := Func(func(_ any, args ...any) any { js.Throw("boom", "s"); return Undefined() })
	handled := Func(func(_ any, args ...any) any { return "caught " + String(Arg(args, 0)) })
	if got := js.Await(ctx, js.Then(ctx, "s", Resolve(2.0), double, nil)); got != 4.0 {
		t.Fatalf("then = %v", got)
	}
	rejected := js.Then(ctx, "s", Resolve(1.0), fail, nil)
	if js.Failed() {
		t.Fatal("a rejection is not an exception until awaited")
	}
	if got := js.Await(ctx, js.CatchPromise(ctx, "s", rejected, handled)); got != "caught boom" {
		t.Fatalf("catch = %v", got)
	}
	// finally keeps the settlement unless its reaction throws.
	if got := js.Await(ctx, js.Finally(ctx, "s", Resolve(5.0), Func(func(any, ...any) any { return 9.0 }))); got != 5.0 {
		t.Fatalf("finally = %v", got)
	}
	overridden := js.Finally(ctx, "s", Resolve(5.0), fail)
	js.Await(ctx, overridden)
	var render *RenderError
	if !errors.As(js.Err(), &render) || render.Kind != "Thrown" {
		t.Fatalf("finally throw = %v", js.Err())
	}
}

func TestPromiseAllOrder(t *testing.T) {
	js := NewJS()
	ctx := context.Background()
	all := js.All("s", []any{Resolve(1.0), 2.0, Reject("first", "s"), Reject("second", "s")})
	if got := js.CatchPromise(ctx, "s", all, Func(func(_ any, args ...any) any { return Arg(args, 0) })); js.Await(ctx, got) != "first" {
		t.Fatal("Promise.all rejects with the first rejection in array order")
	}
	settled := js.Await(ctx, js.AllSettled(ctx, "s", []any{Resolve(1.0), Reject("x", "s")})).([]any)
	if status, _ := settled[1].(*Object).Lookup("status"); status != "rejected" {
		t.Fatalf("allSettled = %v", settled)
	}
}

func TestAwaitCancelledIsNotCaught(t *testing.T) {
	js := NewJS()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	caught := false
	Try(js, ctx, func() (any, bool) {
		js.Await(ctx, Resolve(1.0))
		return nil, false
	}, func(any) (any, bool) { caught = true; return nil, false }, nil)
	if caught || !errors.Is(js.Err(), context.Canceled) {
		t.Fatalf("caught=%v err=%v", caught, js.Err())
	}
}

// TestStateWithMethodsIsOmitted: JSON would drop the methods and accessors
// of an object, so state that holds one is recomputed by the client.
func TestStateWithMethodsIsOmitted(t *testing.T) {
	var out strings.Builder
	withMethod := NewObject().Set("n", 1.0).Set("m", Func(nil))
	withGetter := NewObject().DefineGetter("x", Func(func(any, ...any) any { return 1.0 }))
	state := NewObject().Set("a", 1.0).Set("o", withMethod).Set("l", []any{2.0, []any{withGetter}}).Set("d", NewObject().Set("k", "v"))
	w, root := BeginComponent(&out, "C", state)
	if err := EndComponent(w, root, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `["s","C",{"a":1,"d":{"k":"v"}},{"omitted":["o","l"]}]`) {
		t.Fatalf("payload = %s", out.String())
	}
}

func TestPromiseStateIsOmitted(t *testing.T) {
	var out strings.Builder
	w, root := BeginComponent(&out, "C", NewObject().Set("a", 1.0).Set("p", Resolve(1.0)).Set("f", Func(nil)).Set("b", 2.0))
	if err := EndComponent(w, root, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `["s","C",{"a":1,"b":2},{"omitted":["p","f"]}]`) {
		t.Fatalf("payload = %s", out.String())
	}
	if String(Resolve(1.0)) != "[object Promise]" {
		t.Fatal("promise text")
	}
}

func TestConsoleObserver(t *testing.T) {
	js := NewJS()
	var calls []string
	ctx := WithConsoleObserver(context.Background(), func(method string, args []any) {
		calls = append(calls, method+":"+String(args))
	})
	js.Console(ctx, "warn", "a", 1.0)
	js.Console(context.Background(), "log", "ignored")
	if len(calls) != 1 || calls[0] != "warn:a,1" {
		t.Fatalf("calls = %v", calls)
	}
}
