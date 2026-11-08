package ssr_runtime

import (
	"errors"
	"fmt"
	"io"
)

// Error boundaries (task 091). A {#boundary} body renders after
// BeginBoundary; when it returns an error, CatchBoundary drops what the
// body wrote (its HTML and its payload entries) and returns the error value
// of the catch branch, which the renderer writes in a range with the state
// token "c" after the "b" entry CatchBoundary records. The client adopts
// that branch without running the body (docs/HYDRATION_ARCHITECTURE.md).
//
// Only the message of a PublicError reaches the browser; any other error
// shows "Internal error". The original error goes to the error observer of
// the render context (WithErrorObserver) as a *BoundaryError.

// Boundary is the position where a boundary body starts.
type Boundary struct {
	out       *out
	buf       int
	direct    int
	entries   int
	frames    int
	hostScope string
}

// BoundaryError is a render error a boundary caught: Source is the
// position of the {#boundary} block, "Card.vela:12:3".
type BoundaryError struct {
	Source string
	Err    error
}

func (e *BoundaryError) Error() string {
	return fmt.Sprintf("{#boundary} at %s caught: %v", e.Source, e.Err)
}

func (e *BoundaryError) Unwrap() error { return e.Err }

// BeginBoundary marks the start of a boundary body written to w. Until
// EndBoundary or CatchBoundary the render keeps its output buffered, so
// that a failure can still take it back.
func BeginBoundary(w io.Writer) Boundary {
	sw, ok := w.(*streamWriter)
	if !ok {
		return Boundary{}
	}
	o := sw.out
	o.hold++
	mark := Boundary{out: o, buf: len(o.buf), frames: len(o.frames), hostScope: sw.hostScope}
	if o.direct != nil {
		mark.direct = o.direct.Len()
	}
	if sw.stream != nil {
		mark.entries = len(sw.stream.entries)
	}
	return mark
}

// EndBoundary finishes a body that rendered without an error.
func EndBoundary(mark Boundary) {
	if mark.out != nil {
		mark.out.hold--
	}
}

// CatchBoundary handles the error of a boundary body. An error the boundary
// does not catch is returned as is: a failure of the output writer and a
// cancelled or expired request. Otherwise the body's output is dropped, a
// pending JavaScript exception of js is cleared, the "b" entry is recorded,
// and the value of the catch branch's error binding is returned: an Error
// whose message is public only for a PublicError.
func CatchBoundary(w io.Writer, mark Boundary, err error, js *JS, source string) (any, error) {
	if mark.out != nil {
		mark.out.hold--
	}
	if err == nil {
		return Undefined(), nil
	}
	if cancelled(err) {
		return nil, err
	}
	sw, ok := w.(*streamWriter)
	if ok && sw.out.err != nil {
		return nil, err
	}
	if js != nil {
		js.err = nil
	}
	if ok && mark.out == sw.out {
		o := sw.out
		if o.direct != nil {
			o.direct.Truncate(mark.direct)
		} else if len(o.buf) >= mark.buf {
			o.buf = o.buf[:mark.buf]
		}
		for i := mark.frames; i < len(o.frames); i++ {
			o.frames[i] = nil
		}
		if len(o.frames) > mark.frames {
			o.frames = o.frames[:mark.frames]
		}
		sw.hostScope = mark.hostScope
		if sw.stream != nil && len(sw.stream.entries) >= mark.entries {
			sw.stream.entries = sw.stream.entries[:mark.entries]
		}
	}
	message := "Internal error"
	var public *PublicError
	if errors.As(err, &public) {
		message = public.Message
	}
	if ok && !sw.static && sw.stream != nil {
		sw.stream.entries = append(sw.stream.entries, []any{"b", source, map[string]any{"message": message}})
	}
	if observe, found := ContextOf(w).Value(errorObserverKey{}).(func(error)); found {
		observe(&BoundaryError{Source: source, Err: err})
	}
	return NewError("Error", message), nil
}
