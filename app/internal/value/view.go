package value

import (
	"cmp"
	"encoding"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
	"unsafe"
)

// The JSON view is built by walking the value with reflection and applying
// the rules of encoding/json, instead of encoding the value and decoding the
// text again: the result is the same (TestViewMatchesRoundTrip), without the
// text in between. Only the values that encode themselves (json.Marshaler)
// still go through JSON, because their encoding is all we know of them.

var (
	marshalerType     = reflect.TypeFor[json.Marshaler]()
	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
	isZeroerType      = reflect.TypeFor[interface{ IsZero() bool }]()
	numberType        = reflect.TypeFor[json.Number]()
)

// viewState detects cycles: the pointers, maps and slices on the path from
// the root to the value being converted.
type viewState struct {
	path map[cycleKey]struct{}
}

type cycleKey struct {
	ptr unsafe.Pointer
	len int
}

func view(v any) (any, error) {
	var s viewState
	return s.value(reflect.ValueOf(v))
}

func unsupported(t reflect.Type) error {
	return &json.UnsupportedTypeError{Type: t}
}

func (s *viewState) value(v reflect.Value) (any, error) {
	if !v.IsValid() {
		return nil, nil
	}
	return converterFor(v.Type())(s, v)
}

// converter builds the view of a value of one type.
type converter func(s *viewState, v reflect.Value) (any, error)

var converters sync.Map // reflect.Type → converter

// converterFor returns the converter of t, built once per type, as
// encoding/json caches its encoders.
func converterFor(t reflect.Type) converter {
	if c, ok := converters.Load(t); ok {
		return c.(converter)
	}
	// A recursive type reaches itself while its converter is built: the
	// placeholder waits for the real one.
	var (
		wg   sync.WaitGroup
		real converter
	)
	wg.Add(1)
	c, loaded := converters.LoadOrStore(t, converter(func(s *viewState, v reflect.Value) (any, error) {
		wg.Wait()
		return real(s, v)
	}))
	if loaded {
		return c.(converter)
	}
	real = newConverter(t, true)
	wg.Done()
	converters.Store(t, real)
	return real
}

func newConverter(t reflect.Type, allowAddr bool) converter {
	// Methods first, as encoding/json does: a pointer method only when the
	// value is addressable.
	if t.Kind() != reflect.Pointer && allowAddr && reflect.PointerTo(t).Implements(marshalerType) {
		return condAddr(func(s *viewState, v reflect.Value) (any, error) { return s.marshaler(v.Addr()) }, newConverter(t, false))
	}
	if t.Kind() != reflect.Interface && t.Implements(marshalerType) {
		return func(s *viewState, v reflect.Value) (any, error) { return s.marshaler(v) }
	}
	if t.Kind() != reflect.Pointer && allowAddr && reflect.PointerTo(t).Implements(textMarshalerType) {
		return condAddr(func(s *viewState, v reflect.Value) (any, error) { return textView(v.Addr()) }, newConverter(t, false))
	}
	if t.Kind() != reflect.Interface && t.Implements(textMarshalerType) {
		return func(s *viewState, v reflect.Value) (any, error) { return textView(v) }
	}
	switch t.Kind() {
	case reflect.Bool:
		return func(s *viewState, v reflect.Value) (any, error) { return v.Bool(), nil }
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return func(s *viewState, v reflect.Value) (any, error) { return float64(v.Int()), nil }
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return func(s *viewState, v reflect.Value) (any, error) { return float64(v.Uint()), nil }
	case reflect.Float32, reflect.Float64:
		return func(s *viewState, v reflect.Value) (any, error) { return floatView(v) }
	case reflect.String:
		if t == numberType {
			return func(s *viewState, v reflect.Value) (any, error) { return numberView(v.String()) }
		}
		return func(s *viewState, v reflect.Value) (any, error) { return validString(v.String()), nil }
	case reflect.Interface:
		return func(s *viewState, v reflect.Value) (any, error) {
			if v.IsNil() {
				return nil, nil
			}
			return s.value(v.Elem())
		}
	case reflect.Pointer:
		elem := t.Elem()
		return func(s *viewState, v reflect.Value) (any, error) {
			if v.IsNil() {
				return nil, nil
			}
			key := cycleKey{ptr: v.UnsafePointer()}
			if err := s.enter(key, v); err != nil {
				return nil, err
			}
			defer s.leave(key)
			return converterFor(elem)(s, v.Elem())
		}
	case reflect.Struct:
		return structConverter(t)
	case reflect.Map:
		return mapConverter(t)
	case reflect.Slice:
		if isBytes(t) {
			return func(s *viewState, v reflect.Value) (any, error) {
				if v.IsNil() {
					return nil, nil
				}
				return base64.StdEncoding.EncodeToString(v.Bytes()), nil
			}
		}
		elem := converterFor(t.Elem())
		return func(s *viewState, v reflect.Value) (any, error) {
			if v.IsNil() {
				return nil, nil
			}
			key := cycleKey{ptr: v.UnsafePointer(), len: v.Len()}
			if err := s.enter(key, v); err != nil {
				return nil, err
			}
			defer s.leave(key)
			return s.elements(v, elem)
		}
	case reflect.Array:
		elem := converterFor(t.Elem())
		return func(s *viewState, v reflect.Value) (any, error) { return s.elements(v, elem) }
	}
	return func(s *viewState, v reflect.Value) (any, error) { return nil, unsupported(t) }
}

func condAddr(addr, plain converter) converter {
	return func(s *viewState, v reflect.Value) (any, error) {
		if v.CanAddr() {
			return addr(s, v)
		}
		return plain(s, v)
	}
}

func (s *viewState) enter(key cycleKey, v reflect.Value) error {
	if s.path == nil {
		s.path = map[cycleKey]struct{}{}
	}
	if _, ok := s.path[key]; ok {
		return &json.UnsupportedValueError{Value: v, Str: fmt.Sprintf("encountered a cycle via %s", v.Type())}
	}
	s.path[key] = struct{}{}
	return nil
}

func (s *viewState) leave(key cycleKey) {
	delete(s.path, key)
}

// isBytes reports whether encoding/json writes a slice of t as base64.
func isBytes(t reflect.Type) bool {
	e := t.Elem()
	if e.Kind() != reflect.Uint8 {
		return false
	}
	p := reflect.PointerTo(e)
	return !p.Implements(marshalerType) && !p.Implements(textMarshalerType)
}

func (s *viewState) elements(v reflect.Value, elem converter) (any, error) {
	n := v.Len()
	out := make([]any, n)
	for i := range n {
		e, err := elem(s, v.Index(i))
		if err != nil {
			return nil, err
		}
		out[i] = e
	}
	return out, nil
}

func mapConverter(t reflect.Type) converter {
	k := t.Key()
	switch k.Kind() {
	case reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
	default:
		if !k.Implements(textMarshalerType) {
			return func(s *viewState, v reflect.Value) (any, error) { return nil, unsupported(t) }
		}
	}
	elem := converterFor(t.Elem())
	return func(s *viewState, v reflect.Value) (any, error) {
		if v.IsNil() {
			return nil, nil
		}
		key := cycleKey{ptr: v.UnsafePointer()}
		if err := s.enter(key, v); err != nil {
			return nil, err
		}
		defer s.leave(key)
		out := make(map[string]any, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			name, err := keyName(iter.Key())
			if err != nil {
				return nil, err
			}
			e, err := elem(s, iter.Value())
			if err != nil {
				return nil, err
			}
			out[name] = e
		}
		return out, nil
	}
}

func keyName(k reflect.Value) (string, error) {
	if k.Kind() == reflect.String {
		return validString(k.String()), nil
	}
	if k.Type().Implements(textMarshalerType) {
		if k.Kind() == reflect.Pointer && k.IsNil() {
			return "", nil
		}
		text, err := k.Interface().(encoding.TextMarshaler).MarshalText()
		if err != nil {
			return "", &json.MarshalerError{Type: k.Type(), Err: err}
		}
		return validString(string(text)), nil
	}
	switch k.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(k.Int(), 10), nil
	default:
		return strconv.FormatUint(k.Uint(), 10), nil
	}
}

func structConverter(t reflect.Type) converter {
	fields := typeFields(t)
	for i := range fields {
		f := &fields[i]
		if !f.quoted {
			f.convert = converterFor(typeByIndex(t, f.index))
		}
	}
	return func(s *viewState, v reflect.Value) (any, error) {
		out := make(map[string]any, len(fields))
	next:
		for i := range fields {
			f := &fields[i]
			fv := v
			for _, i := range f.index {
				if fv.Kind() == reflect.Pointer {
					if fv.IsNil() {
						continue next
					}
					fv = fv.Elem()
				}
				fv = fv.Field(i)
			}
			if f.omitEmpty && isEmptyValue(fv) {
				continue
			}
			if f.omitZero && (f.isZero == nil && fv.IsZero() || f.isZero != nil && f.isZero(fv)) {
				continue
			}
			var (
				e   any
				err error
			)
			if f.quoted {
				e, err = quotedView(fv)
			} else {
				e, err = f.convert(s, fv)
			}
			if err != nil {
				return nil, err
			}
			out[f.name] = e
		}
		return out, nil
	}
}

func typeByIndex(t reflect.Type, index []int) reflect.Type {
	for _, i := range index {
		if t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		t = t.Field(i).Type
	}
	return t
}

// marshaler converts a value that encodes itself: its JSON decoded again.
func (s *viewState) marshaler(v reflect.Value) (any, error) {
	if v.Kind() == reflect.Pointer && v.IsNil() {
		return nil, nil
	}
	m, ok := v.Interface().(json.Marshaler)
	if !ok {
		return nil, nil
	}
	data, err := m.MarshalJSON()
	if err != nil {
		return nil, &json.MarshalerError{Type: v.Type(), Err: err}
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, &json.MarshalerError{Type: v.Type(), Err: err}
	}
	return out, nil
}

func textView(v reflect.Value) (any, error) {
	if v.Kind() == reflect.Pointer && v.IsNil() {
		return nil, nil
	}
	text, err := v.Interface().(encoding.TextMarshaler).MarshalText()
	if err != nil {
		return nil, &json.MarshalerError{Type: v.Type(), Err: err}
	}
	return validString(string(text)), nil
}

func floatView(v reflect.Value) (any, error) {
	f := v.Float()
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return nil, &json.UnsupportedValueError{Value: v, Str: strconv.FormatFloat(f, 'g', -1, v.Type().Bits())}
	}
	if v.Kind() == reflect.Float32 {
		// encoding/json writes the shortest decimal of the float32, which
		// the client reads as a float64.
		f, _ = strconv.ParseFloat(strconv.FormatFloat(f, 'g', -1, 32), 64)
	}
	return f, nil
}

func numberView(s string) (any, error) {
	if s == "" {
		s = "0"
	}
	if !isValidNumber(s) {
		return nil, fmt.Errorf("json: invalid number literal %q", s)
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil, fmt.Errorf("json: cannot read number %s: %w", s, err)
	}
	return f, nil
}

// quotedView is a field with the ",string" option: its JSON inside a
// string.
func quotedView(v reflect.Value) (any, error) {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil, nil
		}
		v = v.Elem()
	}
	t := v.Type()
	if t.Implements(marshalerType) || t.Implements(textMarshalerType) ||
		v.CanAddr() && (reflect.PointerTo(t).Implements(marshalerType) || reflect.PointerTo(t).Implements(textMarshalerType)) {
		var s viewState
		return s.value(v)
	}
	switch v.Kind() {
	case reflect.Bool:
		return strconv.FormatBool(v.Bool()), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.FormatUint(v.Uint(), 10), nil
	case reflect.Float32, reflect.Float64:
		if _, err := floatView(v); err != nil {
			return nil, err
		}
		return formatFloat(v.Float(), v.Type().Bits()), nil
	case reflect.String:
		if t == numberType {
			n := v.String()
			if n == "" {
				n = "0"
			}
			if !isValidNumber(n) {
				return nil, fmt.Errorf("json: invalid number literal %q", n)
			}
			return n, nil
		}
		data, err := Marshal(v.String())
		if err != nil {
			return nil, err
		}
		return string(data), nil
	}
	var s viewState
	return s.value(v)
}

// formatFloat formats f as encoding/json does.
func formatFloat(f float64, bits int) string {
	abs := math.Abs(f)
	format := byte('f')
	if abs != 0 && (bits == 64 && (abs < 1e-6 || abs >= 1e21) || bits == 32 && (float32(abs) < 1e-6 || float32(abs) >= 1e21)) {
		format = 'e'
	}
	b := strconv.AppendFloat(nil, f, format, -1, bits)
	if format == 'e' {
		// Clean up e-09 to e-9.
		if n := len(b); n >= 4 && b[n-4] == 'e' && b[n-3] == '-' && b[n-2] == '0' {
			b[n-2] = b[n-1]
			b = b[:n-1]
		}
	}
	return string(b)
}

// validString replaces each byte of invalid UTF-8 with U+FFFD, as the JSON
// encoding does.
func validString(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			b.WriteRune(utf8.RuneError)
		} else {
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return b.String()
}

func isEmptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64,
		reflect.Interface, reflect.Pointer:
		return v.IsZero()
	}
	return false
}

// isValidNumber reports whether s is a JSON number literal.
func isValidNumber(s string) bool {
	if s == "" {
		return false
	}
	if s[0] == '-' {
		s = s[1:]
		if s == "" {
			return false
		}
	}
	switch {
	case s[0] == '0':
		s = s[1:]
	case '1' <= s[0] && s[0] <= '9':
		s = s[1:]
		for len(s) > 0 && '0' <= s[0] && s[0] <= '9' {
			s = s[1:]
		}
	default:
		return false
	}
	if len(s) >= 2 && s[0] == '.' && '0' <= s[1] && s[1] <= '9' {
		s = s[2:]
		for len(s) > 0 && '0' <= s[0] && s[0] <= '9' {
			s = s[1:]
		}
	}
	if len(s) >= 2 && (s[0] == 'e' || s[0] == 'E') {
		s = s[1:]
		if s[0] == '+' || s[0] == '-' {
			s = s[1:]
			if s == "" {
				return false
			}
		}
		for len(s) > 0 && '0' <= s[0] && s[0] <= '9' {
			s = s[1:]
		}
	}
	return s == ""
}

// field is a struct field as encoding/json encodes it.
type field struct {
	name      string
	tag       bool
	index     []int
	typ       reflect.Type
	omitEmpty bool
	omitZero  bool
	isZero    func(reflect.Value) bool
	quoted    bool
	convert   converter
}

// typeFields lists the fields encoding/json encodes for the struct type t,
// in its order, with its rules for embedded structs (encoding/json's
// typeFields).
func typeFields(t reflect.Type) []field {
	current := []field{}
	next := []field{{typ: t}}
	var count, nextCount map[reflect.Type]int
	visited := map[reflect.Type]bool{}
	var fields []field

	for len(next) > 0 {
		current, next = next, current[:0]
		count, nextCount = nextCount, map[reflect.Type]int{}

		for _, f := range current {
			if visited[f.typ] {
				continue
			}
			visited[f.typ] = true
			for i := 0; i < f.typ.NumField(); i++ {
				sf := f.typ.Field(i)
				if sf.Anonymous {
					t := sf.Type
					if t.Kind() == reflect.Pointer {
						t = t.Elem()
					}
					if !sf.IsExported() && t.Kind() != reflect.Struct {
						continue
					}
				} else if !sf.IsExported() {
					continue
				}
				tag := sf.Tag.Get("json")
				if tag == "-" {
					continue
				}
				name, opts, _ := strings.Cut(tag, ",")
				if !isValidTag(name) {
					name = ""
				}
				index := make([]int, len(f.index)+1)
				copy(index, f.index)
				index[len(f.index)] = i

				ft := sf.Type
				if ft.Name() == "" && ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				quoted := false
				if hasOption(opts, "string") {
					switch ft.Kind() {
					case reflect.Bool,
						reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
						reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
						reflect.Float32, reflect.Float64,
						reflect.String:
						quoted = true
					}
				}

				if name != "" || !sf.Anonymous || ft.Kind() != reflect.Struct {
					tagged := name != ""
					if name == "" {
						name = sf.Name
					}
					fl := field{
						name:      name,
						tag:       tagged,
						index:     index,
						typ:       ft,
						omitEmpty: hasOption(opts, "omitempty"),
						omitZero:  hasOption(opts, "omitzero"),
						quoted:    quoted,
					}
					if fl.omitZero {
						fl.isZero = isZeroFunc(sf.Type)
					}
					fields = append(fields, fl)
					if count[f.typ] > 1 {
						// A second copy, so that the annihilation below sees a
						// duplicate.
						fields = append(fields, fields[len(fields)-1])
					}
					continue
				}
				nextCount[ft]++
				if nextCount[ft] == 1 {
					next = append(next, field{name: ft.Name(), index: index, typ: ft})
				}
			}
		}
	}

	slices.SortFunc(fields, func(a, b field) int {
		if c := strings.Compare(a.name, b.name); c != 0 {
			return c
		}
		if c := cmp.Compare(len(a.index), len(b.index)); c != 0 {
			return c
		}
		if a.tag != b.tag {
			if a.tag {
				return -1
			}
			return +1
		}
		return slices.Compare(a.index, b.index)
	})

	// Drop the fields hidden by Go's rules for embedded fields, except
	// that tagged fields are promoted.
	out := fields[:0]
	for advance, i := 0, 0; i < len(fields); i += advance {
		fi := fields[i]
		for advance = 1; i+advance < len(fields); advance++ {
			if fields[i+advance].name != fi.name {
				break
			}
		}
		if advance == 1 {
			out = append(out, fi)
			continue
		}
		dominant := fields[i : i+advance]
		if len(dominant[0].index) == len(dominant[1].index) && dominant[0].tag == dominant[1].tag {
			continue
		}
		out = append(out, dominant[0])
	}
	slices.SortFunc(out, func(a, b field) int { return slices.Compare(a.index, b.index) })
	return out
}

func isZeroFunc(t reflect.Type) func(reflect.Value) bool {
	switch {
	case t.Kind() == reflect.Interface && t.Implements(isZeroerType):
		return func(v reflect.Value) bool {
			return v.IsNil() ||
				(v.Elem().Kind() == reflect.Pointer && v.Elem().IsNil()) ||
				v.Interface().(interface{ IsZero() bool }).IsZero()
		}
	case t.Kind() == reflect.Pointer && t.Implements(isZeroerType):
		return func(v reflect.Value) bool {
			return v.IsNil() || v.Interface().(interface{ IsZero() bool }).IsZero()
		}
	case t.Implements(isZeroerType):
		return func(v reflect.Value) bool {
			return v.Interface().(interface{ IsZero() bool }).IsZero()
		}
	case reflect.PointerTo(t).Implements(isZeroerType):
		return func(v reflect.Value) bool {
			if !v.CanAddr() {
				boxed := reflect.New(v.Type()).Elem()
				boxed.Set(v)
				v = boxed
			}
			return v.Addr().Interface().(interface{ IsZero() bool }).IsZero()
		}
	}
	return nil
}

func hasOption(opts, name string) bool {
	for opts != "" {
		var o string
		o, opts, _ = strings.Cut(opts, ",")
		if o == name {
			return true
		}
	}
	return false
}

func isValidTag(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch {
		case strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", c):
		case !unicode.IsLetter(c) && !unicode.IsDigit(c):
			return false
		}
	}
	return true
}

var errNotJSON = errors.New("value: the result is not JSON")
