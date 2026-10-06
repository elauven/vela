package ssr_runtime

import (
	"context"
	"errors"
	"testing"
)

func TestTryCatchFinally(t *testing.T) {
	js := NewJS()
	var log []string
	value, returned := Try(js, context.Background(),
		func() (any, bool) { js.Throw("boom", "s"); return nil, false },
		func(caught any) (any, bool) { log = append(log, String(caught)); return "c", true },
		func() (any, bool) { log = append(log, "f"); return nil, false })
	if value != "c" || !returned || js.Failed() || len(log) != 2 || log[0] != "boom" {
		t.Fatalf("value=%v returned=%v failed=%v log=%v", value, returned, js.Failed(), log)
	}
	// finally without catch keeps the pending exception.
	Try(js, nil, func() (any, bool) { js.Throw(NewError("TypeError", "x"), "s"); return nil, false }, nil,
		func() (any, bool) { return nil, false })
	var render *RenderError
	if !errors.As(js.Err(), &render) || render.Kind != "TypeError" || render.Message != "x" {
		t.Fatalf("pending = %v", js.Err())
	}
}

func TestCatchHostErrorIsPrivate(t *testing.T) {
	js := NewJS()
	var observed error
	ctx := WithErrorObserver(context.Background(), func(err error) { observed = err })
	js.fail(&HostError{JSName: "load", Err: errors.New("db password wrong")})
	caught := js.Catch(ctx).(*Object)
	if message, _ := caught.Lookup("message"); message != "Internal error" || observed == nil {
		t.Fatalf("message=%v observed=%v", message, observed)
	}
	js.fail(&HostError{JSName: "load", Err: &PublicError{Message: "Not found"}})
	if message, _ := js.Catch(ctx).(*Object).Lookup("message"); message != "Not found" {
		t.Fatalf("public message=%v", message)
	}
}

func TestSetMemberAndCall(t *testing.T) {
	js := NewJS()
	object := NewObject()
	js.SetMember(object, "a", 1.0, "s")
	if value, _ := object.Lookup("a"); value != 1.0 {
		t.Fatal(value)
	}
	js.SetMember([]any{1.0}, 3.0, 2.0, "s")
	if !js.Failed() {
		t.Fatal("growing an array must fail")
	}
	js = NewJS()
	if js.Call("s", Func(func(args ...any) any { return Arg(args, 1) }), 1.0) != Undefined() {
		t.Fatal("missing argument must be undefined")
	}
	js.Call("s", 3.0)
	var render *RenderError
	if !errors.As(js.Err(), &render) || render.Kind != "TypeError" {
		t.Fatalf("calling a number: %v", js.Err())
	}
}
