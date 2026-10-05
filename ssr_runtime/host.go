package ssr_runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"
	"time"
)

// Developer host implementations (docs/SSR_TRANSLATION_CONTRACT.md §8).
// A //vela:host Go function stands in for a JavaScript function the
// compiler does not translate. The generated renderer calls it through
// CallHost, which converts the JavaScript arguments to the Go parameter
// types (§8.4), passes the render context, and turns a returned error into
// a *HostError. Panics of developer code are not recovered (H-8.5c).

// ErrHostNotImplemented is returned by scaffolded host stubs
// (--ssr-scaffold-hosts) until the developer implements them; a render that
// reaches one fails with a positioned *HostError (H-8.6b).
var ErrHostNotImplemented = errors.New("vela: host function is not implemented")

// HostError is a host function failure: the source position of the call,
// the JavaScript name, and the error the host returned.
type HostError struct {
	Site   string
	JSName string
	Err    error
}

func (e *HostError) Error() string {
	text := "host " + e.JSName + ": " + e.Err.Error()
	if e.Site != "" {
		return e.Site + ": " + text
	}
	return text
}

func (e *HostError) Unwrap() error { return e.Err }

// Optional receives a JavaScript argument that may be null or absent
// (undefined, or not passed): Null and Absent tell which; otherwise Value
// holds the converted argument (H-8.3a).
type Optional[T any] struct {
	Value  T
	Null   bool
	Absent bool
}

// ── Render context ───────────────────────────────────────────────────────

// WithContext attaches the render context to w. RenderXContext calls it;
// child renderers and slots receive the same writer and so the same
// context, which host functions with a context.Context parameter get
// (H-8.3c).
func WithContext(w io.Writer, ctx context.Context) io.Writer {
	if sw, ok := w.(*streamWriter); ok {
		copy := *sw
		copy.ctx = ctx
		return &copy
	}
	return &streamWriter{Writer: w, ctx: ctx, pending: true}
}

// ContextOf returns the render context carried by w, or
// context.Background().
func ContextOf(w io.Writer) context.Context {
	if sw, ok := w.(*streamWriter); ok && sw.ctx != nil {
		return sw.ctx
	}
	return context.Background()
}

// ── Calls ────────────────────────────────────────────────────────────────

var (
	contextType = reflect.TypeOf((*context.Context)(nil)).Elem()
	errorType   = reflect.TypeOf((*error)(nil)).Elem()
	timeType    = reflect.TypeOf(time.Time{})
)

// CallHost calls the host function fn for the JavaScript call name(args...)
// at site. Missing arguments are undefined and extra arguments are
// ignored, as in JavaScript. A conversion failure is a TypeError or a
// RangeError; an error result becomes a *HostError. Either is recorded in
// js and the call yields undefined. The result is a Go value read through
// its JSON view (T-5.1).
func CallHost(js *JS, ctx context.Context, site, name string, fn any, args ...any) any {
	function := reflect.ValueOf(fn)
	signature := function.Type()
	var in []reflect.Value
	offset := 0
	if signature.NumIn() > 0 && signature.In(0) == contextType {
		if ctx == nil {
			ctx = context.Background()
		}
		in = append(in, reflect.ValueOf(&ctx).Elem())
		offset = 1
	}
	fixed := signature.NumIn() - offset
	if signature.IsVariadic() {
		fixed--
	}
	for i := 0; i < fixed; i++ {
		var arg any = Undefined()
		if i < len(args) {
			arg = args[i]
		}
		value, err := hostArgument(arg, signature.In(offset+i))
		if err != nil {
			return js.throw(err.kind, fmt.Sprintf("argument %d of %s: %s", i+1, name, err.message), site)
		}
		in = append(in, value)
	}
	if signature.IsVariadic() {
		element := signature.In(signature.NumIn() - 1).Elem()
		for i := fixed; i < len(args); i++ {
			value, err := hostArgument(args[i], element)
			if err != nil {
				return js.throw(err.kind, fmt.Sprintf("argument %d of %s: %s", i+1, name, err.message), site)
			}
			in = append(in, value)
		}
	}
	out := function.Call(in)
	if len(out) > 0 && signature.Out(len(out)-1) == errorType {
		if failure, _ := out[len(out)-1].Interface().(error); failure != nil {
			return js.fail(&HostError{Site: site, JSName: name, Err: failure})
		}
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		return Undefined()
	}
	return out[0].Interface()
}

type conversionError struct {
	kind, message string
}

// hostArgument converts a JavaScript value to a Go parameter type (§8.4).
func hostArgument(value any, target reflect.Type) (reflect.Value, *conversionError) {
	if target.Kind() == reflect.Interface && target.NumMethod() == 0 {
		if value == nil {
			return reflect.Zero(target), nil
		}
		return reflect.ValueOf(&value).Elem(), nil
	}
	_, undefined := value.(UndefinedValue)
	isNull := value == nil
	if optionalType(target) {
		out := reflect.New(target).Elem()
		switch {
		case undefined:
			out.FieldByName("Absent").SetBool(true)
		case isNull:
			out.FieldByName("Null").SetBool(true)
		default:
			inner, err := hostArgument(value, target.Field(0).Type)
			if err != nil {
				return reflect.Value{}, err
			}
			out.Field(0).Set(inner)
		}
		return out, nil
	}
	if target.Kind() == reflect.Pointer {
		if isNull || undefined {
			return reflect.Zero(target), nil
		}
		inner, err := hostArgument(value, target.Elem())
		if err != nil {
			return reflect.Value{}, err
		}
		pointer := reflect.New(target.Elem())
		pointer.Elem().Set(inner)
		return pointer, nil
	}
	view := jsValue(value)
	mismatch := func() (reflect.Value, *conversionError) {
		return reflect.Value{}, &conversionError{"TypeError", fmt.Sprintf("Go type %s cannot hold %s", target, describe(view))}
	}
	switch {
	case target == timeType:
		text, ok := view.(string)
		if !ok {
			return mismatch()
		}
		parsed, err := time.Parse(time.RFC3339Nano, text)
		if err != nil {
			return reflect.Value{}, &conversionError{"RangeError", fmt.Sprintf("%q is not an RFC 3339 time", text)}
		}
		return reflect.ValueOf(parsed), nil
	case target.Kind() == reflect.String:
		text, ok := view.(string)
		if !ok {
			return mismatch()
		}
		return reflect.ValueOf(text).Convert(target), nil
	case target.Kind() == reflect.Bool:
		flag, ok := view.(bool)
		if !ok {
			return mismatch()
		}
		return reflect.ValueOf(flag).Convert(target), nil
	case target.Kind() == reflect.Float64 || target.Kind() == reflect.Float32:
		number, ok := view.(float64)
		if !ok {
			return mismatch()
		}
		return reflect.ValueOf(number).Convert(target), nil
	case integerKind(target.Kind()):
		number, ok := view.(float64)
		if !ok {
			return mismatch()
		}
		out := reflect.New(target).Elem()
		if math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number {
			return reflect.Value{}, &conversionError{"RangeError", fmt.Sprintf("%v is not an integer", number)}
		}
		if strings.HasPrefix(target.Kind().String(), "uint") {
			if number < 0 || out.OverflowUint(uint64(number)) || number > math.MaxUint64 {
				return reflect.Value{}, &conversionError{"RangeError", fmt.Sprintf("%v is out of range for %s", number, target)}
			}
			out.SetUint(uint64(number))
		} else {
			if number < math.MinInt64 || number > math.MaxInt64 || out.OverflowInt(int64(number)) {
				return reflect.Value{}, &conversionError{"RangeError", fmt.Sprintf("%v is out of range for %s", number, target)}
			}
			out.SetInt(int64(number))
		}
		return out, nil
	}
	// Slices, maps, and structs go through the JSON view.
	if isNull || undefined {
		return mismatch()
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return mismatch()
	}
	out := reflect.New(target)
	if err := json.Unmarshal(encoded, out.Interface()); err != nil {
		return mismatch()
	}
	return out.Elem(), nil
}

func optionalType(t reflect.Type) bool {
	return t.Kind() == reflect.Struct && t.PkgPath() == reflect.TypeOf(Optional[int]{}).PkgPath() && strings.HasPrefix(t.Name(), "Optional[")
}

func integerKind(kind reflect.Kind) bool {
	switch kind {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	}
	return false
}

func describe(view any) string {
	switch v := view.(type) {
	case nil:
		return "null"
	case UndefinedValue:
		return "undefined"
	case string:
		return fmt.Sprintf("the string %q", v)
	case float64:
		return "the number " + String(v)
	case bool:
		return fmt.Sprintf("the boolean %v", v)
	case []any:
		return "an array"
	}
	return "an object"
}
