package ssr_runtime

import (
	"context"
	"errors"
	"fmt"
	"strconv"
)

// Translated functions (docs/SSR_TRANSLATION_CONTRACT.md §7, task 045).
// A JavaScript function becomes a Go closure of type Func. Exceptions use
// the render's evaluation state: throw records the value and the function
// returns; every statement that can throw is followed by a check, so the
// statements after a throw do not run, and try/catch/finally take the
// pending exception (T-6.5d).

// Func is a translated JavaScript function value.
type Func func(args ...any) any

// Arg returns argument i, or undefined when the call passed fewer.
func Arg(args []any, i int) any {
	if i < len(args) {
		return args[i]
	}
	return Undefined()
}

// Thrown is a value thrown by `throw` in translated code.
type Thrown struct {
	Value any
	Site  string
}

func (t *Thrown) Error() string {
	text := "Uncaught " + errorText(t.Value)
	if t.Site != "" {
		return t.Site + ": " + text
	}
	return text
}

func errorText(value any) string {
	if object, ok := value.(*Object); ok {
		name, _ := object.Lookup("name")
		message, _ := object.Lookup("message")
		return String(name) + ": " + String(message)
	}
	return String(value)
}

// Throw records a thrown value as the render's exception (T-6.5a).
func (js *JS) Throw(value any, site string) UndefinedValue {
	return js.fail(&Thrown{Value: value, Site: site})
}

// Failed reports a pending exception.
func (js *JS) Failed() bool {
	return js != nil && js.err != nil
}

// PublicError marks an error whose message may reach the browser
// (T-7.4h): a host error that wraps one is exposed to catch with this
// message.
type PublicError struct {
	Message string
	Err     error
}

func (e *PublicError) Error() string { return e.Message }
func (e *PublicError) Unwrap() error { return e.Err }

type errorObserverKey struct{}

// WithErrorObserver passes host errors that JavaScript code catches to
// observe, which receives the original error (T-7.4h).
func WithErrorObserver(ctx context.Context, observe func(error)) context.Context {
	return context.WithValue(ctx, errorObserverKey{}, observe)
}

// Catch takes the pending exception as the JavaScript value a catch
// binding receives: a thrown value as is, a runtime exception as an error
// object, and a host error as an Error whose message is public only when
// the host said so (T-7.4h).
func (js *JS) Catch(ctx context.Context) any {
	err := js.err
	js.err = nil
	var thrown *Thrown
	var runtime *RenderError
	var host *HostError
	switch {
	case errors.As(err, &thrown):
		return thrown.Value
	case errors.As(err, &runtime):
		return NewError(runtime.Kind, runtime.Message)
	case errors.As(err, &host):
		var public *PublicError
		if errors.As(host.Err, &public) {
			return NewError("Error", public.Message)
		}
		if ctx != nil {
			if observe, ok := ctx.Value(errorObserverKey{}).(func(error)); ok {
				observe(err)
			}
		}
		return NewError("Error", "Internal error")
	case err == nil:
		return Undefined()
	}
	return NewError("Error", "Internal error")
}

// Try runs try/catch/finally (T-6.5d). Each part returns its completion:
// a value and true for `return`, or false for normal completion. catch is
// nil without a handler, finally nil without a finalizer.
func Try(js *JS, ctx context.Context, try func() (any, bool), catch func(error any) (any, bool), finally func() (any, bool)) (any, bool) {
	if js.Failed() {
		return nil, false
	}
	value, returned := try()
	if js.Failed() && catch != nil && !cancelled(js.err) {
		value, returned = catch(js.Catch(ctx))
	}
	if finally == nil {
		return value, returned
	}
	pending := js.err
	js.err = nil
	finalValue, finalReturned := finally()
	if js.Failed() || finalReturned {
		// A throw or return in finally overrides the pending completion.
		return finalValue, finalReturned
	}
	js.err = pending
	return value, returned
}

// Call calls a function value (`f(x)` where f is a value).
func (js *JS) Call(site string, fn any, args ...any) any {
	if function, ok := fn.(Func); ok {
		return function(args...)
	}
	return js.throw("TypeError", fmt.Sprintf("%s is not a function", describe(jsValue(fn))), site)
}

// SetMember assigns object[key] = value for an object or array the
// function created (T-6.6b).
func (js *JS) SetMember(object, key, value any, site string) any {
	name := propertyKey(key)
	switch target := object.(type) {
	case *Object:
		if target.frozen {
			if target.find(name) >= 0 {
				return js.throw("TypeError", fmt.Sprintf("Cannot assign to read only property '%s' of object '#<Object>'", name), site)
			}
			return js.throw("TypeError", fmt.Sprintf("Cannot add property %s, object is not extensible", name), site)
		}
		target.Set(name, value)
		return value
	case []any:
		// Growing an array needs an array identity (task 046).
		if n, err := strconv.Atoi(name); err == nil && arrayIndexKey(name) {
			if n < len(target) {
				target[n] = value
				return value
			}
			return js.throw("RangeError", fmt.Sprintf("assigning index %d beyond the length %d of an array is not supported during server rendering", n, len(target)), site)
		}
		return value
	}
	if nullish(object) {
		return js.throw("TypeError", fmt.Sprintf("Cannot set properties of %s (setting '%s')", nullishName(object), name), site)
	}
	return value
}

// NewError creates an error object: new Error(message), new TypeError(…).
func NewError(kind string, message any) *Object {
	object := &Object{errorName: kind}
	if _, undefined := message.(UndefinedValue); !undefined {
		object.message, object.hasMessage = String(jsValue(message)), true
	}
	return object
}

// As lets errors.As view an uncaught error object as a *RenderError of its
// kind (TypeError, RangeError, Error, …); a thrown primitive has kind
// "Thrown".
func (t *Thrown) As(target any) bool {
	out, ok := target.(**RenderError)
	if !ok {
		return false
	}
	kind, message := "Thrown", String(t.Value)
	if object, isObject := t.Value.(*Object); isObject {
		if name, found := object.Lookup("name"); found {
			kind = String(name)
		}
		text, _ := object.Lookup("message")
		message = String(text)
	}
	*out = &RenderError{Kind: kind, Message: message, Site: t.Site}
	return true
}

// Impure fails the render when it calls a function that changes state,
// props, or module bindings (T-6.6a); such a function value is compiled
// because a render that does not call it is pure.
func (js *JS) Impure(message, site string) UndefinedValue {
	return js.fail(&RenderError{Kind: "Error", Message: "VELA-SSR-IMPURE: " + message, Site: site})
}
