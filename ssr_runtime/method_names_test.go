package ssr_runtime

import "testing"

// TestMethodNamesMatchDispatch keeps the name sets of HasMethod equal to
// the names Method dispatches for each receiver kind.
func TestMethodNamesMatchDispatch(t *testing.T) {
	names := map[string]bool{"keys": true, "values": true, "entries": true, "with": true, "toSpliced": true, "length": true, "missing": true}
	for _, set := range []map[string]bool{stringMethodNames, arrayMethodNames, numberMethodNames} {
		for name := range set {
			names[name] = true
		}
	}
	for name := range names {
		js := NewJS()
		if _, ok := js.stringMethod("", "abc", name, nil); ok != stringMethodNames[name] {
			t.Errorf("string %s: dispatched %v, HasMethod %v", name, ok, stringMethodNames[name])
		}
		if _, _, ok := js.arrayMethod("", []any{3.0, 1.0}, name, nil); ok != arrayMethodNames[name] {
			t.Errorf("array %s: dispatched %v, HasMethod %v", name, ok, arrayMethodNames[name])
		}
		if _, ok := js.numberMethod("", 1.5, name, nil); ok != numberMethodNames[name] {
			t.Errorf("number %s: dispatched %v, HasMethod %v", name, ok, numberMethodNames[name])
		}
	}
}
