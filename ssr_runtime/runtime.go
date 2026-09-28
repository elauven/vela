package ssr_runtime

import (
	"fmt"
	"html"
	"io"
	"reflect"
	"strings"
	"unicode/utf16"
)

// Renderer is the composable server-side rendering contract.
type Renderer interface {
	Render(io.Writer) error
}

// RendererFunc adapts a direct rendering function to Renderer.
type RendererFunc func(io.Writer) error

func (render RendererFunc) Render(writer io.Writer) error {
	return render(writer)
}

// UndefinedValue represents JavaScript's absent-value sentinel inside the
// server expression subset. It deliberately differs from nil, which models a
// JavaScript null literal/value. The public Go render API still cannot infer
// the distinction for an untyped nil prop, so the compiler only emits this
// sentinel for source-level undefined and missing object properties.
type UndefinedValue struct{}

// Undefined returns the server representation of JavaScript undefined.
func Undefined() UndefinedValue { return UndefinedValue{} }

// Slot is a server-rendered component slot. Phase 2 will populate named slot
// fields with this type while the Phase 1 slot struct remains intentionally
// empty.
type Slot func(io.Writer) error

// WriteString preserves direct error propagation from a renderer write.
func WriteString(writer io.Writer, value string) error {
	_, err := io.WriteString(writer, value)
	return err
}

// String implements the template-facing subset of JavaScript text conversion.
// Interpolation renders null-like values empty; arrays join members with a
// comma; objects use JavaScript's default object tag rather than Go's map
// formatting. More specialised JavaScript values remain outside the baseline
// SSR expression subset.
func String(value any) string {
	if value == nil {
		return ""
	}
	if _, undefined := value.(UndefinedValue); undefined {
		return ""
	}
	reflected := reflect.ValueOf(value)
	for reflected.Kind() == reflect.Interface || reflected.Kind() == reflect.Pointer {
		if reflected.IsNil() {
			return ""
		}
		reflected = reflected.Elem()
	}
	if reflected.Kind() == reflect.Slice || reflected.Kind() == reflect.Array {
		parts := make([]string, reflected.Len())
		for index := 0; index < reflected.Len(); index++ {
			parts[index] = String(reflected.Index(index).Interface())
		}
		return strings.Join(parts, ",")
	}
	if reflected.Kind() == reflect.Map || reflected.Kind() == reflect.Struct {
		return "[object Object]"
	}
	return fmt.Sprint(value)
}

// TemplateString joins values after JavaScript template-literal conversion.
// The current compiler only emits it for non-nullish values (or literal
// quasis), but retaining the nil branch makes its behaviour explicit for the
// future typed null/undefined value model.
func TemplateString(values ...any) string {
	var out strings.Builder
	for _, value := range values {
		if value == nil {
			out.WriteString("null")
			continue
		}
		if _, undefined := value.(UndefinedValue); undefined {
			out.WriteString("undefined")
			continue
		}
		out.WriteString(String(value))
	}
	return out.String()
}

// Length returns JavaScript's .length for the baseline string/array subset.
// JavaScript counts UTF-16 code units rather than UTF-8 bytes or Go runes.
func Length(value any) int {
	if value == nil {
		return 0
	}
	if stringValue, ok := value.(string); ok {
		return len(utf16.Encode([]rune(stringValue)))
	}
	reflected := reflect.ValueOf(value)
	if reflected.Kind() == reflect.Slice || reflected.Kind() == reflect.Array || reflected.Kind() == reflect.Map {
		return reflected.Len()
	}
	return 0
}

// Truthy implements JavaScript truthiness for values admitted by the baseline
// SSR expression subset. Arrays and objects stay truthy even when empty.
func Truthy(value any) bool {
	if value == nil {
		return false
	}
	if _, undefined := value.(UndefinedValue); undefined {
		return false
	}
	switch value := value.(type) {
	case bool:
		return value
	case string:
		return value != ""
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return reflected.Int() != 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return reflected.Uint() != 0
	case reflect.Float32, reflect.Float64:
		return reflected.Float() != 0
	case reflect.Interface, reflect.Pointer:
		return !reflected.IsNil()
	default:
		return true
	}
}

// Ternary selects an already translated pure branch after JavaScript truthiness
// has been evaluated. Branch expressions in the baseline subset are side-effect
// free, so eager Go argument evaluation cannot change observable behavior.
func Ternary(condition bool, thenValue, elseValue any) any {
	if condition {
		return thenValue
	}
	return elseValue
}

// ContainsString reports whether a server-rendered multi-select model contains
// candidate after the same visible-value conversion used for regular select
// bindings. JavaScript select multiple has set-like membership semantics: an
// option is selected when its value occurs in the model, regardless of model
// order or duplicate entries.
//
// Generated prop/state values can be []string, []any, or another slice/array
// inferred by Go. Reflection keeps the generated renderer type-safe without
// forcing every application model through JSON or an untyped helper map.
func ContainsString(values any, candidate string) bool {
	if values == nil {
		return false
	}
	value := reflect.ValueOf(values)
	if value.Kind() != reflect.Slice && value.Kind() != reflect.Array {
		return false
	}
	for index := 0; index < value.Len(); index++ {
		if String(value.Index(index).Interface()) == candidate {
			return true
		}
	}
	return false
}

// Iter exposes the array-like subset of a JavaScript value to generated Go
// renderers. Props and object members that cross the SSR boundary are often
// represented as any, so ranging over them directly would not type-check in
// Go. The adapter preserves array order and makes non-array values render as
// an empty collection, matching the existing SSR fallback behaviour without
// panicking on malformed input.
func Iter(value any) []any {
	if value == nil {
		return nil
	}
	reflected := reflect.ValueOf(value)
	if reflected.Kind() != reflect.Slice && reflected.Kind() != reflect.Array {
		return nil
	}
	items := make([]any, reflected.Len())
	for index := range items {
		items[index] = reflected.Index(index).Interface()
	}
	return items
}

// Get reads one plain JavaScript-style object field from data supplied to an
// SSR renderer. Vela's current generated prop surface represents unknown JS
// object values as map[string]any; absent or non-object fields intentionally
// become nil, matching an undefined-like render value instead of panicking.
func Get(value any, key string) any {
	switch object := value.(type) {
	case map[string]any:
		result, ok := object[key]
		if !ok {
			return Undefined()
		}
		return result
	case map[string]string:
		result, ok := object[key]
		if !ok {
			return Undefined()
		}
		return result
	case map[string]bool:
		result, ok := object[key]
		if !ok {
			return Undefined()
		}
		return result
	case map[string]float64:
		result, ok := object[key]
		if !ok {
			return Undefined()
		}
		return result
	default:
		return Undefined()
	}
}

// EscapeString escapes text and attribute content for HTML output.
func EscapeString(value string) string {
	return html.EscapeString(value)
}
