package ssr_runtime

import (
	"fmt"
	"html"
	"io"
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
