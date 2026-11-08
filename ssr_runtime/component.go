package ssr_runtime

import (
	"context"
	"fmt"
	"io"
)

// Component is a component as a value (task 070): what a generated
// renderer exports as XComponent, and what <vela:component :this={…}>
// renders. An imported component used in an expression translates to its
// XComponent, so objects, arrays and conditionals can hold components.
type Component interface {
	// ComponentID is the component's $$id: its source path relative to the
	// project without the extension (routes/users/Page), the same as the
	// client factory's.
	ComponentID() string
	// RenderDynamic renders the component with props by name: a prop the
	// component does not declare is ignored, and the keys present are the
	// props supplied (Set). slots maps slot names to their content. ctx
	// reaches host functions; nil keeps the context of w.
	RenderDynamic(ctx context.Context, w io.Writer, props map[string]any, slots map[string]func(io.Writer) error, options DynamicOptions) error
}

// DynamicOptions controls RenderDynamic.
type DynamicOptions struct {
	// Static renders plain HTML without markers and payload (A-8.3), as the
	// app layer's static fragments need.
	Static bool
}

// DynamicWriter is the writer a generated RenderDynamic renders to: w with
// the context ctx, and static when options say so.
func DynamicWriter(w io.Writer, ctx context.Context, options DynamicOptions) io.Writer {
	if ctx != nil {
		w = WithContext(w, ctx)
	}
	if options.Static {
		w = StaticWriter(w)
	}
	return w
}

// ClientComponent is the value of a client-only component (<script
// context="client">, T-9.2a) as <vela:component> renders it (task 092): it
// has no Go renderer, so it writes the empty client range "f" that the
// browser fills after hydration, as for a client-only component tag
// (T-9.2b). A static rendering writes nothing.
func ClientComponent(id string) Component { return clientComponent(id) }

type clientComponent string

func (c clientComponent) ComponentID() string { return string(c) }

func (c clientComponent) RenderDynamic(ctx context.Context, w io.Writer, props map[string]any, slots map[string]func(io.Writer) error, options DynamicOptions) error {
	w = DynamicWriter(w, ctx, options)
	if err := OpenRange(w, "f"); err != nil {
		return err
	}
	return CloseRange(w)
}

// RenderDynamicComponent renders a <vela:component> site (S-6.12): nothing
// for null, undefined or false (an empty range with the state token "x"),
// or the component value in a range with the token "" after its "d" payload
// entry. Any other value is a render error naming where, the site's source
// position.
func RenderDynamicComponent(w io.Writer, site, where string, value any, props map[string]any, slots map[string]func(io.Writer) error) error {
	switch v := value.(type) {
	case nil, UndefinedValue:
		return emptyDynamicRange(w)
	case bool:
		if !v {
			return emptyDynamicRange(w)
		}
	case Component:
		if err := OpenRange(w, ""); err != nil {
			return err
		}
		if sw, ok := w.(*streamWriter); ok && !sw.static {
			sw.stream.entries = append(sw.stream.entries, []any{"d", site, v.ComponentID()})
		}
		if err := v.RenderDynamic(nil, w, props, slots, DynamicOptions{}); err != nil {
			return err
		}
		return CloseRange(w)
	}
	return fmt.Errorf("VELA-SSR-DYNAMIC: <vela:component> at %s: :this is %s, not a component", where, dynamicKind(value))
}

func emptyDynamicRange(w io.Writer) error {
	if err := OpenRange(w, "x"); err != nil {
		return err
	}
	return CloseRange(w)
}

// dynamicKind names a value as JavaScript typeof would.
func dynamicKind(value any) string {
	switch v := value.(type) {
	case string:
		if v == "" {
			return "an empty string"
		}
		return "string"
	case bool:
		return "boolean"
	case float64, float32, int, int64, int32, uint, uint64, uint32:
		return "number"
	}
	return "object"
}
