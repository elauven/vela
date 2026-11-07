package ssr_runtime

import "io"

// Component context (task 090, docs/SSR_TRANSLATION_CONTRACT.md §7.5).
//
// setContext, getContext and hasContext of `vela` run while a component
// starts: in the server renderer, in the prologue before the component's
// template. A renderer whose script calls setContext opens a context frame
// with EnterContext and closes it with a deferred LeaveContext, so its
// frame is on top while its prologue runs and stays below the frames of
// the children and slots it renders. getContext reads the innermost frame
// that holds the key: the component's own frame, then its ancestors' in the
// render tree, the same chain the browser runtime builds. Slot content is
// rendered by the component that opens the slot, so it sees that
// component's context, as in the browser.
//
// The frames belong to one render (the out of its writers): concurrent
// renders never share them. Context values are not sent in the payload; a
// component state value that is a context value of an ancestor is omitted
// from the stream, so the client takes it from its own context, where it
// is the same object (inheritedState).

type contextFrame struct {
	// depth is the number of components that began before the component
	// that opened the frame.
	depth  int
	values map[string]any
}

// renderOf returns the output of the render w writes to, or nil.
func renderOf(w io.Writer) *out {
	if sw, ok := w.(*streamWriter); ok {
		return sw.out
	}
	return nil
}

// EnterContext opens the context frame of the component that starts
// rendering to w. When w does not belong to a render yet, it starts one, as
// WithContext does; the component's BeginComponent then makes it the root.
func EnterContext(w io.Writer) io.Writer {
	sw, ok := w.(*streamWriter)
	if !ok {
		sw = &streamWriter{out: newOut(w), pending: true}
	}
	sw.out.frames = append(sw.out.frames, &contextFrame{depth: sw.out.depth})
	return sw
}

// LeaveContext closes the frame EnterContext opened. Renderers defer it.
func LeaveContext(w io.Writer) {
	if o := renderOf(w); o != nil && len(o.frames) > 0 {
		o.frames[len(o.frames)-1] = nil
		o.frames = o.frames[:len(o.frames)-1]
	}
}

// SetContext sets key in the frame of the starting component and returns
// value, as setContext does. Without a frame (a module function called by
// a component that never calls setContext itself) the value is not kept.
func (js *JS) SetContext(key string, value any) any {
	if js == nil || js.render == nil || len(js.render.frames) == 0 {
		return value
	}
	frame := js.render.frames[len(js.render.frames)-1]
	if frame.values == nil {
		frame.values = map[string]any{}
	}
	frame.values[key] = value
	return value
}

// lookupContext returns the value of key in the innermost frame that has it.
func (js *JS) lookupContext(key string) (any, bool) {
	if js == nil || js.render == nil {
		return nil, false
	}
	frames := js.render.frames
	for i := len(frames) - 1; i >= 0; i-- {
		if value, ok := frames[i].values[key]; ok {
			return value, true
		}
	}
	return nil, false
}

// GetContext returns the value an ancestor (or the component) set for key,
// or undefined.
func (js *JS) GetContext(key string) any {
	if value, ok := js.lookupContext(key); ok {
		return value
	}
	return Undefined()
}

// HasContext reports whether a value was set for key.
func (js *JS) HasContext(key string) bool {
	_, ok := js.lookupContext(key)
	return ok
}

// inheritedState moves to omitted the state values that are context values
// of an ancestor: objects and arrays the component took from its context.
// The client evaluates their initializers and gets the same object from its
// own context, so a $state.deep binding keeps sharing it after hydration.
func inheritedState(o *out, state any, omitted []string) (any, []string) {
	object, ok := state.(*Object)
	if !ok || o == nil || len(o.frames) == 0 {
		return state, omitted
	}
	inherited := map[any]bool{}
	for _, frame := range o.frames {
		// The frame of this component itself (BeginComponent has counted it).
		if frame.depth >= o.depth-1 {
			continue
		}
		for _, value := range frame.values {
			switch value.(type) {
			case *Object, *Array:
				inherited[value] = true
			}
		}
	}
	if len(inherited) == 0 {
		return state, omitted
	}
	var kept *Object
	for i, key := range object.keys {
		value := object.vals[i]
		reference := false
		switch value.(type) {
		case *Object, *Array:
			reference = inherited[value]
		}
		if reference {
			if kept == nil {
				kept = NewObject()
				for j := 0; j < i; j++ {
					kept.Set(object.keys[j], object.vals[j])
				}
			}
			omitted = append(omitted, key)
			continue
		}
		if kept != nil {
			kept.Set(key, value)
		}
	}
	if kept == nil {
		return state, omitted
	}
	return kept, omitted
}
