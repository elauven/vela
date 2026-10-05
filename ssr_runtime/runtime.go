package ssr_runtime

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"math"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Renderer is the composable server-side rendering contract.
type Renderer interface {
	Render(io.Writer) error
}

// RendererFunc adapts a direct rendering function to Renderer.
type RendererFunc func(io.Writer) error

func (render RendererFunc) Render(writer io.Writer) error {
	return render(writer)
}

// UndefinedValue represents JavaScript's absent-value sentinel inside the
// server expression subset. It deliberately differs from nil, which models a
// JavaScript null literal/value. The public Go render API still cannot infer
// the distinction for an untyped nil prop, so the compiler only emits this
// sentinel for source-level undefined and missing object properties.
type UndefinedValue struct{}

// Undefined returns the server representation of JavaScript undefined.
func Undefined() UndefinedValue { return UndefinedValue{} }

// StrictEqual implements JavaScript strict equality for values admitted by
// the server expression model. Numeric Go kinds share JavaScript's single
// Number domain, null and undefined remain distinct, and reference values are
// compared by identity rather than by Go's structural equality. In
// particular, this helper never applies == to an interface containing a map
// or slice, which would panic at runtime.
func StrictEqual(left, right any) bool {
	_, leftUndefined := left.(UndefinedValue)
	_, rightUndefined := right.(UndefinedValue)
	if leftUndefined || rightUndefined {
		return leftUndefined && rightUndefined
	}

	leftValue, leftNull := strictEqualityValue(left)
	rightValue, rightNull := strictEqualityValue(right)
	if leftNull || rightNull {
		return leftNull && rightNull
	}

	leftNumber, leftIsNumber := strictEqualityNumber(leftValue)
	rightNumber, rightIsNumber := strictEqualityNumber(rightValue)
	if leftIsNumber || rightIsNumber {
		if !leftIsNumber || !rightIsNumber || math.IsNaN(leftNumber) || math.IsNaN(rightNumber) {
			return false
		}
		return leftNumber == rightNumber
	}

	if leftValue.Kind() == reflect.String || rightValue.Kind() == reflect.String {
		return leftValue.Kind() == reflect.String && rightValue.Kind() == reflect.String && leftValue.String() == rightValue.String()
	}
	if leftValue.Kind() == reflect.Bool || rightValue.Kind() == reflect.Bool {
		return leftValue.Kind() == reflect.Bool && rightValue.Kind() == reflect.Bool && leftValue.Bool() == rightValue.Bool()
	}

	// Maps, slices, and pointers model JavaScript reference values at the Go
	// boundary. Type equality prevents unrelated Go representations from
	// becoming the same JavaScript object merely because an address matches.
	if leftValue.Type() != rightValue.Type() {
		return false
	}
	switch leftValue.Kind() {
	case reflect.Map, reflect.Pointer:
		return leftValue.Pointer() == rightValue.Pointer()
	case reflect.Slice:
		return leftValue.Pointer() == rightValue.Pointer() && leftValue.Len() == rightValue.Len() && leftValue.Cap() == rightValue.Cap()
	default:
		// Structs, Go arrays, functions, and other host values have no stable
		// JavaScript reference identity in the serialized SSR contract.
		return false
	}
}

func strictEqualityValue(value any) (reflect.Value, bool) {
	if value == nil {
		return reflect.Value{}, true
	}
	reflected := reflect.ValueOf(value)
	for reflected.IsValid() && reflected.Kind() == reflect.Interface {
		if reflected.IsNil() {
			return reflect.Value{}, true
		}
		reflected = reflected.Elem()
	}
	if !reflected.IsValid() {
		return reflect.Value{}, true
	}
	switch reflected.Kind() {
	case reflect.Map, reflect.Pointer, reflect.Slice:
		if reflected.IsNil() {
			return reflect.Value{}, true
		}
	}
	return reflected, false
}

func strictEqualityNumber(value reflect.Value) (float64, bool) {
	switch value.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(value.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return float64(value.Uint()), true
	case reflect.Float32, reflect.Float64:
		return value.Float(), true
	default:
		return 0, false
	}
}

// StringLess compares strings using JavaScript's UTF-16 code-unit order.
// Go compares UTF-8 bytes, which differs when an astral code point is ordered
// against some BMP values, so generated relational expressions must not use
// the native Go string operator directly.
func StringLess(left, right string) bool {
	leftUnits := utf16.Encode([]rune(left))
	rightUnits := utf16.Encode([]rune(right))
	limit := len(leftUnits)
	if len(rightUnits) < limit {
		limit = len(rightUnits)
	}
	for index := 0; index < limit; index++ {
		if leftUnits[index] == rightUnits[index] {
			continue
		}
		return leftUnits[index] < rightUnits[index]
	}
	return len(leftUnits) < len(rightUnits)
}

// Index implements the admitted JavaScript computed-member subset without
// exposing Go's out-of-range panics. Arrays accept non-negative integral
// Number indices; plain objects accept string keys. Missing entries preserve
// the distinct undefined sentinel used by template conversion.
func Index(value, index any) any {
	reflected := reflect.ValueOf(value)
	for reflected.IsValid() && (reflected.Kind() == reflect.Interface || reflected.Kind() == reflect.Pointer) {
		if reflected.IsNil() {
			return Undefined()
		}
		reflected = reflected.Elem()
	}
	if !reflected.IsValid() {
		return Undefined()
	}
	switch reflected.Kind() {
	case reflect.Array, reflect.Slice:
		indexValue, ok := strictEqualityNumber(reflect.ValueOf(index))
		if !ok || math.IsNaN(indexValue) || math.IsInf(indexValue, 0) || indexValue < 0 || math.Trunc(indexValue) != indexValue || indexValue >= float64(reflected.Len()) {
			return Undefined()
		}
		return reflected.Index(int(indexValue)).Interface()
	case reflect.Map:
		key := reflect.ValueOf(index)
		if !key.IsValid() || key.Kind() != reflect.String || reflected.Type().Key().Kind() != reflect.String {
			return Undefined()
		}
		if key.Type() != reflected.Type().Key() {
			key = key.Convert(reflected.Type().Key())
		}
		entry := reflected.MapIndex(key)
		if !entry.IsValid() {
			return Undefined()
		}
		return entry.Interface()
	default:
		return Undefined()
	}
}

// Number converts the Go representations admitted by the SSR type model into
// JavaScript's single IEEE-754 Number domain. Callers are statically checked
// by the compiler; NaN is a defensive result for an unexpected host value.
func Number(value any) float64 {
	reflected := reflect.ValueOf(value)
	for reflected.IsValid() && (reflected.Kind() == reflect.Interface || reflected.Kind() == reflect.Pointer) {
		if reflected.IsNil() {
			return 0
		}
		reflected = reflected.Elem()
	}
	if number, ok := strictEqualityNumber(reflected); ok {
		return number
	}
	return math.NaN()
}

// Numeric operations use functions rather than Go constant expressions so
// division by zero, overflow, remainder, and negative zero retain JavaScript
// runtime behaviour instead of becoming Go compile errors.
func NumberAdd(left, right any) float64      { return Number(left) + Number(right) }
func NumberSubtract(left, right any) float64 { return Number(left) - Number(right) }
func NumberMultiply(left, right any) float64 { return Number(left) * Number(right) }
func NumberDivide(left, right any) float64   { return Number(left) / Number(right) }
func NumberRemainder(left, right any) float64 {
	return math.Mod(Number(left), Number(right))
}
func NumberNegate(value any) float64 { return -Number(value) }

func numberString(value reflect.Value) string {
	number, ok := strictEqualityNumber(value)
	if !ok {
		return "NaN"
	}
	switch {
	case math.IsNaN(number):
		return "NaN"
	case math.IsInf(number, 1):
		return "Infinity"
	case math.IsInf(number, -1):
		return "-Infinity"
	case number == 0:
		return "0"
	}

	sign := ""
	if number < 0 {
		sign = "-"
		number = -number
	}
	scientific := strconv.FormatFloat(number, 'e', -1, 64)
	eIndex := strings.IndexByte(scientific, 'e')
	mantissa := scientific[:eIndex]
	exponent, _ := strconv.Atoi(scientific[eIndex+1:])
	digits := strings.ReplaceAll(mantissa, ".", "")
	decimalPosition := exponent + 1

	switch {
	case decimalPosition > 0 && decimalPosition <= 21:
		if decimalPosition >= len(digits) {
			return sign + digits + strings.Repeat("0", decimalPosition-len(digits))
		}
		return sign + digits[:decimalPosition] + "." + digits[decimalPosition:]
	case decimalPosition > -6 && decimalPosition <= 0:
		return sign + "0." + strings.Repeat("0", -decimalPosition) + digits
	default:
		exponential := digits[:1]
		if len(digits) > 1 {
			exponential += "." + digits[1:]
		}
		exponentText := strconv.Itoa(exponent)
		if exponent >= 0 {
			exponentText = "+" + exponentText
		}
		return sign + exponential + "e" + exponentText
	}
}

// PropProvided reports whether a typed generated SSR props value explicitly
// supplied a Vela prop. Generated contracts expose Set; older user-maintained
// sidecars without it retain their historical behaviour and are treated as
// fully provided until the developer opts into the presence contract.
func PropProvided(props any, name string) bool {
	value := reflect.ValueOf(props)
	for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
		if value.IsNil() {
			return false
		}
		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return true
	}
	set := value.FieldByName("Set")
	if !set.IsValid() || set.Kind() != reflect.Map || set.Type().Key().Kind() != reflect.String || set.Type().Elem().Kind() != reflect.Bool {
		return true
	}
	// A nil Set is the compatibility mode for pre-presence-map generated
	// sidecars and ordinary Go struct literals. Opting into exact presence
	// tracking is explicit: callers initialise Set (including an empty map)
	// before rendering.
	if set.IsNil() {
		return true
	}
	provided := set.MapIndex(reflect.ValueOf(name))
	return provided.IsValid() && provided.Bool()
}

// MarshalHydrationPayload serializes the client-owned SSR payload with a
// stable diagnostic for values that cannot cross the server/browser boundary.
// The generated renderer already constructs an allow-list of declared Vela
// props, so a failure here points to a non-serializable declared prop rather
// than an arbitrary server-only sidecar field.
func MarshalHydrationPayload(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("VELA-SSR-PROPS: declared Vela props must be JSON-serializable: %w", err)
	}
	return encoded, nil
}

// Slot is a server-rendered component slot. Phase 2 will populate named slot
// fields with this type while the Phase 1 slot struct remains intentionally
// empty.
type Slot func(io.Writer) error

// WriteString preserves direct error propagation from a renderer write.
func WriteString(writer io.Writer, value string) error {
	_, err := io.WriteString(writer, value)
	return err
}

// String implements the template-facing JavaScript ToString subset used for
// standalone interpolation and ordinary dynamic attributes. Null and
// undefined are observable JavaScript values here; array joining intentionally
// has its own nullish-member rule below.
func String(value any) string {
	if value == nil {
		return "null"
	}
	if _, undefined := value.(UndefinedValue); undefined {
		return "undefined"
	}
	reflected := reflect.ValueOf(value)
	for reflected.Kind() == reflect.Interface || reflected.Kind() == reflect.Pointer {
		if reflected.IsNil() {
			return "null"
		}
		reflected = reflected.Elem()
	}
	if reflected.Kind() == reflect.Slice || reflected.Kind() == reflect.Array {
		parts := make([]string, reflected.Len())
		for index := 0; index < reflected.Len(); index++ {
			parts[index] = ArrayJoinString(reflected.Index(index).Interface())
		}
		return strings.Join(parts, ",")
	}
	if reflected.Kind() == reflect.Map || reflected.Kind() == reflect.Struct {
		return "[object Object]"
	}
	if _, numeric := strictEqualityNumber(reflected); numeric {
		return numberString(reflected)
	}
	return fmt.Sprint(value)
}

// ArrayJoinString implements the distinct Array.prototype.join conversion
// rule: nil and undefined members become empty fields, while all other values
// follow the normal JavaScript text conversion. Nested arrays recursively use
// this same rule through String.
func ArrayJoinString(value any) string {
	if value == nil {
		return ""
	}
	if _, undefined := value.(UndefinedValue); undefined {
		return ""
	}
	reflected := reflect.ValueOf(value)
	for reflected.IsValid() && (reflected.Kind() == reflect.Interface || reflected.Kind() == reflect.Pointer) {
		if reflected.IsNil() {
			return ""
		}
		reflected = reflected.Elem()
	}
	return String(value)
}

// FormValueString implements the HTML input/textarea value property boundary.
// It intentionally differs from template text: assigning JavaScript null to
// those properties produces an empty string, while undefined and ordinary
// values use their normal DOMString conversion.
func FormValueString(value any) string {
	if value == nil {
		return ""
	}
	if _, undefined := value.(UndefinedValue); undefined {
		return "undefined"
	}
	reflected := reflect.ValueOf(value)
	for reflected.Kind() == reflect.Interface || reflected.Kind() == reflect.Pointer {
		if reflected.IsNil() {
			return ""
		}
		reflected = reflected.Elem()
	}
	return String(value)
}

// TemplateString joins values after JavaScript template-literal conversion.
// The current compiler only emits it for non-nullish values (or literal
// quasis), but retaining the nil branch makes its behaviour explicit for the
// future typed null/undefined value model.
func TemplateString(values ...any) string {
	var out strings.Builder
	for _, value := range values {
		if value == nil {
			out.WriteString("null")
			continue
		}
		if _, undefined := value.(UndefinedValue); undefined {
			out.WriteString("undefined")
			continue
		}
		out.WriteString(String(value))
	}
	return out.String()
}

// Length returns JavaScript's .length for the baseline string/array subset.
// JavaScript counts UTF-16 code units rather than UTF-8 bytes or Go runes.
func Length(value any) int {
	if value == nil {
		return 0
	}
	if stringValue, ok := value.(string); ok {
		return len(utf16.Encode([]rune(stringValue)))
	}
	reflected := reflect.ValueOf(value)
	if reflected.Kind() == reflect.Slice || reflected.Kind() == reflect.Array || reflected.Kind() == reflect.Map {
		return reflected.Len()
	}
	return 0
}

// Truthy implements JavaScript truthiness for values admitted by the baseline
// SSR expression subset. Arrays and objects stay truthy even when empty.
func Truthy(value any) bool {
	if value == nil {
		return false
	}
	if _, undefined := value.(UndefinedValue); undefined {
		return false
	}
	switch value := value.(type) {
	case bool:
		return value
	case string:
		return value != ""
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return reflected.Int() != 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return reflected.Uint() != 0
	case reflect.Float32, reflect.Float64:
		value := reflected.Float()
		return value != 0 && !math.IsNaN(value)
	case reflect.Interface, reflect.Pointer:
		return !reflected.IsNil()
	default:
		return true
	}
}

// TernaryLazy preserves JavaScript conditional evaluation: the condition is
// evaluated once and only the selected branch closure runs. The closures are
// emitted by the Go-AST backend rather than evaluating translated expressions
// as eager function arguments.
func TernaryLazy(condition, thenValue, elseValue func() any) any {
	if Truthy(condition()) {
		return thenValue()
	}
	return elseValue()
}

// AndLazy returns the same operand JavaScript's && would return, evaluating
// the right operand only when the left result is truthy.
func AndLazy(left, right func() any) any {
	leftValue := left()
	if Truthy(leftValue) {
		return right()
	}
	return leftValue
}

// OrLazy returns the same operand JavaScript's || would return, evaluating
// the right operand only when the left result is falsy.
func OrLazy(left, right func() any) any {
	leftValue := left()
	if Truthy(leftValue) {
		return leftValue
	}
	return right()
}

// ContainsString reports whether a server-rendered multi-select model contains
// candidate after the same visible-value conversion used for regular select
// bindings. JavaScript select multiple has set-like membership semantics: an
// option is selected when its value occurs in the model, regardless of model
// order or duplicate entries.
//
// Generated prop/state values can be []string, []any, or another slice/array
// inferred by Go. Reflection keeps the generated renderer type-safe without
// forcing every application model through JSON or an untyped helper map.
func ContainsString(values any, candidate string) bool {
	if values == nil {
		return false
	}
	value := reflect.ValueOf(values)
	if value.Kind() != reflect.Slice && value.Kind() != reflect.Array {
		return false
	}
	for index := 0; index < value.Len(); index++ {
		if String(value.Index(index).Interface()) == candidate {
			return true
		}
	}
	return false
}

// Iter exposes the array-like subset of a JavaScript value to generated Go
// renderers. Props and object members that cross the SSR boundary are often
// represented as any, so ranging over them directly would not type-check in
// Go. The adapter preserves array order and makes non-array values render as
// an empty collection, matching the existing SSR fallback behaviour without
// panicking on malformed input.
func Iter(value any) []any {
	if value == nil {
		return nil
	}
	reflected := reflect.ValueOf(value)
	if reflected.Kind() != reflect.Slice && reflected.Kind() != reflect.Array {
		return nil
	}
	items := make([]any, reflected.Len())
	for index := range items {
		items[index] = reflected.Index(index).Interface()
	}
	return items
}

// Get reads one plain JavaScript-style object field from data supplied to an
// SSR renderer. Vela's current generated prop surface represents unknown JS
// object values as map[string]any; absent or non-object fields intentionally
// become nil, matching an undefined-like render value instead of panicking.
func Get(value any, key string) any {
	switch object := value.(type) {
	case map[string]any:
		result, ok := object[key]
		if !ok {
			return Undefined()
		}
		return result
	case map[string]string:
		result, ok := object[key]
		if !ok {
			return Undefined()
		}
		return result
	case map[string]bool:
		result, ok := object[key]
		if !ok {
			return Undefined()
		}
		return result
	case map[string]float64:
		result, ok := object[key]
		if !ok {
			return Undefined()
		}
		return result
	default:
		return Undefined()
	}
}

// EscapeString escapes text and attribute content for HTML output.
func EscapeString(value string) string {
	return html.EscapeString(value)
}

// RootPayloadVersion is the schema version of the payload stream written by
// block-engine renderers (docs/HYDRATION_ARCHITECTURE.md §6).
const RootPayloadVersion = 2

// Stream collects the ordered payload entries of one root render (S-6.1).
// Component instances and server-rendered await blocks append entries in
// render order; the client consumes them in the same order.
type Stream struct {
	entries []any
}

// streamWriter carries the render state of one root through every nested
// component and slot: they all receive an io.Writer, so the stream needs no
// extra renderer parameter. static suppresses markers and entries (A-8.3).
type streamWriter struct {
	io.Writer
	stream *Stream
	static bool
	// hostScope is the scope attribute of the parent component, written on
	// the root element of the next child component (B-3.8).
	hostScope string
}

// StaticWriter makes a render write plain HTML: no block markers, no
// payload script (A-8.3).
func StaticWriter(w io.Writer) io.Writer {
	if sw, ok := w.(*streamWriter); ok && sw.static {
		return sw
	}
	return &streamWriter{Writer: w, static: true}
}

// BeginComponent registers a component instance. When w does not belong to
// a render yet, the component is the root: a new stream starts and root is
// true. The component's state entry is recorded (S-6.4); state is the
// compile-time JSON of its initial state and omitted lists bindings whose
// initial value could not be serialized. The caller writes the component
// range with OpenRange and CloseRange.
func BeginComponent(w io.Writer, component, state string, omitted ...string) (io.Writer, bool) {
	sw, ok := w.(*streamWriter)
	root := !ok
	if root {
		sw = &streamWriter{Writer: w, stream: &Stream{}}
	}
	if sw.static {
		return sw, root
	}
	if state == "" {
		state = "{}"
	}
	entry := []any{"s", component, json.RawMessage(state)}
	if len(omitted) > 0 {
		entry = append(entry, map[string][]string{"omitted": omitted})
	}
	sw.stream.entries = append(sw.stream.entries, entry)
	return sw, root
}

// EndComponent finishes a component after its range. A root writes its
// payload script right after the range (M-5.6); props is the JSON of the
// root's declared props after defaults (S-6.2).
func EndComponent(w io.Writer, root bool, props json.RawMessage) error {
	sw, ok := w.(*streamWriter)
	if !ok {
		return fmt.Errorf("vela: EndComponent without BeginComponent")
	}
	if sw.static || !root {
		return nil
	}
	return writeRootPayload(sw.Writer, props, sw.stream)
}

// OpenRange writes the start marker of a block range with its state token
// (§5.1); CloseRange writes the end marker. Static renders write neither.
func OpenRange(w io.Writer, state string) error {
	if sw, ok := w.(*streamWriter); ok && sw.static {
		return nil
	}
	return WriteString(w, "<!--["+state+"-->")
}

func CloseRange(w io.Writer) error {
	if sw, ok := w.(*streamWriter); ok && sw.static {
		return nil
	}
	return WriteString(w, "<!--]-->")
}

// AwaitEntry records the branch a server-rendered await block selected
// (S-6.4) and returns its state token. state is "", "pending", "then" or
// "catch" as in the render options.
func AwaitEntry(w io.Writer, site, state, value, errText string) string {
	token := "p"
	snapshot := map[string]string{}
	switch state {
	case "then":
		token = "t"
		snapshot["value"] = value
	case "catch":
		token = "c"
		snapshot["error"] = errText
	}
	snapshot["state"] = token
	if sw, ok := w.(*streamWriter); ok && !sw.static {
		sw.stream.entries = append(sw.stream.entries, []any{"a", site, snapshot})
	}
	return token
}

// writeRootPayload writes the payload script of a root. encoding/json
// escapes <, > and & (and U+2028/U+2029), so the content cannot close the
// script element or open a comment (S-6.6).
func writeRootPayload(w io.Writer, props json.RawMessage, stream *Stream) error {
	entries := stream.entries
	if entries == nil {
		entries = []any{}
	}
	if len(props) == 0 {
		props = json.RawMessage("{}")
	}
	encoded, err := json.Marshal(struct {
		Version int             `json:"v"`
		Props   json.RawMessage `json:"props"`
		Entries []any           `json:"entries"`
	}{RootPayloadVersion, props, entries})
	if err != nil {
		return err
	}
	if err := WriteString(w, `<script type="application/json" data-vela-root>`); err != nil {
		return err
	}
	if _, err := w.Write(encoded); err != nil {
		return err
	}
	return WriteString(w, `</script>`)
}

// SetHostScope passes the parent's scope attribute to the root element of
// the child component rendered next, as the client sets it on the child's
// root element.
func SetHostScope(w io.Writer, scope string) {
	if sw, ok := w.(*streamWriter); ok {
		sw.hostScope = scope
	}
}

// WriteHostScope writes the pending parent scope attribute inside the open
// tag of a component's root element and clears it.
func WriteHostScope(w io.Writer) error {
	sw, ok := w.(*streamWriter)
	if !ok || sw.hostScope == "" {
		return nil
	}
	scope := sw.hostScope
	sw.hostScope = ""
	return WriteString(w, " "+scope)
}

// AssignProp stores a value computed by a parent template in a prop field
// of a child component. A value of the field's Go type is stored as is; any
// other value is converted through its JSON view (T-5.1), which is how the
// browser sees it, so a JSON number reaches a float64 field and an object
// reaches a struct field. null reaches only a field that can hold it. A
// value the field cannot hold is a render error naming the prop: the
// child's Go props contract is authoritative (§5.2).
func AssignProp[T any](field *T, value any, prop string) error {
	if typed, ok := value.(T); ok {
		*field = typed
		return nil
	}
	if _, undefined := value.(UndefinedValue); undefined {
		value = nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("VELA-SSR-PROPS: prop %s: value is not JSON-serializable: %w", prop, err)
	}
	var converted T
	target := reflect.TypeOf(field).Elem()
	if string(encoded) == "null" {
		switch target.Kind() {
		case reflect.Interface, reflect.Pointer, reflect.Slice, reflect.Map:
			*field = converted
			return nil
		}
		return fmt.Errorf("VELA-SSR-PROPS: prop %s: Go type %s cannot hold null; declare the field as a pointer or any in the child's props contract", prop, target)
	}
	if err := json.Unmarshal(encoded, &converted); err != nil {
		return fmt.Errorf("VELA-SSR-PROPS: prop %s: Go type %s cannot hold %s: %w", prop, target, encoded, err)
	}
	*field = converted
	return nil
}
