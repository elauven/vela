package ssr_runtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
)

// Asynchronous code (docs/SSR_TRANSLATION_CONTRACT.md §7.4, task 045). The
// server runs async code in one synchronous pass: an async function runs
// to completion at its call and returns a settled Promise; await takes the
// fulfilled value or throws the rejection (T-7.4a, T-7.4b). There are no
// goroutines, event loop, or microtasks.

// Promise is a settled promise: fulfilled with a value or rejected with an
// exception.
type Promise struct {
	value  any
	reason error
}

// Fulfilled reports whether the promise was fulfilled.
func (p *Promise) Fulfilled() bool { return p.reason == nil }

// MarshalJSON keeps promises out of payloads: their JSON view is null.
func (p *Promise) MarshalJSON() ([]byte, error) { return []byte("null"), nil }

func (p *Promise) String() string { return "[object Promise]" }

// Resolve returns a promise fulfilled with value, or value itself when it
// is a promise (Promise.resolve).
func Resolve(value any) *Promise {
	if promise, ok := value.(*Promise); ok {
		return promise
	}
	return &Promise{value: value}
}

// Reject returns a promise rejected with value (Promise.reject).
func Reject(value any, site string) *Promise {
	return &Promise{reason: &Thrown{Value: value, Site: site}}
}

// Async finishes the body of an async function: an exception the body
// threw becomes the rejection of the returned promise. The caller checks
// that no exception was pending before the body ran.
func (js *JS) Async(result any) *Promise {
	if js.err != nil {
		reason := js.err
		js.err = nil
		return &Promise{reason: reason}
	}
	return Resolve(result)
}

// Await takes the value of a promise or throws its rejection; any other
// value is its own result. When ctx is cancelled the render stops with
// ctx.Err() (T-7.4g), which no catch clause takes.
func (js *JS) Await(ctx context.Context, value any) any {
	if js.Failed() {
		return Undefined()
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			js.fail(err)
			return Undefined()
		}
	}
	promise, ok := value.(*Promise)
	if !ok {
		return value
	}
	if promise.reason != nil {
		js.fail(promise.reason)
		return Undefined()
	}
	return promise.value
}

// cancelled reports an exception that stops the render without being
// catchable.
func cancelled(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// reasonValue is the JavaScript value of a rejection, as catch receives it.
func (js *JS) reasonValue(ctx context.Context, reason error) any {
	saved := js.err
	js.err = reason
	value := js.Catch(ctx)
	js.err = saved
	return value
}

// settle calls a reaction and settles its result: a returned promise is
// adopted, a thrown exception rejects.
func (js *JS) settle(reaction func() any) *Promise {
	result := reaction()
	return js.Async(result)
}

// Then is promise.then(onFulfilled, onRejected); a value that is not a
// promise is treated as a fulfilled one.
func (js *JS) Then(ctx context.Context, site string, value, onFulfilled, onRejected any) any {
	if js.Failed() {
		return Undefined()
	}
	promise := Resolve(value)
	if promise.reason == nil {
		if fn, ok := onFulfilled.(Func); ok {
			return js.settle(func() any { return fn(promise.value) })
		}
		return promise
	}
	if cancelled(promise.reason) {
		return promise
	}
	if fn, ok := onRejected.(Func); ok {
		reason := js.reasonValue(ctx, promise.reason)
		return js.settle(func() any { return fn(reason) })
	}
	return promise
}

// CatchPromise is promise.catch(onRejected).
func (js *JS) CatchPromise(ctx context.Context, site string, value, onRejected any) any {
	return js.Then(ctx, site, value, Undefined(), onRejected)
}

// Finally is promise.finally(onFinally): the reaction's own exception
// rejects; otherwise the original settlement is kept.
func (js *JS) Finally(ctx context.Context, site string, value, onFinally any) any {
	if js.Failed() {
		return Undefined()
	}
	promise := Resolve(value)
	if fn, ok := onFinally.(Func); ok {
		result := js.settle(func() any { return fn() })
		if result.reason != nil {
			return result
		}
	}
	return promise
}

// All is Promise.all over an array, in array order: the first rejection
// in array order rejects (T-7.4b).
func (js *JS) All(site string, values any) any {
	items := js.SpreadItems(values, site)
	if js.Failed() {
		return Undefined()
	}
	out := make([]any, len(items))
	for i, item := range items {
		promise := Resolve(item)
		if promise.reason != nil {
			return promise
		}
		out[i] = promise.value
	}
	return &Promise{value: out}
}

// AllSettled is Promise.allSettled over an array.
func (js *JS) AllSettled(ctx context.Context, site string, values any) any {
	items := js.SpreadItems(values, site)
	if js.Failed() {
		return Undefined()
	}
	out := make([]any, len(items))
	for i, item := range items {
		promise := Resolve(item)
		if promise.reason != nil {
			out[i] = NewObject().Set("status", "rejected").Set("reason", js.reasonValue(ctx, promise.reason))
		} else {
			out[i] = NewObject().Set("status", "fulfilled").Set("value", promise.value)
		}
	}
	return &Promise{value: out}
}

// AwaitSettle settles the operand of a server-evaluated {#await} block
// (T-7.4f) and records the snapshot of the selected branch in the payload:
// "t" with the value, or "c" with the error as catch receives it. A value
// that is not a promise selects then.
func AwaitSettle(w io.Writer, js *JS, site string, operand any) (string, any) {
	ctx := ContextOf(w)
	promise := Resolve(operand)
	if ctx != nil && ctx.Err() != nil {
		js.fail(ctx.Err())
		return "p", Undefined()
	}
	token, value := "t", promise.value
	snapshot := map[string]any{"state": "t", "value": value}
	if promise.reason != nil {
		if cancelled(promise.reason) {
			js.fail(promise.reason)
			return "p", Undefined()
		}
		token, value = "c", js.reasonValue(ctx, promise.reason)
		snapshot = map[string]any{"state": "c", "error": value}
	}
	if sw, ok := w.(*streamWriter); ok && !sw.static {
		sw.stream.entries = append(sw.stream.entries, []any{"a", site, snapshot})
	}
	return token, value
}

var _ json.Marshaler = (*Promise)(nil)

// MarshalJSON gives functions the view JSON.stringify gives them in an
// array: null.
func (Func) MarshalJSON() ([]byte, error) { return []byte("null"), nil }

// clientOnlyState moves the state values that cannot be restored from
// JSON, promises and functions, to omitted: the client evaluates their
// initializers instead (S-6.3).
func clientOnlyState(state any, omitted []string) (any, []string) {
	object, ok := state.(*Object)
	if !ok {
		return state, omitted
	}
	var kept *Object
	for _, key := range object.keys {
		switch object.values[key].(type) {
		case *Promise, Func:
			if kept == nil {
				kept = NewObject()
				for _, previous := range object.keys {
					if previous == key {
						break
					}
					kept.Set(previous, object.values[previous])
				}
			}
			omitted = append(omitted, key)
			continue
		}
		if kept != nil {
			kept.Set(key, object.values[key])
		}
	}
	if kept == nil {
		return state, omitted
	}
	return kept, omitted
}
