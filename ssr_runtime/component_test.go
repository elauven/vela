package ssr_runtime

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

type testComponent struct{ id string }

func (c testComponent) ComponentID() string { return c.id }

func (c testComponent) RenderDynamic(ctx context.Context, w io.Writer, props map[string]any, slots map[string]func(io.Writer) error, options DynamicOptions) error {
	w = DynamicWriter(w, ctx, options)
	w, root := BeginComponent(w, c.id, nil)
	defer LeaveComponent(w)
	if err := OpenRange(w, ""); err != nil {
		return err
	}
	if err := WriteString(w, "<b>"+String(props["label"])+"</b>"); err != nil {
		return err
	}
	if slot := slots["default"]; slot != nil {
		if err := slot(w); err != nil {
			return err
		}
	}
	if err := CloseRange(w); err != nil {
		return err
	}
	return EndComponent(w, root, nil)
}

// renderInRoot renders body inside a root component, so that markers and
// payload entries are written.
func renderInRoot(t *testing.T, body func(w io.Writer) error) (string, error) {
	t.Helper()
	var out bytes.Buffer
	w, root := BeginComponent(&out, "Root", nil)
	defer LeaveComponent(w)
	if err := body(w); err != nil {
		return out.String(), err
	}
	err := EndComponent(w, root, nil)
	return out.String(), err
}

func TestRenderDynamicComponent(t *testing.T) {
	slots := map[string]func(io.Writer) error{"default": func(w io.Writer) error { return WriteString(w, "<i>slot</i>") }}
	html, err := renderInRoot(t, func(w io.Writer) error {
		return RenderDynamicComponent(w, "Root#0", "Root.vela:1:2", testComponent{"routes/A"}, map[string]any{"label": "x"}, slots)
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `<!--[--><!--[--><b>x</b><i>slot</i><!--]--><!--]--><script type="application/json" data-vela-root>{"v":4,"props":{},"entries":[["d","Root#0","routes/A"]]}</script>`
	if html != want {
		t.Errorf("render:\n got %s\nwant %s", html, want)
	}
	for _, empty := range []any{nil, Undefined(), false} {
		html, err := renderInRoot(t, func(w io.Writer) error {
			return RenderDynamicComponent(w, "Root#0", "Root.vela:1:2", empty, nil, nil)
		})
		if err != nil || !strings.HasPrefix(html, `<!--[x--><!--]--><script`) || !strings.Contains(html, `"entries":[]`) {
			t.Errorf("%#v: %s, %v", empty, html, err)
		}
	}
	for value, kind := range map[any]string{"Chart": "string", "": "an empty string", 3.0: "number", true: "boolean"} {
		_, err := renderInRoot(t, func(w io.Writer) error {
			return RenderDynamicComponent(w, "Root#0", "Root.vela:1:2", value, nil, nil)
		})
		want := "VELA-SSR-DYNAMIC: <vela:component> at Root.vela:1:2: :this is " + kind + ", not a component"
		if err == nil || err.Error() != want {
			t.Errorf("%#v: error %v, want %q", value, err, want)
		}
	}
}

func TestRenderDynamicStatic(t *testing.T) {
	var out bytes.Buffer
	err := testComponent{"routes/A"}.RenderDynamic(context.Background(), &out, map[string]any{"label": "s"}, nil, DynamicOptions{Static: true})
	if err != nil || out.String() != "<b>s</b>" {
		t.Errorf("static render %q, %v", out.String(), err)
	}
}

func TestComponentValues(t *testing.T) {
	a, a2, b := testComponent{"routes/A"}, testComponent{"routes/A"}, testComponent{"routes/B"}
	if !StrictEqual(a, a2) || StrictEqual(a, b) || StrictEqual(a, nil) || StrictEqual("routes/A", a) {
		t.Error("components compare by identity")
	}
	if TypeOf(a) != "function" {
		t.Errorf("typeof a component is %q", TypeOf(a))
	}
}
