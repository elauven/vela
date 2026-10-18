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
//
// Values sit next to their keys (vals[i] belongs to keys[i]). A small object
// is searched linearly, which beats hashing for a few short keys and needs
// no map per object; past objectIndexKeys keys an index is built (task 060).
type Object struct {
	keys  []string
	vals  []any
	index map[string]int
	// frozen is set by Object.freeze: JavaScript writes throw TypeError.
	frozen bool
	// errorName is the name on the prototype of an error object (new
	// Error, new TypeError, …) and empty for other objects. An error's
	// message, when given, is an own property that is not enumerable: it
	// stays out of keys, so Object.keys, spread and JSON do not see it.
	errorName  string
	message    any
	hasMessage bool
	// js is the evaluation state of the render that created the object. A
	// conversion to a primitive can call a function in an own toString or
	// valueOf property, or throw TypeError when it finds no primitive
	// (OrdinaryToPrimitive); the exception is recorded here.
	js *JS
	// local marks an object that a translated function created during this
	// render: its methods may write properties through this (T-6.6b).
	local bool
	// accessors is set once a property is defined by a getter or setter.
	accessors bool
	// nullProto marks an object without a prototype (Object.groupBy): it
	// has no toString or valueOf, so converting it throws TypeError.
	nullProto bool
}

// accessor is the value of a property defined by a getter and/or a
// setter ({ get x() {}, set x(v) {} }).
type accessor struct {
	get, set Func
}

// objectIndexKeys is the size above which an object gets a key index.
const objectIndexKeys = 16

// NewObject returns an empty object.
func NewObject() *Object { return &Object{} }

// NewObject returns an empty object created by translated code of this
// render.
func (js *JS) NewObject() *Object { return &Object{js: js} }

// LocalObject returns an empty object created by a translated function.
func (js *JS) LocalObject() *Object { return &Object{js: js, local: true} }

// find returns the position of key, or -1.
func (o *Object) find(key string) int {
	if o.index != nil {
		if i, ok := o.index[key]; ok {
			return i
		}
		return -1
	}
	for i, k := range o.keys {
		if k == key {
			return i
		}
	}
	return -1
}

// Set assigns a property and returns the object, so a literal lowers to
// one expression. A property with a setter calls it; one with only a
// getter throws TypeError (module code is strict).
func (o *Object) Set(key string, value any) *Object {
	if o.hasMessage && key == "message" {
		o.message = value
		return o
	}
	if i := o.find(key); i >= 0 {
		if property, ok := o.vals[i].(*accessor); ok {
			if property.set == nil {
				o.js.throw("TypeError", fmt.Sprintf("Cannot set property %s of #<Object> which has only a getter", key), "")
				return o
			}
			property.set(o, value)
			return o
		}
		o.vals[i] = value
		return o
	}
	return o.add(key, value)
}

// Define defines a data property of an object literal: a later property of
// the same name replaces an accessor and keeps the first position.
func (o *Object) Define(key string, value any) *Object {
	if i := o.find(key); i >= 0 {
		o.vals[i] = value
		return o
	}
	return o.add(key, value)
}

// DefineGetter and DefineSetter define the accessor halves of an object
// literal's get x() {} and set x(v) {}.
func (o *Object) DefineGetter(key string, get Func) *Object {
	return o.defineAccessor(key, get, nil)
}

func (o *Object) DefineSetter(key string, set Func) *Object {
	return o.defineAccessor(key, nil, set)
}

func (o *Object) defineAccessor(key string, get, set Func) *Object {
	o.accessors = true
	if i := o.find(key); i >= 0 {
		property, ok := o.vals[i].(*accessor)
		if !ok {
			property = &accessor{}
			o.vals[i] = property
		}
		if get != nil {
			property.get = get
		}
		if set != nil {
			property.set = set
		}
		return o
	}
	return o.add(key, &accessor{get: get, set: set})
}

// add appends a new property.
func (o *Object) add(key string, value any) *Object {
	if o.keys == nil {
		// Most translated objects are literals of a few properties.
		o.keys, o.vals = make([]string, 0, 8), make([]any, 0, 8)
	}
	o.keys = append(o.keys, key)
	o.vals = append(o.vals, value)
	switch {
	case o.index != nil:
		o.index[key] = len(o.keys) - 1
	case len(o.keys) > objectIndexKeys:
		o.index = make(map[string]int, 2*len(o.keys))
		for i, k := range o.keys {
			o.index[k] = i
		}
	}
	return o
}

// entries returns the keys and their values in enumeration order (Keys),
// getters called. Without integer-like keys or accessors these are the
// object's own slices: read only.
func (o *Object) entries() ([]string, []any) {
	keys := o.Keys()
	if len(keys) > 0 && len(o.keys) > 0 && &keys[0] == &o.keys[0] && !o.accessors {
		return keys, o.vals
	}
	vals := make([]any, len(keys))
	for i, key := range keys {
		vals[i] = o.read(o.vals[o.find(key)])
	}
	return keys, vals
}

// read returns the value of a property: its getter's result for an
// accessor (undefined without a getter).
func (o *Object) read(value any) any {
	property, ok := value.(*accessor)
	if !ok {
		return value
	}
	if property.get == nil {
		return Undefined()
	}
	return property.get(o)
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
		keys, vals := source.entries()
		for i, key := range keys {
			o.Define(key, vals[i])
		}
		if o.js == nil {
			o.js = source.js
		}
	}
	return o
}

// Lookup returns a property, its getter called, and whether it exists.
func (o *Object) Lookup(key string) (any, bool) {
	if i := o.find(key); i >= 0 {
		return o.read(o.vals[i]), true
	}
	if o.errorName != "" {
		// Error.prototype gives name, and message "" when none was given.
		switch {
		case key == "message" && o.hasMessage:
			return o.message, true
		case key == "message":
			return "", true
		case key == "name":
			return o.errorName, true
		}
	}
	return nil, false
}

// Keys returns the property names in JavaScript enumeration order.
func (o *Object) Keys() []string {
	integer := false
	for _, key := range o.keys {
		if arrayIndexKey(key) {
			integer = true
			break
		}
	}
	if !integer {
		// Insertion order already is the JavaScript order. The capacity is
		// capped so that a caller's append cannot reach the object's keys.
		return o.keys[:len(o.keys):len(o.keys)]
	}
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
	return appendJSON(nil, o)
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

// ForInKeys returns the keys for…in visits, taken before the first
// iteration: the own enumerable keys of an object in enumeration order,
// the indices of an array without its holes and of a string's code units;
// none for other values, null and undefined included.
func ForInKeys(value any) []any {
	var keys []string
	switch v := jsValue(value).(type) {
	case *Object:
		keys = v.Keys()
	case []any:
		for i, item := range v {
			if !isHole(item) {
				keys = append(keys, strconv.Itoa(i))
			}
		}
	case string:
		for i := range units(v) {
			keys = append(keys, strconv.Itoa(i))
		}
	}
	out := make([]any, len(keys))
	for i, key := range keys {
		out[i] = key
	}
	return out
}

// RestItems returns a copy of items from index from: the array a rest
// element or a rest parameter binds.
func RestItems(items []any, from int) []any {
	if from >= len(items) {
		return []any{}
	}
	return append([]any{}, items[from:]...)
}

// ObjectRest returns the object a rest property of an object pattern binds:
// a copy of the own enumerable properties of value without the excluded
// keys, in enumeration order. Destructuring null or undefined throws
// TypeError.
func (js *JS) ObjectRest(value any, excluded []string, site string) *Object {
	if nullish(value) {
		js.throw("TypeError", fmt.Sprintf("Cannot destructure '%s' as it is %s.", String(jsValue(value)), nullishName(value)), site)
		return js.NewObject()
	}
	all := js.NewObject().Spread(value)
	if len(excluded) == 0 {
		return all
	}
	rest := js.NewObject()
	keys, vals := all.entries()
next:
	for i, key := range keys {
		for _, skip := range excluded {
			if key == skip {
				continue next
			}
		}
		rest.Set(key, vals[i])
	}
	return rest
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

// PropertyKey converts the key of a computed property ({ [k]: v }) to a
// property name (ToPropertyKey).
func PropertyKey(key any) string {
	return propertyKey(key)
}

// ── Conversions ──────────────────────────────────────────────────────────

// toPrimitive applies ToPrimitive with the hint "default": arrays become
// their string form (Array.prototype.toString), objects follow
// OrdinaryToPrimitive.
func toPrimitive(value any) any {
	return toPrimitiveHint(value, "default")
}

// toPrimitiveHint applies ToPrimitive with a hint: "string", "number", or
// "default".
func toPrimitiveHint(value any, hint string) any {
	switch v := jsValue(value).(type) {
	case []any:
		return String(v)
	case *Object:
		return v.toPrimitive(hint)
	default:
		return v
	}
}

// toPrimitive is OrdinaryToPrimitive: valueOf then toString, toString first
// for the hint "string". A function in an own property is called and its
// result taken when it is a primitive; an own property that is not a
// function hides the method of the prototype. Object.prototype.valueOf
// returns the object itself, so the inherited toString ends the search.
// When no method gives a primitive, the conversion throws TypeError.
func (o *Object) toPrimitive(hint string) any {
	names := [2]string{"valueOf", "toString"}
	if hint == "string" {
		names = [2]string{"toString", "valueOf"}
	}
	for _, name := range names {
		value, own := o.Lookup(name)
		if !own {
			if name == "toString" && !o.nullProto {
				return objectString(o)
			}
			continue
		}
		fn, ok := value.(Func)
		if !ok {
			continue
		}
		result := jsValue(fn(o))
		if o.js.Failed() {
			return Undefined()
		}
		switch result.(type) {
		case nil, UndefinedValue, bool, float64, string:
			return result
		}
	}
	return o.js.throw("TypeError", "Cannot convert object to primitive value", "")
}

// objectString converts an object to text: Error.prototype.toString for an
// error object ("Name: message", either part alone when the other is
// empty), "[object Object]" for other objects.
func objectString(o *Object) string {
	if o.errorName == "" {
		return "[object Object]"
	}
	name, message := "Error", ""
	if value, _ := o.Lookup("name"); !isUndefinedValue(value) {
		name = String(value)
	}
	if value, _ := o.Lookup("message"); !isUndefinedValue(value) {
		message = String(value)
	}
	switch {
	case name == "":
		return message
	case message == "":
		return name
	}
	return name + ": " + message
}

// ToNumber implements JavaScript ToNumber over the value domain.
func ToNumber(value any) float64 {
	switch v := toPrimitiveHint(value, "number").(type) {
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

// AddString converts an operand of + whose other operand is a string:
// ToPrimitive with the hint "default", then ToString.
func AddString(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return String(toPrimitive(value))
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
	l, r := toPrimitiveHint(left, "number"), toPrimitiveHint(right, "number")
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
