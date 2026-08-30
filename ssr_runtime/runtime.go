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

// EscapeString escapes text and attribute content for HTML output.
func EscapeString(value string) string {
	return html.EscapeString(value)
}
