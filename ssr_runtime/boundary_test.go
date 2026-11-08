package ssr_runtime

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// renderWithBoundary renders a root whose boundary body writes body bytes,
// starts a child with a state entry, and fails with fail.
func renderWithBoundary(t *testing.T, w interface {
	Write([]byte) (int, error)
}, body int, fail error) (string, error) {
	t.Helper()
	root, isRoot := BeginComponent(w, "Page", nil)
	defer LeaveComponent(root)
	if err := WriteString(root, "<main>"); err != nil {
		return "", err
	}
	mark := BeginBoundary(root)
	err := func() error {
		if err := OpenRange(root, ""); err != nil {
			return err
		}
		child, _ := BeginComponent(root, "Child", NewObject().Set("n", 1.0))
		defer LeaveComponent(child)
		if err := WriteString(child, strings.Repeat("x", body)); err != nil {
			return err
		}
		return fail
	}()
	if err != nil {
		value, err := CatchBoundary(root, mark, err, nil, "src/Page.vela:3:5")
		if err != nil {
			return "", err
		}
		message, _ := value.(*Object).Lookup("message")
		if err := OpenRange(root, "c"); err != nil {
			return "", err
		}
		if err := WriteString(root, "<p>"+String(message)+"</p>"); err != nil {
			return "", err
		}
		if err := CloseRange(root); err != nil {
			return "", err
		}
	} else {
		EndBoundary(mark)
	}
	if err := WriteString(root, "</main>"); err != nil {
		return "", err
	}
	return "", EndComponent(root, isRoot, nil)
}

func TestBoundaryDropsTheFailedBody(t *testing.T) {
	want := `<main><!--[c--><p>Internal error</p><!--]--></main><script type="application/json" data-vela-root>{"v":4,"props":{},"entries":[["b","src/Page.vela:3:5",{"message":"Internal error"}]]}</script>`
	for _, size := range []int{10, 3 * outChunk} {
		// A plain writer is buffered in chunks; a *bytes.Buffer is written
		// directly.
		var plain strings.Builder
		if _, err := renderWithBoundary(t, &plain, size, errors.New("db down")); err != nil {
			t.Fatal(err)
		}
		if plain.String() != want {
			t.Errorf("size %d:\n got %s\nwant %s", size, plain.String(), want)
		}
		var direct bytes.Buffer
		if _, err := renderWithBoundary(t, &direct, size, errors.New("db down")); err != nil {
			t.Fatal(err)
		}
		if direct.String() != want {
			t.Errorf("size %d, *bytes.Buffer:\n got %.300s\nwant %s", size, direct.String(), want)
		}
	}
	var public strings.Builder
	if _, err := renderWithBoundary(t, &public, 5, &HostError{Err: &PublicError{Message: "no stock", Err: errors.New("db")}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(public.String(), "<p>no stock</p>") || !strings.Contains(public.String(), `{"message":"no stock"}`) {
		t.Errorf("public message: %s", public.String())
	}
	var ok strings.Builder
	if _, err := renderWithBoundary(t, &ok, 3*outChunk, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ok.String(), strings.Repeat("x", 3*outChunk)) || !strings.Contains(ok.String(), `["s","Child",{"n":1}]`) || strings.Contains(ok.String(), `"b"`) {
		t.Errorf("a body without an error: %.200s", ok.String())
	}
}

func TestBoundaryPassesCancellation(t *testing.T) {
	var out strings.Builder
	if _, err := renderWithBoundary(t, &out, 10, context.Canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestBoundaryObserver(t *testing.T) {
	var seen error
	ctx := WithErrorObserver(context.Background(), func(err error) { seen = err })
	var out strings.Builder
	if _, err := renderWithBoundary(t, WithContext(&out, ctx), 10, errors.New("db down")); err != nil {
		t.Fatal(err)
	}
	var boundary *BoundaryError
	if !errors.As(seen, &boundary) || boundary.Source != "src/Page.vela:3:5" || boundary.Err.Error() != "db down" {
		t.Fatalf("observed %v", seen)
	}
}
