package ssr_runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"
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
type UndefinedValue struct {
	// hole marks an array hole: an element that was never set. It reads
	// as undefined; array methods skip it where JavaScript does.
	hole bool
}

// Undefined returns the server representation of JavaScript undefined.
func Undefined() UndefinedValue { return UndefinedValue{} }

// StrictEqual implements JavaScript strict equality for values admitted by
// the server expression model. Numeric Go kinds share JavaScript's single
// Number domain, null and undefined remain distinct, and reference values are
// compared by identity rather than by Go's structural equality. In
// particular, this helper never applies == to an interface containing a map
// or slice, which would panic at runtime.
func StrictEqual(left, right any) bool {
	if l, ok := left.(string); ok {
		if r, ok := right.(string); ok && (!utf8.ValidString(l) || !utf8.ValidString(r)) {
			// Surrogate halves joined by concatenation equal the pair.
			return fromUnits(units(l)) == fromUnits(units(r))
		}
	}
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
	leftUnits := units(left)
	rightUnits := units(right)
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
	if nullish(value) {
		return Undefined()
	}
	return property(value, propertyKey(index))
}

// Number converts a value to JavaScript's single IEEE-754 Number domain
// with ToNumber (value.go).
func Number(value any) float64 {
	return ToNumber(value)
}

// Numeric operations use functions rather than Go constant expressions so
// division by zero, overflow, remainder, and negative zero retain JavaScript
// runtime behaviour instead of becoming Go compile errors.
func NumberAdd(left, right any) float64      { return Number(left) + Number(right) }
func NumberSubtract(left, right any) float64 { return Number(left) - Number(right) }
func NumberMultiply(left, right any) float64 { return float64(Number(left) * Number(right)) }
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
	// The buffer starts at the size of the previous payload of the same
	// props type: one allocation instead of a series of growths, which was
	// half of the bytes a large render allocated (task 060).
	hint, _ := payloadSizes.LoadOrStore(reflect.TypeOf(value), new(atomic.Int64))
	size := hint.(*atomic.Int64)
	encoded, err := appendJSON(make([]byte, 0, size.Load()*9/8+64), value)
	if err != nil {
		return nil, fmt.Errorf("VELA-SSR-PROPS: declared Vela props must be JSON-serializable: %w", err)
	}
	size.Store(int64(len(encoded)))
	return encoded, nil
}

// payloadSizes maps a props type to the length of its last payload.
var payloadSizes sync.Map

// Slot is a server-rendered component slot. Phase 2 will populate named slot
// fields with this type while the Phase 1 slot struct remains intentionally
// empty.
type Slot func(io.Writer) error

// WriteString preserves direct error propagation from a renderer write.
// Inside a render it appends to the render's buffer (task 060).
func WriteString(writer io.Writer, value string) error {
	if sw, ok := writer.(*streamWriter); ok {
		return sw.out.writeString(value)
	}
	_, err := io.WriteString(writer, value)
	return err
}

// WriteEscaped writes EscapeString(value): text without characters to
// escape, the usual case, is written as is without a new string.
func WriteEscaped(writer io.Writer, value string) error {
	ascii := true
	for i := 0; i < len(value); i++ {
		c := value[i]
		if htmlSpecial[c] {
			return WriteString(writer, EscapeString(value))
		}
		ascii = ascii && c < utf8.RuneSelf
	}
	if !ascii && !utf8.ValidString(value) {
		// WellFormed replaces ill-formed text (T-6.3b).
		return WriteString(writer, EscapeString(value))
	}
	return WriteString(writer, value)
}

// htmlSpecial marks the five characters html.EscapeString escapes.
var htmlSpecial = [256]bool{'&': true, '\'': true, '<': true, '>': true, '"': true}

// String implements the template-facing JavaScript ToString subset used for
// standalone interpolation and ordinary dynamic attributes. Null and
// undefined are observable JavaScript values here; array joining intentionally
// has its own nullish-member rule below.
func String(value any) string {
	// Fast paths for the values templates print most (task 058): a string
	// went through reflection and fmt.Sprint.
	switch v := value.(type) {
	case nil:
		return "null"
	case *Object:
		return String(v.toPrimitive("string"))
	case string:
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	case float64:
		if v == math.Trunc(v) && math.Abs(v) < 1<<53 {
			return strconv.FormatInt(int64(v), 10)
		}
	case int:
		if v > -1<<53 && v < 1<<53 {
			return strconv.Itoa(v)
		}
	case int64:
		if v > -1<<53 && v < 1<<53 {
			return strconv.FormatInt(v, 10)
		}
	}
	if _, promise := value.(*Promise); promise {
		return "[object Promise]"
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
		return len(units(stringValue))
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

// Get reads value.key through the JSON view (T-5.1a) without throwing: a
// nullish receiver yields undefined. Renderers with an evaluation state use
// JS.Get, which throws TypeError instead (T-6.5a).
func Get(value any, key string) any {
	if nullish(value) {
		return Undefined()
	}
	return property(value, key)
}

// EscapeString escapes text and attribute content for HTML output.
func EscapeString(value string) string {
	// A lone surrogate is written as U+FFFD (T-6.3b).
	return html.EscapeString(WellFormed(value))
}

// RootPayloadVersion is the schema version of the payload stream written by
// block-engine renderers (docs/HYDRATION_ARCHITECTURE.md §6).
const RootPayloadVersion = 3

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
	out    *out
	stream *Stream
	static bool
	// ctx is the render context (WithContext). pending marks a writer that
	// carries a context but whose root component has not started yet.
	ctx     context.Context
	pending bool
	// hostScope is the scope attribute of the parent component, written on
	// the root element of the next child component (B-3.8).
	hostScope string
}

// Write and WriteString let a slot written by hand use w as any writer;
// the bytes join the render's buffer in order.
func (sw *streamWriter) Write(p []byte) (int, error) {
	if err := sw.out.write(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (sw *streamWriter) WriteString(s string) (int, error) {
	if err := sw.out.writeString(s); err != nil {
		return 0, err
	}
	return len(s), nil
}

// out is the output of one render, shared by every writer of it (the
// copies WithContext, StaticWriter and BeginComponent make). Generated code
// writes many short strings; they are appended to buf, which goes to w in
// chunks of outChunk bytes and when the outermost component returns, also
// with an error: output written before an error is not rolled back
// (T-6.5c). A *bytes.Buffer is written directly (task 060).
type out struct {
	w      io.Writer
	direct *bytes.Buffer
	buf    []byte
	// depth counts the components being rendered; at 0 nothing is buffered.
	depth int
	err   error
}

const outChunk = 32 << 10

// outBuffers keeps the buffers of finished renders.
var outBuffers = sync.Pool{New: func() any { b := make([]byte, 0, 2*outChunk); return &b }}

func newOut(w io.Writer) *out {
	if direct, ok := w.(*bytes.Buffer); ok {
		return &out{w: w, direct: direct}
	}
	return &out{w: w, buf: (*outBuffers.Get().(*[]byte))[:0]}
}

// release returns the buffer after the outermost component; later writes
// outside a component go straight to w.
func (o *out) release() {
	if o.buf != nil && cap(o.buf) <= 1<<20 {
		buf := o.buf[:0]
		outBuffers.Put(&buf)
	}
	o.buf = nil
}

func (o *out) writeString(s string) error {
	if o.direct != nil {
		o.direct.WriteString(s)
		return nil
	}
	o.buf = append(o.buf, s...)
	if len(o.buf) >= outChunk || o.depth == 0 {
		return o.flush()
	}
	return o.err
}

func (o *out) write(p []byte) error {
	if o.direct != nil {
		o.direct.Write(p)
		return nil
	}
	o.buf = append(o.buf, p...)
	if len(o.buf) >= outChunk || o.depth == 0 {
		return o.flush()
	}
	return o.err
}

// flush writes the buffered bytes to w. The first error of w is kept and
// returned by every later write.
func (o *out) flush() error {
	if o.err == nil && len(o.buf) > 0 {
		_, o.err = o.w.Write(o.buf)
	}
	o.buf = o.buf[:0]
	return o.err
}

// StaticWriter makes a render write plain HTML: no block markers, no
// payload script (A-8.3).
func StaticWriter(w io.Writer) io.Writer {
	if sw, ok := w.(*streamWriter); ok {
		if sw.static {
			return sw
		}
		if sw.pending {
			return &streamWriter{out: sw.out, ctx: sw.ctx, static: true}
		}
	}
	return &streamWriter{out: newOut(w), static: true}
}

// BeginComponent registers a component instance. When w does not belong to
// a render yet, the component is the root: a new stream starts and root is
// true. The component's state entry is recorded (S-6.4): state holds the
// computed initial values (T-6.6d) and omitted lists bindings whose value
// the server could not compute. The caller writes the component
// range with OpenRange and CloseRange.
func BeginComponent(w io.Writer, component string, state any, omitted ...string) (io.Writer, bool) {
	sw, ok := w.(*streamWriter)
	root := !ok || sw.pending
	if !ok {
		sw = &streamWriter{out: newOut(w), stream: &Stream{}}
	} else if sw.pending {
		sw = &streamWriter{out: sw.out, ctx: sw.ctx, stream: &Stream{}}
	}
	sw.out.depth++
	if sw.static {
		return sw, root
	}
	if state == nil {
		if len(omitted) == 0 {
			// No serialized state: no entry (S-6.4, task 059).
			return sw, root
		}
		state = NewObject()
	}
	state, omitted = clientOnlyState(state, omitted)
	entry := []any{"s", component, state}
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
	if !sw.static && root {
		if err := writeRootPayload(sw.out, props, sw.stream); err != nil {
			return err
		}
	}
	// The outermost component, or the root even when a child did not end.
	if root || sw.out.depth == 1 {
		return sw.out.flush()
	}
	return sw.out.err
}

// LeaveComponent is deferred by every renderer after BeginComponent: the
// outermost one writes what is still buffered, also when the render
// returns an error (T-6.5c).
func LeaveComponent(w io.Writer) {
	if sw, ok := w.(*streamWriter); ok {
		if sw.out.depth--; sw.out.depth == 0 {
			sw.out.flush()
			sw.out.release()
		}
	}
}

// OpenRange writes the start marker of a block range with its state token
// (§5.1); CloseRange writes the end marker. Static renders write neither.
func OpenRange(w io.Writer, state string) error {
	if sw, ok := w.(*streamWriter); ok && sw.static {
		return nil
	}
	if state == "" {
		return WriteString(w, "<!--[-->")
	}
	// Three writes instead of a concatenated string per range.
	if err := WriteString(w, "<!--["); err != nil {
		return err
	}
	if err := WriteString(w, state); err != nil {
		return err
	}
	return WriteString(w, "-->")
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
func AwaitEntry(w io.Writer, site, state string, value, errValue any) string {
	token := "p"
	snapshot := map[string]any{}
	switch state {
	case "then":
		token = "t"
		snapshot["value"] = value
	case "catch":
		token = "c"
		snapshot["error"] = errValue
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
func writeRootPayload(o *out, props json.RawMessage, stream *Stream) error {
	entries := stream.entries
	if entries == nil {
		entries = []any{}
	}
	if len(props) == 0 {
		props = json.RawMessage("{}")
	}
	// The same bytes as json.Marshal of {v, props, entries}, in one pass,
	// appended to the render's output. props is the output of
	// MarshalHydrationPayload (appendJSON): compact, valid and escaped for a
	// script element already, so it is not re-validated (task 058).
	var b []byte
	if o.direct != nil {
		o.direct.Grow(len(props) + 4096)
		b = o.direct.AvailableBuffer()
	} else {
		b = o.buf
	}
	b = append(b, `<script type="application/json" data-vela-root>{"v":`...)
	b = strconv.AppendInt(b, int64(RootPayloadVersion), 10)
	b = append(b, `,"props":`...)
	b = append(b, props...)
	b = append(b, `,"entries":`...)
	b, err := appendJSON(b, entries)
	if err != nil {
		return err
	}
	b = append(b, `}</script>`...)
	if o.direct != nil {
		o.direct.Write(b)
		return nil
	}
	o.buf = b
	return o.err
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

// StaticRoot makes a static root component render plain HTML (A-8.1): a
// writer outside any render becomes a static writer, while a component
// rendered inside an interactive parent keeps the hydration markup.
func StaticRoot(w io.Writer) io.Writer {
	if sw, ok := w.(*streamWriter); ok && !sw.pending {
		return w
	}
	return StaticWriter(w)
}
