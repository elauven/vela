package ssr_runtime

import (
	"bytes"
	"fmt"
	"io"
	"sync"
	"testing"
)

// renderWithContext renders a parent that sets "user" and a child that
// reads it, the way generated renderers call the runtime.
func renderWithContext(w io.Writer, user string) (string, error) {
	w = EnterContext(w)
	defer LeaveContext(w)
	js := NewJSFor(w)
	js.SetContext("user", user)
	w, root := BeginComponent(w, "Parent", nil)
	defer LeaveComponent(w)
	child := func(w io.Writer) error {
		js := NewJSFor(w)
		value := js.GetContext("user")
		w, root := BeginComponent(w, "Child", nil)
		defer LeaveComponent(w)
		if err := WriteString(w, String(value)); err != nil {
			return err
		}
		return EndComponent(w, root, nil)
	}
	if err := child(w); err != nil {
		return "", err
	}
	if js.HasContext("missing") {
		return "", fmt.Errorf("missing key is present")
	}
	return "", EndComponent(w, root, nil)
}

func TestContextFramesBelongToOneRender(t *testing.T) {
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var buf bytes.Buffer
			user := fmt.Sprintf("user-%d", i)
			if _, err := renderWithContext(&buf, user); err != nil {
				errs <- err
				return
			}
			if !bytes.Contains(buf.Bytes(), []byte(user)) {
				errs <- fmt.Errorf("render %d wrote %q", i, buf.String())
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestContextLookupAndScope(t *testing.T) {
	var buf bytes.Buffer
	w := EnterContext(&buf)
	js := NewJSFor(w)
	js.SetContext("a", 1.0)
	inner := EnterContext(w)
	js2 := NewJSFor(inner)
	if got := js2.GetContext("a"); got != 1.0 {
		t.Fatalf("inherited value = %v", got)
	}
	js2.SetContext("a", 2.0)
	if got := js.GetContext("a"); got != 2.0 {
		t.Fatalf("the innermost frame wins while it is open: %v", got)
	}
	LeaveContext(inner)
	if got := js.GetContext("a"); got != 1.0 {
		t.Fatalf("after the child frame closed: %v", got)
	}
	if _, ok := js.GetContext("none").(UndefinedValue); !ok {
		t.Fatalf("a missing key is undefined")
	}
	LeaveContext(w)
	if NewJS().HasContext("a") {
		t.Fatalf("a JS outside a render has no context")
	}
}

// A state value that is an ancestor's context value is omitted from the
// payload; the component's own context values are not.
func TestInheritedStateIsOmitted(t *testing.T) {
	var buf bytes.Buffer
	shared := NewObject().Set("name", "dark")
	w := EnterContext(&buf)
	NewJSFor(w).SetContext("theme", shared)
	w, root := BeginComponent(w, "Parent", NewObject().Set("theme", shared))
	child := EnterContext(w)
	NewJSFor(child).SetContext("own", NewObject())
	cw, croot := BeginComponent(child, "Child", NewObject().Set("theme", shared).Set("n", 1.0))
	if err := EndComponent(cw, croot, nil); err != nil {
		t.Fatal(err)
	}
	LeaveComponent(cw)
	LeaveContext(child)
	if err := EndComponent(w, root, nil); err != nil {
		t.Fatal(err)
	}
	LeaveComponent(w)
	LeaveContext(w)
	got := buf.String()
	for _, want := range []string{`["s","Parent",{"theme":{"name":"dark"}}]`, `["s","Child",{"n":1},{"omitted":["theme"]}]`} {
		if !bytes.Contains([]byte(got), []byte(want)) {
			t.Errorf("payload misses %s:\n%s", want, got)
		}
	}
}
