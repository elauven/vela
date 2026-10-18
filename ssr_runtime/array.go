package ssr_runtime

import "fmt"

// Array gives an array identity (task 057): translated code boxes an array
// that is copied into another variable or stored in another value, so that
// every holder observes a change, as in JavaScript. A local array that is
// never copied stays a []any and a change reassigns the local. Reads see
// the items through jsValue.
type Array struct {
	items []any
}

// Box boxes a []any; other values are returned unchanged.
func Box(value any) any {
	if items, ok := value.([]any); ok {
		return &Array{items: items}
	}
	return value
}

// MarshalJSON writes the items.
func (a *Array) MarshalJSON() ([]byte, error) { return appendJSON(nil, a.items) }

// MutateShared calls a method that changes an array through a value that
// may be held elsewhere (xs.push(x) on a boxed local, m.get(k).push(x),
// this.items.push(x)): a boxed array changes in place; an array of state,
// props, or a module fails the render (T-6.6b). Other receivers dispatch
// the method (an object's own push, a Map's set).
func (js *JS) MutateShared(site string, receiver any, name string, args ...any) any {
	if js.Failed() {
		return Undefined()
	}
	switch v := receiver.(type) {
	case *Array:
		result, out, _ := js.arrayMethod(site, v.items, name, args)
		if out != nil {
			v.items = out
		}
		return result
	case []any:
		return js.Impure(fmt.Sprintf("%s changes an array that a translated function did not create during this render (T-6.6b)", name), site)
	}
	return js.Method(site, receiver, name, args...)
}

// setItem assigns a[key] on a boxed array: an index beyond the length grows
// it with holes, length truncates or extends it.
func (js *JS) setItem(a *Array, name string, value any, site string) any {
	grown := js.SetElement(a.items, name, value, site)
	if items, ok := grown.([]any); ok {
		a.items = items
	}
	return value
}
