package ssr_runtime

import (
	"fmt"
	"math"
	"reflect"
)

// Map and Set (docs/SSR_TRANSLATION_CONTRACT.md T-6.6f, task 057): entries
// in insertion order, keys compared with SameValueZero (NaN equals NaN, +0
// equals -0; objects, arrays and other references by identity). A deleted
// entry leaves a tombstone, so forEach visits the entries added during the
// iteration and skips the deleted ones, as in JavaScript.

// Collection is a JavaScript Map, or a Set when set is true (its values are
// the keys).
type Collection struct {
	keys, vals []any
	live       []bool
	index      map[any]int
	size       int
	set        bool
	// local marks a collection a translated function created during this
	// render: only such a collection may change (T-6.6b).
	local bool
}

// nanKey is the key of NaN.
type nanKey struct{}

// referenceKey identifies an array or a Go map by its storage.
type referenceKey struct {
	kind     reflect.Kind
	pointer  uintptr
	len, cap int
}

// collectionKey normalises a key for SameValueZero. ok is false for a key
// without a stable identity on the server (a function value, a Go struct).
func collectionKey(key any) (any, bool) {
	switch k := key.(type) {
	case nil, UndefinedValue, bool, string, *Object, *Date, *Promise, *Collection:
		return k, true
	case Func:
		return nil, false
	}
	switch v := jsValue(key).(type) {
	case float64:
		if math.IsNaN(v) {
			return nanKey{}, true
		}
		return v + 0, true
	case bool, string:
		return v, true
	}
	reflected := reflect.ValueOf(key)
	switch reflected.Kind() {
	case reflect.Slice:
		return referenceKey{kind: reflect.Slice, pointer: reflected.Pointer(), len: reflected.Len(), cap: reflected.Cap()}, true
	case reflect.Map, reflect.Pointer:
		return referenceKey{kind: reflected.Kind(), pointer: reflected.Pointer()}, true
	}
	return nil, false
}

func newCollection(set, local bool) *Collection {
	return &Collection{index: map[any]int{}, set: set, local: local}
}

func (c *Collection) find(key any) (int, bool) {
	normal, ok := collectionKey(key)
	if !ok {
		return -1, false
	}
	i, found := c.index[normal]
	return i, found
}

// put sets key to value, appending a new entry at the end.
func (c *Collection) put(js *JS, site string, key, value any) bool {
	normal, ok := collectionKey(key)
	if !ok {
		js.unsupported(fmt.Sprintf("a %s key must be a primitive, an object, an array, a Date, or a collection during server rendering", c.kindName()), site)
		return false
	}
	if f, isNumber := jsValue(key).(float64); isNumber && f == 0 {
		key = 0.0 // -0 is stored as +0
	}
	if i, found := c.index[normal]; found {
		c.vals[i] = value
		return true
	}
	c.index[normal] = len(c.keys)
	c.keys = append(c.keys, key)
	c.vals = append(c.vals, value)
	c.live = append(c.live, true)
	c.size++
	return true
}

func (c *Collection) remove(key any) bool {
	i, found := c.find(key)
	if !found {
		return false
	}
	normal, _ := collectionKey(key)
	delete(c.index, normal)
	c.live[i] = false
	c.keys[i], c.vals[i] = nil, nil
	c.size--
	return true
}

func (c *Collection) clear() {
	for i := range c.live {
		c.live[i] = false
		c.keys[i], c.vals[i] = nil, nil
	}
	c.index = map[any]int{}
	c.size = 0
}

func (c *Collection) kindName() string {
	if c.set {
		return "Set"
	}
	return "Map"
}

// items returns the iteration of name ("entries", "keys", "values"; ""
// iterates the collection itself: entries of a Map, values of a Set).
func (c *Collection) items(name string) []any {
	if name == "" && c.set {
		name = "values"
	}
	out := make([]any, 0, c.size)
	for i, live := range c.live {
		if !live {
			continue
		}
		key, value := c.keys[i], c.vals[i]
		if c.set {
			value = key
		}
		switch name {
		case "keys":
			out = append(out, key)
		case "values":
			out = append(out, value)
		default:
			out = append(out, []any{key, value})
		}
	}
	return out
}

// NewCollection is new Map(iterable) or new Set(iterable); local marks a
// collection created by a translated function.
func (js *JS) NewCollection(site string, set, local bool, iterable any) *Collection {
	c := newCollection(set, local)
	if nullish(iterable) {
		return c
	}
	items := js.SpreadItems(iterable, site)
	for _, item := range items {
		if js.Failed() {
			return c
		}
		if set {
			c.put(js, site, item, item)
			continue
		}
		if nullish(item) || TypeOf(item) != "object" {
			js.throw("TypeError", fmt.Sprintf("Iterator value %s is not an entry object", String(jsValue(item))), site)
			return c
		}
		c.put(js, site, property(jsValue(item), "0"), property(jsValue(item), "1"))
	}
	return c
}

// MapGroupBy is Map.groupBy(items, fn): a Map from fn(item, index), compared
// with SameValueZero, to the items of each group, in first appearance order.
func (js *JS) MapGroupBy(site string, items, callback any, local bool) any {
	values := js.SpreadItems(items, site)
	if js.Failed() {
		return Undefined()
	}
	fn, ok := js.callable(site, "groupBy", callback)
	if !ok {
		return Undefined()
	}
	out := newCollection(false, local)
	for i, item := range values {
		key := fn(Undefined(), item, float64(i))
		if js.Failed() {
			return Undefined()
		}
		if at, found := out.find(key); found {
			out.vals[at] = append(out.vals[at].([]any), item)
		} else if !out.put(js, site, key, []any{item}) {
			return Undefined()
		}
	}
	return out
}

// collectionMethodNames are the methods Method dispatches on a Map and on a
// Set; keys, values and entries are iterated where they are consumed
// (ArrayIterItems).
var mapMethodNames = map[string]bool{"get": true, "set": true, "has": true, "delete": true, "clear": true, "forEach": true}

var setMethodNames = map[string]bool{"add": true, "has": true, "delete": true, "clear": true, "forEach": true}

var collectionPrototypeNames = map[string]bool{"constructor": true, "size": true, "keys": true, "values": true, "entries": true}

func (c *Collection) methodNames() map[string]bool {
	if c.set {
		return setMethodNames
	}
	return mapMethodNames
}

// collectionMethod calls a method of a Map or a Set. The methods that
// change the collection are admitted on a collection a translated function
// created during this render.
func (js *JS) collectionMethod(site string, c *Collection, name string, args []any) (any, bool) {
	if !c.methodNames()[name] {
		return nil, false
	}
	arg := func(i int) any {
		if i < len(args) {
			return args[i]
		}
		return Undefined()
	}
	switch name {
	case "set", "add", "delete", "clear":
		if !c.local {
			return js.Impure(fmt.Sprintf("%s.%s changes a collection that a translated function did not create during this render (T-6.6b)", c.kindName(), name), site), true
		}
	}
	switch name {
	case "get":
		if i, found := c.find(arg(0)); found {
			return c.vals[i], true
		}
		return Undefined(), true
	case "has":
		_, found := c.find(arg(0))
		return found, true
	case "set":
		c.put(js, site, arg(0), arg(1))
		return c, true
	case "add":
		c.put(js, site, arg(0), arg(0))
		return c, true
	case "delete":
		return c.remove(arg(0)), true
	case "clear":
		c.clear()
		return Undefined(), true
	case "forEach":
		fn, ok := js.callable(site, "forEach", arg(0))
		if !ok {
			return Undefined(), true
		}
		// The length is read at every step: entries added by the
		// callback are visited, deleted ones are skipped.
		for i := 0; i < len(c.live); i++ {
			if !c.live[i] {
				continue
			}
			key, value := c.keys[i], c.vals[i]
			if c.set {
				value = key
			}
			fn(Undefined(), value, key, c)
			if js.Failed() {
				return Undefined(), true
			}
		}
		return Undefined(), true
	}
	return nil, false
}

// clone is structuredClone of a collection: keys and values cloned.
func (c *Collection) clone(js *JS, clone func(any) (any, bool)) (*Collection, bool) {
	out := newCollection(c.set, true)
	for i, live := range c.live {
		if !live {
			continue
		}
		key, ok := clone(c.keys[i])
		if !ok {
			return nil, false
		}
		value := key
		if !c.set {
			if value, ok = clone(c.vals[i]); !ok {
				return nil, false
			}
		}
		out.put(js, "", key, value)
	}
	return out, true
}

// MarshalJSON writes {}: a Map or a Set has no own enumerable properties.
func (c *Collection) MarshalJSON() ([]byte, error) { return []byte("{}"), nil }
