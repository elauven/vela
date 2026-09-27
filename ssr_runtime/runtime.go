package ssr_runtime

import (
	"fmt"
	"html"
	"io"
	"reflect"
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

// Slot is a server-rendered component slot. Phase 2 will populate named slot
// fields with this type while the Phase 1 slot struct remains intentionally
// empty.
type Slot func(io.Writer) error

// WriteString preserves direct error propagation from a renderer write.
func WriteString(writer io.Writer, value string) error {
	_, err := io.WriteString(writer, value)
	return err
}

// String provides stable server text conversion for typed template values.
func String(value any) string {
	return fmt.Sprint(value)
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

// Get reads one plain JavaScript-style object field from data supplied to an
// SSR renderer. Vela's current generated prop surface represents unknown JS
// object values as map[string]any; absent or non-object fields intentionally
// become nil, matching an undefined-like render value instead of panicking.
func Get(value any, key string) any {
	switch object := value.(type) {
	case map[string]any:
		return object[key]
	case map[string]string:
		return object[key]
	case map[string]bool:
		return object[key]
	case map[string]float64:
		return object[key]
	default:
		return nil
	}
}

// EscapeString escapes text and attribute content for HTML output.
func EscapeString(value string) string {
	return html.EscapeString(value)
}
