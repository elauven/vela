package ssr_runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf16"
)

// Dynamic tier of the translation contract (docs/SSR_TRANSLATION_CONTRACT.md
// §5.4, §6): JavaScript semantics over the JSON value domain. A value is
// nil (null), UndefinedValue, bool, a number (float64; Go integers are
// widened), string, []any or another slice (Array), *Object or a map with
// string keys (Object), or a Go struct read through its JSON view (T-5.1).
// Every helper is total: it returns a value, or records a JavaScript
// exception in a *JS and returns undefined. None panics (T-5.4c).

// ── Exceptions ───────────────────────────────────────────────────────────

// RenderError is a JavaScript exception raised while rendering (T-6.5a):
// the error kind (TypeError, RangeError, Error), the message, and the
// source position of the site.
type RenderError struct {
	Kind    string
	Message string
	Site    string
}

func (e *RenderError) Error() string {
	text := e.Kind + ": " + e.Message
	if e.Site != "" {
		return e.Site + ": " + text
	}
	return text
}

// JS is the evaluation state of one render function. Operations that throw
// in JavaScript record the first exception here and return undefined, so
// evaluation stays panic-free; the renderer returns Err before it writes
// output that depends on a failed site.
type JS struct {
	err error
}

// NewJS starts the evaluation state of a render function.
func NewJS() *JS { return &JS{} }

// Err returns the first exception, or nil.
func (js *JS) Err() error {
	if js == nil {
		return nil
	}
	return js.err
}

// fail records err as the first failure of the render and returns
// undefined.
func (js *JS) fail(err error) UndefinedValue {
	if js != nil && js.err == nil {
		js.err = err
	}
	return Undefined()
}

func (js *JS) throw(kind, message, site string) UndefinedValue {
	return js.fail(&RenderError{Kind: kind, Message: message, Site: site})
}

// ── Object ───────────────────────────────────────────────────────────────

// Object is a JavaScript object created by translated code. Keys keep
// insertion order, and enumeration follows JavaScript: integer-like keys
// in ascending numeric order first, then the other keys in insertion order
// (T-5.1c).
type Object struct {
	keys   []string
	values map[string]any
}

// NewObject returns an empty object.
func NewObject() *Object { return &Object{values: map[string]any{}} }

// Set defines or replaces a property and returns the object, so a literal
// lowers to one expression.
func (o *Object) Set(key string, value any) *Object {
	if _, exists := o.values[key]; !exists {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
	return o
}

// Spread copies the own enumerable properties of value, as {...value}
// does: nothing for null and undefined, indices for strings and arrays,
// and the JSON view of Go values.
func (o *Object) Spread(value any) *Object {
	switch source := jsValue(value).(type) {
	case nil, UndefinedValue, bool, float64:
	case string:
		for index, unit := range utf16.Encode([]rune(source)) {
			o.Set(strconv.Itoa(index), string(utf16.Decode([]uint16{unit})))
		}
	case []any:
		for index, item := range source {
			o.Set(strconv.Itoa(index), item)
		}
	case *Object:
		for _, key := range source.Keys() {
			o.Set(key, source.values[key])
		}
	}
	return o
}

// Lookup returns a property and whether it exists.
func (o *Object) Lookup(key string) (any, bool) {
	value, ok := o.values[key]
	return value, ok
}

// Keys returns the property names in JavaScript enumeration order.
func (o *Object) Keys() []string {
	var integers, others []string
	for _, key := range o.keys {
		if arrayIndexKey(key) {
			integers = append(integers, key)
		} else {
			others = append(others, key)
		}
	}
	sort.SliceStable(integers, func(i, j int) bool {
		a, _ := strconv.ParseUint(integers[i], 10, 32)
		b, _ := strconv.ParseUint(integers[j], 10, 32)
		return a < b
	})
	return append(integers, others...)
}

// MarshalJSON writes the properties in enumeration order.
func (o *Object) MarshalJSON() ([]byte, error) {
	var out bytes.Buffer
	out.WriteByte('{')
	for i, key := range o.Keys() {
		if i > 0 {
			out.WriteByte(',')
		}
		name, _ := json.Marshal(key)
		out.Write(name)
		out.WriteByte(':')
		value, err := json.Marshal(o.values[key])
		if err != nil {
			return nil, err
		}
		out.Write(value)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

// arrayIndexKey reports a canonical array index ("0", "17", not "01").
func arrayIndexKey(key string) bool {
	if key == "" || len(key) > 10 || (len(key) > 1 && key[0] == '0') {
		return false
	}
	for _, c := range key {
		if c < '0' || c > '9' {
			return false
		}
	}
	n, err := strconv.ParseUint(key, 10, 64)
	return err == nil && n < math.MaxUint32
}

// ── JSON view ────────────────────────────────────────────────────────────

// jsValue maps a Go value to its JavaScript-visible kind without copying
// arrays and objects that are already in the value domain. A Go number
// becomes float64; a struct, a map with non-any values, a Marshaler, a
// time.Time, or a []byte is read through JSON (T-5.1).
func jsValue(value any) any {
	switch v := value.(type) {
	case Func, *Promise:
		return v
	case nil, UndefinedValue, bool, string, float64, []any, *Object, map[string]any:
		if m, ok := v.(map[string]any); ok {
			return objectFromMap(m)
		}
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case float32:
		return float64(v)
	case json.Marshaler:
		return jsonView(v)
	}
	reflected := reflect.ValueOf(value)
	for reflected.Kind() == reflect.Pointer || reflected.Kind() == reflect.Interface {
		if reflected.IsNil() {
			return nil
		}
		reflected = reflected.Elem()
	}
	if number, ok := strictEqualityNumber(reflected); ok {
		return number
	}
	switch reflected.Kind() {
	case reflect.Bool:
		return reflected.Bool()
	case reflect.String:
		return reflected.String()
	case reflect.Slice, reflect.Array:
		if reflected.Kind() == reflect.Slice && reflected.IsNil() {
			return nil
		}
		if reflected.Type().Elem().Kind() == reflect.Uint8 {
			return jsonView(value)
		}
		items := make([]any, reflected.Len())
		for i := range items {
			items[i] = reflected.Index(i).Interface()
		}
		return items
	}
	return jsonView(value)
}

// objectFromMap orders a Go map like JSON text: encoding/json sorts keys.
func objectFromMap(m map[string]any) *Object {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	object := NewObject()
	for _, key := range keys {
		object.Set(key, m[key])
	}
	return object
}

// jsonView is JSON.parse(json.Marshal(value)) with objects as *Object in
// JSON text order. A value that cannot be encoded is undefined.
func jsonView(value any) any {
	encoded, err := json.Marshal(value)
	if err != nil {
		return Undefined()
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	parsed, err := decodeJSONValue(decoder)
	if err != nil {
		return Undefined()
	}
	return parsed
}

func decodeJSONValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch t := token.(type) {
	case json.Delim:
		switch t {
		case '{':
			object := NewObject()
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				value, err := decodeJSONValue(decoder)
				if err != nil {
					return nil, err
				}
				object.Set(keyToken.(string), value)
			}
			_, err := decoder.Token()
			return object, err
		case '[':
			items := []any{}
			for decoder.More() {
				value, err := decodeJSONValue(decoder)
				if err != nil {
					return nil, err
				}
				items = append(items, value)
			}
			_, err := decoder.Token()
			return items, err
		}
	case json.Number:
		return t.Float64()
	}
	return token, nil
}

type jsonField struct {
	index     []int
	omitEmpty bool
	viaJSON   bool
}

var jsonFieldCache sync.Map // reflect.Type → map[string]jsonField

// structField finds the field whose encoded JSON name is key (T-5.1a).
func structField(value reflect.Value, key string) (any, bool) {
	fields := jsonFields(value.Type())
	field, ok := fields[key]
	if !ok {
		return nil, false
	}
	fieldValue, err := value.FieldByIndexErr(field.index)
	if err != nil {
		return nil, false // nil embedded pointer: the field is not encoded
	}
	if field.omitEmpty && fieldValue.IsZero() {
		return nil, false
	}
	if field.viaJSON {
		return jsonView(fieldValue.Interface()), true
	}
	return fieldValue.Interface(), true
}

func jsonFields(t reflect.Type) map[string]jsonField {
	if cached, ok := jsonFieldCache.Load(t); ok {
		return cached.(map[string]jsonField)
	}
	fields := map[string]jsonField{}
	collectJSONFields(t, nil, fields)
	jsonFieldCache.Store(t, fields)
	return fields
}

var marshalerType = reflect.TypeOf((*json.Marshaler)(nil)).Elem()

func collectJSONFields(t reflect.Type, prefix []int, fields map[string]jsonField) {
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := field.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, options, _ := strings.Cut(tag, ",")
		index := append(append([]int{}, prefix...), i)
		if field.Anonymous && name == "" {
			embedded := field.Type
			if embedded.Kind() == reflect.Pointer {
				embedded = embedded.Elem()
			}
			if embedded.Kind() == reflect.Struct {
				collectJSONFields(embedded, index, fields)
				continue
			}
		}
		if !field.IsExported() {
			continue
		}
		if name == "" {
			name = field.Name
		}
		if _, exists := fields[name]; exists && len(prefix) > 0 {
			continue // an outer field shadows a promoted one
		}
		fieldType := field.Type
		viaJSON := fieldType.Implements(marshalerType) || reflect.PointerTo(fieldType).Implements(marshalerType) ||
			(fieldType.Kind() == reflect.Slice && fieldType.Elem().Kind() == reflect.Uint8) ||
			strings.Contains(options, "string")
		fields[name] = jsonField{index: index, omitEmpty: strings.Contains(options, "omitempty"), viaJSON: viaJSON}
	}
}

// property reads key from a non-nullish value: an own property, an array
// index or length, or a string length or code unit; otherwise undefined.
func property(value any, key string) any {
	switch v := value.(type) {
	case *Object:
		if result, ok := v.Lookup(key); ok {
			return result
		}
		return Undefined()
	case map[string]any:
		if result, ok := v[key]; ok {
			return result
		}
		return Undefined()
	case string:
		units := units(v)
		if key == "length" {
			return float64(len(units))
		}
		if arrayIndexKey(key) {
			if n, _ := strconv.Atoi(key); n < len(units) {
				return fromUnits(units[n : n+1])
			}
		}
		return Undefined()
	}
	reflected := reflect.ValueOf(value)
	for reflected.Kind() == reflect.Pointer || reflected.Kind() == reflect.Interface {
		if reflected.IsNil() {
			return Undefined()
		}
		reflected = reflected.Elem()
	}
	switch reflected.Kind() {
	case reflect.Slice, reflect.Array:
		if reflected.Type().Elem().Kind() == reflect.Uint8 {
			return property(jsonView(value), key)
		}
		if key == "length" {
			return float64(reflected.Len())
		}
		if arrayIndexKey(key) {
			if n, _ := strconv.Atoi(key); n < reflected.Len() {
				return unhole(reflected.Index(n).Interface())
			}
		}
	case reflect.Map:
		if reflected.Type().Key().Kind() != reflect.String {
			return property(jsonView(value), key)
		}
		entry := reflected.MapIndex(reflect.ValueOf(key).Convert(reflected.Type().Key()))
		if entry.IsValid() {
			return entry.Interface()
		}
	case reflect.Struct:
		if _, ok := value.(json.Marshaler); ok || reflected.Type().Implements(marshalerType) {
			return property(jsonView(value), key)
		}
		if result, ok := structField(reflected, key); ok {
			return result
		}
	}
	return Undefined()
}

func nullish(value any) bool {
	if value == nil {
		return true
	}
	if _, ok := value.(UndefinedValue); ok {
		return true
	}
	reflected := reflect.ValueOf(value)
	return (reflected.Kind() == reflect.Pointer || reflected.Kind() == reflect.Interface || reflected.Kind() == reflect.Map || reflected.Kind() == reflect.Slice) && reflected.IsNil()
}

func nullishName(value any) string {
	if _, ok := value.(UndefinedValue); ok {
		return "undefined"
	}
	return "null"
}

// Get reads value.key (T-5.1a). Reading a property of null or undefined
// throws TypeError (T-6.5a); a missing property is undefined (T-6.5b).
func (js *JS) Get(value any, key, site string) any {
	if nullish(value) {
		return js.throw("TypeError", fmt.Sprintf("Cannot read properties of %s (reading '%s')", nullishName(value), key), site)
	}
	return property(value, key)
}

// Index reads value[key] with JavaScript property-key conversion.
func (js *JS) Index(value, key any, site string) any {
	name := propertyKey(key)
	if nullish(value) {
		return js.throw("TypeError", fmt.Sprintf("Cannot read properties of %s (reading '%s')", nullishName(value), name), site)
	}
	return property(value, name)
}

// Length reads value.length.
func (js *JS) Length(value any, site string) any {
	return js.Get(value, "length", site)
}

// SpreadItems returns the elements [...value] spreads: array elements or
// string code points. Other values are not iterable and throw TypeError.
func (js *JS) SpreadItems(value any, site string) []any {
	switch v := jsValue(value).(type) {
	case []any:
		return unholed(v)
	case string:
		return codePoints(v)
	case nil, UndefinedValue:
		js.throw("TypeError", nullishName(v)+" is not iterable", site)
		return nil
	default:
		js.throw("TypeError", fmt.Sprintf("%s is not iterable", TypeOf(value)), site)
		return nil
	}
}

// JoinArrays concatenates the literal segments and spread elements of an
// array literal.
func JoinArrays(parts ...[]any) []any {
	out := []any{}
	for _, part := range parts {
		out = append(out, part...)
	}
	return out
}

// propertyKey converts a computed member key to a property name.
func propertyKey(key any) string {
	return String(jsValue(key))
}

// ── Conversions ──────────────────────────────────────────────────────────

// toPrimitive applies ToPrimitive: arrays and objects become their string
// form (Array.prototype.toString, "[object Object]").
func toPrimitive(value any) any {
	switch v := jsValue(value).(type) {
	case []any:
		return String(v)
	case *Object:
		return "[object Object]"
	default:
		return v
	}
}

// ToNumber implements JavaScript ToNumber over the value domain.
func ToNumber(value any) float64 {
	switch v := toPrimitive(value).(type) {
	case nil:
		return 0
	case UndefinedValue:
		return math.NaN()
	case bool:
		if v {
			return 1
		}
		return 0
	case float64:
		return v
	case string:
		return stringToNumber(v)
	}
	return math.NaN()
}

// stringToNumber implements StringToNumber: whitespace-trimmed decimal,
// Infinity, or 0x/0o/0b integers; the empty string is 0.
func stringToNumber(text string) float64 {
	text = strings.TrimFunc(text, jsWhitespace)
	if text == "" {
		return 0
	}
	switch text {
	case "Infinity", "+Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	}
	if len(text) > 2 && text[0] == '0' {
		base := 0
		switch text[1] {
		case 'x', 'X':
			base = 16
		case 'o', 'O':
			base = 8
		case 'b', 'B':
			base = 2
		}
		if base != 0 {
			n, err := strconv.ParseUint(text[2:], base, 64)
			if err != nil {
				return math.NaN()
			}
			return float64(n)
		}
	}
	for _, c := range text {
		if !(c >= '0' && c <= '9' || c == '.' || c == 'e' || c == 'E' || c == '+' || c == '-') {
			return math.NaN()
		}
	}
	n, err := strconv.ParseFloat(text, 64)
	if err != nil {
		if numErr, ok := err.(*strconv.NumError); ok && numErr.Err == strconv.ErrRange {
			return n
		}
		return math.NaN()
	}
	return n
}

func jsWhitespace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// TypeOf implements typeof over the value domain; Go values follow their
// JSON view (time.Time is "string").
func TypeOf(value any) string {
	if _, ok := value.(Func); ok {
		return "function"
	}
	switch jsValue(value).(type) {
	case UndefinedValue:
		return "undefined"
	case bool:
		return "boolean"
	case float64:
		return "number"
	case string:
		return "string"
	}
	return "object"
}

// ── Operators ────────────────────────────────────────────────────────────

// Add implements + : string concatenation when either primitive operand is
// a string, otherwise numeric addition.
func Add(left, right any) any {
	l, r := toPrimitive(left), toPrimitive(right)
	_, ls := l.(string)
	_, rs := r.(string)
	if ls || rs {
		return String(l) + String(r)
	}
	return ToNumber(l) + ToNumber(r)
}

// Pow implements ** with the edge cases of Math.pow: a NaN exponent and
// ±1 ** ±Infinity are NaN, where Go's math.Pow returns 1.
func Pow(left, right any) float64 {
	base, exponent := ToNumber(left), ToNumber(right)
	if math.IsNaN(exponent) {
		return math.NaN()
	}
	if math.IsInf(exponent, 0) && math.Abs(base) == 1 {
		return math.NaN()
	}
	return jsPow(base, exponent)
}

// compare implements the abstract relational comparison x < y. ok is
// false when a NaN makes the result undefined.
func compare(left, right any) (less, ok bool) {
	l, r := toPrimitive(left), toPrimitive(right)
	ls, lIsString := l.(string)
	rs, rIsString := r.(string)
	if lIsString && rIsString {
		return StringLess(ls, rs), true
	}
	x, y := ToNumber(l), ToNumber(r)
	if math.IsNaN(x) || math.IsNaN(y) {
		return false, false
	}
	return x < y, true
}

// Less, LessEqual, Greater, and GreaterEqual implement < <= > >=.
func Less(left, right any) bool {
	less, ok := compare(left, right)
	return ok && less
}

func Greater(left, right any) bool { return Less(right, left) }

func LessEqual(left, right any) bool {
	greater, ok := compare(right, left)
	return ok && !greater
}

func GreaterEqual(left, right any) bool {
	less, ok := compare(left, right)
	return ok && !less
}

// LooseNull implements x == null.
func LooseNull(value any) bool { return nullish(value) }

// NullishLazy implements left ?? right; right is evaluated only when left
// is null or undefined.
func NullishLazy(left, right func() any) any {
	value := left()
	if nullish(value) {
		return right()
	}
	return value
}

// OptionalChain implements an optional chain link: when base is null or
// undefined the rest of the chain is not evaluated and the result is
// undefined.
func OptionalChain(base any, rest func(any) any) any {
	if nullish(base) {
		return Undefined()
	}
	return rest(base)
}
