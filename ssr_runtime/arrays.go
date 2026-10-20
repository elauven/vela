package ssr_runtime

import (
	"math"
	"sort"
	"strings"
)

// Hole is an array hole ([1, , 2]): it reads as undefined, and array
// methods skip it where JavaScript does.
func Hole() UndefinedValue { return UndefinedValue{hole: true} }

func isHole(value any) bool {
	u, ok := value.(UndefinedValue)
	return ok && u.hole
}

// unhole turns a hole read from an array into undefined.
func unhole(value any) any {
	if isHole(value) {
		return Undefined()
	}
	return value
}

// unholed returns items with holes read as undefined, copying only when
// there is a hole.
func unholed(items []any) []any {
	for i, item := range items {
		if isHole(item) {
			out := append([]any(nil), items...)
			for j := i; j < len(out); j++ {
				out[j] = unhole(out[j])
			}
			return out
		}
	}
	return items
}

// MarshalJSON writes undefined (and holes) as null, the JSON view of an
// undefined array element.
func (UndefinedValue) MarshalJSON() ([]byte, error) { return []byte("null"), nil }

// callback calls fn with (item, index, array); ok is false when the call
// threw.
func (js *JS) callback(fn Func, item any, index int, array []any) (any, bool) {
	result := fn(Undefined(), unhole(item), float64(index), array)
	return result, !js.Failed()
}

func (js *JS) callable(site, method string, value any) (Func, bool) {
	fn, ok := value.(Func)
	if !ok {
		js.throw("TypeError", describe(jsValue(value))+" is not a function", site)
	}
	return fn, ok
}

// sameValueZero is the equality of includes: NaN equals NaN.
func sameValueZero(left, right any) bool {
	l, lok := jsValue(left).(float64)
	r, rok := jsValue(right).(float64)
	if lok && rok && l != l && r != r {
		return true
	}
	return StrictEqual(left, right)
}

// arrayMethod calls method name of array a (docs/SSR_TRANSLATION_MATRIX.md
// §2.3). Methods that change the array return its new contents in out;
// the compiler admits them only on arrays the function created (T-6.6b).
func (js *JS) arrayMethod(site string, a []any, name string, args []any) (result any, out []any, ok bool) {
	arg := func(i int) any { return Arg(args, i) }
	n := len(a)
	switch name {
	case "at":
		k := toIntegerOrInfinity(arg(0))
		if k < 0 {
			k += float64(n)
		}
		if k < 0 || k >= float64(n) {
			return Undefined(), nil, true
		}
		return unhole(a[int(k)]), nil, true
	case "includes":
		for i := clampFrom(arg(1), n); i < n; i++ {
			if sameValueZero(unhole(a[i]), arg(0)) {
				return true, nil, true
			}
		}
		return false, nil, true
	case "indexOf":
		for i := clampFrom(arg(1), n); i < n; i++ {
			if !isHole(a[i]) && StrictEqual(a[i], arg(0)) {
				return float64(i), nil, true
			}
		}
		return -1.0, nil, true
	case "lastIndexOf":
		from := n - 1
		if len(args) > 1 {
			k := toIntegerOrInfinity(arg(1))
			if k < 0 {
				k += float64(n)
			}
			from = int(math.Min(k, float64(n-1)))
		}
		for i := from; i >= 0; i-- {
			if !isHole(a[i]) && StrictEqual(a[i], arg(0)) {
				return float64(i), nil, true
			}
		}
		return -1.0, nil, true
	case "join":
		sep := ","
		if !undefinedArg(args, 0) {
			sep = String(jsValue(arg(0)))
		}
		parts := make([]string, n)
		for i, item := range a {
			parts[i] = ArrayJoinString(jsValue(item))
		}
		return strings.Join(parts, sep), nil, true
	case "toString":
		return String(a), nil, true
	case "slice":
		start := relative(arg(0), n)
		end := n
		if !undefinedArg(args, 1) {
			end = relative(arg(1), n)
		}
		if start >= end {
			return []any{}, nil, true
		}
		return append([]any{}, a[start:end]...), nil, true
	case "concat":
		result := append([]any{}, a...)
		for _, value := range args {
			if items, isArray := jsValue(value).([]any); isArray {
				result = append(result, items...)
			} else {
				result = append(result, value)
			}
		}
		return result, nil, true
	case "map":
		fn, ok := js.callable(site, name, arg(0))
		if !ok {
			return Undefined(), nil, true
		}
		result := make([]any, n)
		for i, item := range a {
			if isHole(item) {
				result[i] = Hole()
				continue
			}
			value, ok := js.callback(fn, item, i, a)
			if !ok {
				return Undefined(), nil, true
			}
			result[i] = value
		}
		return result, nil, true
	case "filter", "forEach", "some", "every":
		fn, ok := js.callable(site, name, arg(0))
		if !ok {
			return Undefined(), nil, true
		}
		result := []any{}
		for i, item := range a {
			if isHole(item) {
				continue
			}
			value, ok := js.callback(fn, item, i, a)
			if !ok {
				return Undefined(), nil, true
			}
			switch truthy := Truthy(jsValue(value)); {
			case name == "filter" && truthy:
				result = append(result, item)
			case name == "some" && truthy:
				return true, nil, true
			case name == "every" && !truthy:
				return false, nil, true
			}
		}
		switch name {
		case "filter":
			return result, nil, true
		case "some":
			return false, nil, true
		case "every":
			return true, nil, true
		}
		return Undefined(), nil, true
	case "find", "findIndex", "findLast", "findLastIndex":
		fn, ok := js.callable(site, name, arg(0))
		if !ok {
			return Undefined(), nil, true
		}
		last := strings.HasPrefix(name, "findLast")
		for k := 0; k < n; k++ {
			i := k
			if last {
				i = n - 1 - k
			}
			value, ok := js.callback(fn, a[i], i, a)
			if !ok {
				return Undefined(), nil, true
			}
			if Truthy(jsValue(value)) {
				if strings.HasSuffix(name, "Index") {
					return float64(i), nil, true
				}
				return unhole(a[i]), nil, true
			}
		}
		if strings.HasSuffix(name, "Index") {
			return -1.0, nil, true
		}
		return Undefined(), nil, true
	case "reduce", "reduceRight":
		fn, ok := js.callable(site, name, arg(0))
		if !ok {
			return Undefined(), nil, true
		}
		order := make([]int, 0, n)
		for k := 0; k < n; k++ {
			i := k
			if name == "reduceRight" {
				i = n - 1 - k
			}
			if !isHole(a[i]) {
				order = append(order, i)
			}
		}
		var accumulator any
		if len(args) > 1 {
			accumulator = args[1]
		} else {
			if len(order) == 0 {
				return js.throw("TypeError", "Reduce of empty array with no initial value", site), nil, true
			}
			accumulator, order = a[order[0]], order[1:]
		}
		for _, i := range order {
			accumulator = fn(Undefined(), accumulator, a[i], float64(i), a)
			if js.Failed() {
				return Undefined(), nil, true
			}
		}
		return accumulator, nil, true
	case "flat":
		depth := 1.0
		if !undefinedArg(args, 0) {
			depth = toIntegerOrInfinity(arg(0))
		}
		return flatten(a, depth), nil, true
	case "flatMap":
		mapped, _, _ := js.arrayMethod(site, a, "map", args)
		if js.Failed() {
			return Undefined(), nil, true
		}
		return flatten(mapped.([]any), 1), nil, true
	case "toSorted", "toReversed":
		// The copying methods read holes as undefined.
		copied := unholed(append([]any{}, a...))
		if name == "toReversed" {
			result, _, _ := js.arrayMethod(site, copied, "reverse", args)
			return result, nil, true
		}
		result, _, _ := js.arrayMethod(site, copied, "sort", args)
		return result, nil, true
	case "with":
		// A copy with one item replaced; holes read as undefined.
		k := toIntegerOrInfinity(arg(0))
		if k < 0 {
			k += float64(n)
		}
		if k < 0 || k >= float64(n) {
			return js.throw("RangeError", "Invalid index : "+String(jsValue(arg(0))), site), nil, true
		}
		copied := unholed(append([]any{}, a...))
		copied[int(k)] = arg(1)
		return copied, nil, true
	case "toSpliced":
		// splice on a copy, returning the copy; holes read as undefined.
		start := relative(arg(0), n)
		count := n - start
		if len(args) == 0 {
			count = 0
		} else if len(args) > 1 {
			count = int(math.Max(0, math.Min(toIntegerOrInfinity(args[1]), float64(n-start))))
		}
		var inserted []any
		if len(args) > 2 {
			inserted = args[2:]
		}
		copied := append(append(append([]any{}, a[:start]...), inserted...), a[start+count:]...)
		return unholed(copied), nil, true
	case "sort":
		return js.sortArray(site, a, arg(0)), nil, true
	case "reverse":
		for i, j := 0, n-1; i < j; i, j = i+1, j-1 {
			a[i], a[j] = a[j], a[i]
		}
		return a, nil, true
	case "push":
		out = append(a, args...)
		return float64(len(out)), out, true
	case "unshift":
		out = append(append([]any{}, args...), a...)
		return float64(len(out)), out, true
	case "pop":
		if n == 0 {
			return Undefined(), a, true
		}
		return unhole(a[n-1]), a[:n-1], true
	case "shift":
		if n == 0 {
			return Undefined(), a, true
		}
		return unhole(a[0]), append([]any{}, a[1:]...), true
	case "splice":
		start := relative(arg(0), n)
		count := n - start
		if len(args) == 0 {
			count = 0
		} else if len(args) > 1 {
			count = int(math.Max(0, math.Min(toIntegerOrInfinity(args[1]), float64(n-start))))
		}
		removed := append([]any{}, a[start:start+count]...)
		var inserted []any
		if len(args) > 2 {
			inserted = args[2:]
		}
		out = append(append(append([]any{}, a[:start]...), inserted...), a[start+count:]...)
		return removed, out, true
	case "fill":
		start := relative(arg(1), n)
		end := n
		if !undefinedArg(args, 2) {
			end = relative(arg(2), n)
		}
		for i := start; i < end; i++ {
			a[i] = arg(0)
		}
		return a, a, true
	}
	return nil, nil, false
}

// clampFrom resolves the fromIndex argument of includes and indexOf.
func clampFrom(value any, n int) int {
	k := toIntegerOrInfinity(value)
	if k < 0 {
		k += float64(n)
		if k < 0 {
			return 0
		}
	}
	if k > float64(n) {
		return n
	}
	return int(k)
}

func flatten(a []any, depth float64) []any {
	out := []any{}
	for _, item := range a {
		if isHole(item) {
			continue
		}
		if items, ok := jsValue(item).([]any); ok && depth >= 1 {
			out = append(out, flatten(items, depth-1)...)
			continue
		}
		out = append(out, item)
	}
	return out
}

// sortArray sorts in place, stably: undefined values after the others and
// holes last; without a comparator by String(x) in UTF-16 order; a
// comparator result NaN counts as 0.
func (js *JS) sortArray(site string, a []any, comparator any) []any {
	fn, hasComparator := comparator.(Func)
	if !hasComparator {
		if _, undefined := comparator.(UndefinedValue); !undefined && comparator != nil {
			return a
		}
	}
	var values []any
	undefineds, holes := 0, 0
	for _, item := range a {
		switch {
		case isHole(item):
			holes++
		case isUndefinedValue(item):
			undefineds++
		default:
			values = append(values, item)
		}
	}
	less := func(x, y any) bool {
		if hasComparator {
			if js.Failed() {
				return false
			}
			n := ToNumber(jsValue(fn(Undefined(), x, y)))
			return n < 0
		}
		return StringLess(String(jsValue(x)), String(jsValue(y)))
	}
	sort.SliceStable(values, func(i, j int) bool { return less(values[i], values[j]) })
	out := a[:0]
	out = append(out, values...)
	for ; undefineds > 0; undefineds-- {
		out = append(out, Undefined())
	}
	for ; holes > 0; holes-- {
		out = append(out, Hole())
	}
	return out
}

func isUndefinedValue(value any) bool {
	_, ok := value.(UndefinedValue)
	return ok
}
