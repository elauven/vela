package ssr_runtime

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Built-ins of docs/SSR_TRANSLATION_MATRIX.md §2. Method calls dispatch on
// the receiver at run time, as JavaScript does; the compiler admits only
// the method names of its catalogue.

// Method calls receiver.name(args...).
func (js *JS) Method(site string, receiver any, name string, args ...any) any {
	if js.Failed() {
		return Undefined()
	}
	if nullish(receiver) {
		return js.throw("TypeError", fmt.Sprintf("Cannot read properties of %s (reading '%s')", nullishName(receiver), name), site)
	}
	switch v := jsValue(receiver).(type) {
	case string:
		if result, ok := js.stringMethod(site, v, name, args); ok {
			return result
		}
	case []any:
		if result, _, ok := js.arrayMethod(site, v, name, args); ok {
			return result
		}
	case float64:
		if result, ok := js.numberMethod(site, v, name, args); ok {
			return result
		}
	case bool:
		if name == "toString" {
			return String(v)
		}
		if name == "valueOf" {
			return v
		}
	case *Object:
		if fn, ok := property(v, name).(Func); ok {
			return fn(args...)
		}
	}
	return js.throw("TypeError", fmt.Sprintf("%s.%s is not a function", describe(jsValue(receiver)), name), site)
}

// MutateArray calls a method that changes an array the function created
// (push, pop, shift, unshift, splice, sort, reverse, fill) and returns the
// array's new contents with the method's result (T-6.6b).
func (js *JS) MutateArray(site string, array any, name string, args ...any) ([]any, any) {
	if js.Failed() {
		a, _ := array.([]any)
		return a, Undefined()
	}
	a, ok := array.([]any)
	if !ok {
		return a, js.throw("TypeError", fmt.Sprintf("%s.%s is not a function", describe(jsValue(array)), name), site)
	}
	result, out, _ := js.arrayMethod(site, a, name, args)
	if out == nil {
		out = a
	}
	return out, result
}

// ── Conversions and global functions (§2.1) ─────────────────────────────

// ToStringValue is String(value).
func ToStringValue(value any) string { return String(jsValue(value)) }

// ToBoolean is Boolean(value).
func ToBoolean(value any) bool { return Truthy(jsValue(value)) }

// ParseInt is parseInt(text, radix).
func ParseInt(text, radix any) float64 {
	s := strings.TrimLeftFunc(String(jsValue(text)), jsWhitespace)
	sign := 1.0
	if s != "" && (s[0] == '+' || s[0] == '-') {
		if s[0] == '-' {
			sign = -1
		}
		s = s[1:]
	}
	r := int(toInt32(ToNumber(radix)))
	strip := true
	if r != 0 {
		if r < 2 || r > 36 {
			return math.NaN()
		}
		strip = r == 16
	} else {
		r = 10
	}
	if strip && len(s) >= 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X') {
		s, r = s[2:], 16
	}
	end := 0
	for end < len(s) && digitValue(s[end]) < r {
		end++
	}
	if end == 0 {
		return math.NaN()
	}
	digits := s[:end]
	if r == 10 {
		value, _ := strconv.ParseFloat(digits, 64)
		return float64(sign * value)
	}
	value := 0.0
	for i := 0; i < len(digits); i++ {
		value = float64(value*float64(r)) + float64(digitValue(digits[i]))
	}
	return float64(sign * value)
}

func digitValue(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'z':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'Z':
		return int(c-'A') + 10
	}
	return 99
}

func toInt32(n float64) int32 {
	return int32(toUint32(n))
}

// ParseFloat is parseFloat(text): the longest decimal prefix.
func ParseFloat(text any) float64 {
	s := strings.TrimLeftFunc(String(jsValue(text)), jsWhitespace)
	rest := s
	sign := ""
	if rest != "" && (rest[0] == '+' || rest[0] == '-') {
		sign, rest = rest[:1], rest[1:]
	}
	if strings.HasPrefix(rest, "Infinity") {
		if sign == "-" {
			return math.Inf(-1)
		}
		return math.Inf(1)
	}
	i := 0
	digits := 0
	for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
		i, digits = i+1, digits+1
	}
	if i < len(rest) && rest[i] == '.' {
		i++
		for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
			i, digits = i+1, digits+1
		}
	}
	if digits == 0 {
		return math.NaN()
	}
	if i < len(rest) && (rest[i] == 'e' || rest[i] == 'E') {
		j := i + 1
		if j < len(rest) && (rest[j] == '+' || rest[j] == '-') {
			j++
		}
		k := j
		for k < len(rest) && rest[k] >= '0' && rest[k] <= '9' {
			k++
		}
		if k > j {
			i = k
		}
	}
	value, _ := strconv.ParseFloat(sign+strings.TrimSuffix(rest[:i], "."), 64)
	if rest[:i] == "." {
		return math.NaN()
	}
	return value
}

// IsNaN is the global isNaN, which converts its argument.
func IsNaN(value any) bool { return math.IsNaN(ToNumber(jsValue(value))) }

// IsFinite is the global isFinite, which converts its argument.
func IsFinite(value any) bool {
	n := ToNumber(jsValue(value))
	return !math.IsNaN(n) && !math.IsInf(n, 0)
}

// NumberIsNaN, NumberIsFinite, NumberIsInteger, and NumberIsSafeInteger
// do not convert their argument.
func NumberIsNaN(value any) bool {
	n, ok := jsValue(value).(float64)
	return ok && math.IsNaN(n)
}

func NumberIsFinite(value any) bool {
	n, ok := jsValue(value).(float64)
	return ok && !math.IsNaN(n) && !math.IsInf(n, 0)
}

func NumberIsInteger(value any) bool {
	n, ok := jsValue(value).(float64)
	return ok && !math.IsInf(n, 0) && math.Trunc(n) == n
}

func NumberIsSafeInteger(value any) bool {
	n, ok := jsValue(value).(float64)
	return NumberIsInteger(value) && ok && math.Abs(n) <= 9007199254740991
}

const uriUnreserved = "-_.!~*'()"
const uriReserved = ";/?:@&=+$,#"

// EncodeURIComponent and EncodeURI escape UTF-8 bytes as %XX; a lone
// surrogate throws URIError.
func (js *JS) EncodeURIComponent(site string, value any) any {
	return js.encodeURI(site, value, uriUnreserved)
}

func (js *JS) EncodeURI(site string, value any) any {
	return js.encodeURI(site, value, uriUnreserved+uriReserved)
}

func (js *JS) encodeURI(site string, value any, keep string) any {
	u := units(String(jsValue(value)))
	var b strings.Builder
	for i := 0; i < len(u); i++ {
		c := rune(u[i])
		if c >= 0xD800 && c <= 0xDFFF {
			if c > 0xDBFF || i+1 == len(u) || u[i+1] < 0xDC00 || u[i+1] > 0xDFFF {
				return js.throw("URIError", "URI malformed", site)
			}
			c = (c-0xD800)<<10 + (rune(u[i+1]) - 0xDC00) + 0x10000
			i++
		}
		if c < 0x80 && (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune(keep, c)) {
			b.WriteRune(c)
			continue
		}
		var buf [4]byte
		for _, octet := range buf[:utf8.EncodeRune(buf[:], c)] {
			fmt.Fprintf(&b, "%%%02X", octet)
		}
	}
	return b.String()
}

// DecodeURIComponent and DecodeURI reverse the escapes; decodeURI keeps
// the escapes of reserved characters. Malformed input throws URIError.
func (js *JS) DecodeURIComponent(site string, value any) any {
	return js.decodeURI(site, value, "")
}

func (js *JS) DecodeURI(site string, value any) any {
	return js.decodeURI(site, value, uriReserved)
}

func (js *JS) decodeURI(site string, value any, reserved string) any {
	s := String(jsValue(value))
	var b strings.Builder
	hex := func(i int) (byte, bool) {
		if i+3 > len(s) {
			return 0, false
		}
		n, err := strconv.ParseUint(s[i+1:i+3], 16, 8)
		return byte(n), err == nil
	}
	for i := 0; i < len(s); {
		if s[i] != '%' {
			b.WriteByte(s[i])
			i++
			continue
		}
		first, ok := hex(i)
		if !ok {
			return js.throw("URIError", "URI malformed", site)
		}
		if first < 0x80 {
			if strings.IndexByte(reserved, first) >= 0 {
				b.WriteString(s[i : i+3])
			} else {
				b.WriteByte(first)
			}
			i += 3
			continue
		}
		n := 0
		switch {
		case first&0xE0 == 0xC0:
			n = 2
		case first&0xF0 == 0xE0:
			n = 3
		case first&0xF8 == 0xF0:
			n = 4
		default:
			return js.throw("URIError", "URI malformed", site)
		}
		octets := []byte{first}
		for k := 1; k < n; k++ {
			j := i + 3*k
			if j >= len(s) || s[j] != '%' {
				return js.throw("URIError", "URI malformed", site)
			}
			octet, ok := hex(j)
			if !ok || octet&0xC0 != 0x80 {
				return js.throw("URIError", "URI malformed", site)
			}
			octets = append(octets, octet)
		}
		r, size := utf8.DecodeRune(octets)
		if r == utf8.RuneError || size != n {
			return js.throw("URIError", "URI malformed", site)
		}
		b.Write(octets)
		i += 3 * n
	}
	return b.String()
}

// ── Math (§2.5) ──────────────────────────────────────────────────────────

func num(value any) float64 { return ToNumber(jsValue(value)) }

// MathRound rounds ties toward +∞ and keeps -0 (T-6.4b).
func MathRound(value any) float64 {
	x := num(value)
	if math.IsNaN(x) || math.IsInf(x, 0) || x == 0 {
		return x
	}
	f := math.Floor(x)
	if x-f >= 0.5 {
		f++
	}
	if f == 0 && x < 0 {
		return math.Copysign(0, -1)
	}
	return f
}

func MathFloor(value any) float64 { return math.Floor(num(value)) }
func MathCeil(value any) float64  { return math.Ceil(num(value)) }
func MathTrunc(value any) float64 { return math.Trunc(num(value)) }
func MathAbs(value any) float64   { return math.Abs(num(value)) }
func MathSqrt(value any) float64  { return math.Sqrt(num(value)) }
func MathCbrt(value any) float64  { return fdlibmCbrt(num(value)) }

// MathSign keeps -0 and NaN.
func MathSign(value any) float64 {
	x := num(value)
	switch {
	case math.IsNaN(x) || x == 0:
		return x
	case x > 0:
		return 1
	}
	return -1
}

// MathMin and MathMax: any NaN gives NaN, -0 is below +0, no arguments
// give Infinity and -Infinity.
func MathMin(values ...any) float64 {
	result := math.Inf(1)
	nan := false
	for _, value := range values {
		x := num(value)
		if math.IsNaN(x) {
			nan = true
		} else if x < result || x == 0 && result == 0 && math.Signbit(x) {
			result = x
		}
	}
	if nan {
		return math.NaN()
	}
	return result
}

func MathMax(values ...any) float64 {
	result := math.Inf(-1)
	nan := false
	for _, value := range values {
		x := num(value)
		if math.IsNaN(x) {
			nan = true
		} else if x > result || x == 0 && result == 0 && !math.Signbit(x) {
			result = x
		}
	}
	if nan {
		return math.NaN()
	}
	return result
}

// MathHypot: an infinite argument wins over NaN.
func MathHypot(values ...any) float64 {
	numbers := make([]float64, len(values))
	largest := 0.0
	nan := false
	for i, value := range values {
		x := math.Abs(num(value))
		numbers[i] = x
		if math.IsInf(x, 1) {
			return math.Inf(1)
		}
		if math.IsNaN(x) {
			nan = true
		}
		largest = math.Max(largest, x)
	}
	if nan {
		return math.NaN()
	}
	if largest == 0 {
		return 0
	}
	sum, compensation := 0.0, 0.0
	for _, x := range numbers {
		scaled := x / largest
		term := float64(scaled*scaled) - compensation
		total := sum + term
		compensation = (total - sum) - term
		sum = total
	}
	return float64(math.Sqrt(sum) * largest)
}

// ── Number methods (§2.6) ────────────────────────────────────────────────

func (js *JS) numberMethod(site string, x float64, name string, args []any) (any, bool) {
	switch name {
	case "toString":
		if undefinedArg(args, 0) {
			return String(x), true
		}
		radix := toIntegerOrInfinity(args[0])
		if radix < 2 || radix > 36 {
			return js.throw("RangeError", "toString() radix must be between 2 and 36", site), true
		}
		if radix == 10 {
			return String(x), true
		}
		return radixString(x, int(radix)), true
	case "toPrecision":
		return js.toPrecision(site, x, Arg(args, 0)), true
	case "toExponential":
		return js.toExponential(site, x, Arg(args, 0)), true
	case "valueOf":
		return x, true
	case "toFixed":
		return js.toFixed(site, x, Arg(args, 0)), true
	}
	return nil, false
}

// toFixed rounds the exact binary value to d decimals, ties up (T-6.4b).
func (js *JS) toFixed(site string, x float64, digits any) any {
	d := toIntegerOrInfinity(digits)
	if d < 0 || d > 100 {
		return js.throw("RangeError", "toFixed() digits argument must be between 0 and 100", site)
	}
	if math.IsNaN(x) {
		return "NaN"
	}
	if math.Abs(x) >= 1e21 || math.IsInf(x, 0) {
		return String(x)
	}
	sign := ""
	if x < 0 {
		sign, x = "-", -x
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(d)), nil)
	value := new(big.Rat).SetFloat64(x)
	value.Mul(value, new(big.Rat).SetInt(scale))
	value.Add(value, big.NewRat(1, 2))
	n := new(big.Int).Quo(value.Num(), value.Denom())
	text := n.String()
	if d == 0 {
		return sign + text
	}
	if len(text) <= int(d) {
		text = strings.Repeat("0", int(d)-len(text)+1) + text
	}
	cut := len(text) - int(d)
	return sign + text[:cut] + "." + text[cut:]
}

// ── Object functions (§2.4) ──────────────────────────────────────────────

// ownKeys lists the own enumerable keys of a value in JavaScript order.
func (js *JS) ownKeys(site string, value any) ([]string, any, bool) {
	if nullish(value) {
		js.throw("TypeError", "Cannot convert undefined or null to object", site)
		return nil, nil, false
	}
	switch v := jsValue(value).(type) {
	case *Object:
		return v.Keys(), v, true
	case []any:
		keys := make([]string, 0, len(v))
		for i, item := range v {
			if !isHole(item) {
				keys = append(keys, strconv.Itoa(i))
			}
		}
		return keys, v, true
	case string:
		u := units(v)
		keys := make([]string, len(u))
		for i := range u {
			keys[i] = strconv.Itoa(i)
		}
		return keys, v, true
	}
	return nil, value, true
}

func (js *JS) ObjectKeys(site string, value any) any {
	keys, _, ok := js.ownKeys(site, value)
	if !ok {
		return Undefined()
	}
	out := make([]any, len(keys))
	for i, key := range keys {
		out[i] = key
	}
	return out
}

func (js *JS) ObjectValues(site string, value any) any {
	keys, source, ok := js.ownKeys(site, value)
	if !ok {
		return Undefined()
	}
	out := make([]any, len(keys))
	for i, key := range keys {
		out[i] = unhole(property(source, key))
	}
	return out
}

func (js *JS) ObjectEntries(site string, value any) any {
	keys, source, ok := js.ownKeys(site, value)
	if !ok {
		return Undefined()
	}
	out := make([]any, len(keys))
	for i, key := range keys {
		out[i] = []any{key, unhole(property(source, key))}
	}
	return out
}

// ObjectFromEntries builds an object from [key, value] entries; a later
// duplicate key keeps the first position.
func (js *JS) ObjectFromEntries(site string, entries any) any {
	items := js.SpreadItems(entries, site)
	if js.Failed() {
		return Undefined()
	}
	out := NewObject()
	for _, entry := range items {
		if nullish(entry) || TypeOf(entry) != "object" {
			return js.throw("TypeError", fmt.Sprintf("Iterator value %s is not an entry object", String(jsValue(entry))), site)
		}
		out.Set(propertyKey(js.Index(entry, 0.0, site)), js.Index(entry, 1.0, site))
	}
	return out
}

// ObjectAssign copies the own enumerable properties of sources into
// target, an object the expression created.
func (js *JS) ObjectAssign(site string, target any, sources ...any) any {
	object, ok := target.(*Object)
	if !ok {
		return js.throw("TypeError", "Object.assign target must be an object literal during server rendering", site)
	}
	for _, source := range sources {
		if nullish(source) {
			continue
		}
		keys, from, _ := js.ownKeys(site, source)
		for _, key := range keys {
			object.Set(key, unhole(property(from, key)))
		}
	}
	return object
}

// ObjectHasOwn is Object.hasOwn(value, key).
func (js *JS) ObjectHasOwn(site string, value, key any) any {
	keys, _, ok := js.ownKeys(site, value)
	if !ok {
		return Undefined()
	}
	name := propertyKey(key)
	switch jsValue(value).(type) {
	case []any, string:
		if name == "length" {
			return true
		}
	}
	for _, own := range keys {
		if own == name {
			return true
		}
	}
	return false
}

// ── Array functions (§2.3) ───────────────────────────────────────────────

// IsArray is Array.isArray.
func IsArray(value any) bool {
	_, ok := jsValue(value).([]any)
	return ok
}

// ArrayOf is Array.of.
func ArrayOf(values ...any) []any { return append([]any{}, values...) }

// ArrayFrom is Array.from(source, mapFn): strings by code point, arrays
// with holes read as undefined, and array-like objects by length.
func (js *JS) ArrayFrom(site string, source any, mapFn any) any {
	if nullish(source) {
		return js.throw("TypeError", fmt.Sprintf("%s is not iterable (cannot read property Symbol(Symbol.iterator))", nullishName(source)), site)
	}
	var items []any
	switch v := jsValue(source).(type) {
	case string:
		items = codePoints(v)
	case []any:
		items = unholed(append([]any{}, v...))
	case *Object:
		length := toIntegerOrInfinity(property(v, "length"))
		for i := 0; i < int(math.Min(length, 1<<24)); i++ {
			items = append(items, property(v, strconv.Itoa(i)))
		}
	}
	if items == nil {
		items = []any{}
	}
	if _, undefined := mapFn.(UndefinedValue); undefined || mapFn == nil {
		return items
	}
	fn, ok := js.callable(site, "from", mapFn)
	if !ok {
		return Undefined()
	}
	for i, item := range items {
		items[i] = fn(item, float64(i))
		if js.Failed() {
			return Undefined()
		}
	}
	return items
}

// ── JSON (§2.7) ──────────────────────────────────────────────────────────

// JSONStringify is JSON.stringify(value, null, indent); the result is
// undefined for undefined and functions.
func (js *JS) JSONStringify(site string, value, replacer, indent any) any {
	if !nullish(replacer) {
		return js.throw("TypeError", "JSON.stringify with a replacer is not supported during server rendering", site)
	}
	gap := ""
	switch v := jsValue(indent).(type) {
	case float64:
		gap = strings.Repeat(" ", int(math.Max(0, math.Min(10, toIntegerOrInfinity(v)))))
	case string:
		u := units(v)
		gap = fromUnits(u[:min(10, len(u))])
	}
	var b strings.Builder
	if !js.stringify(site, &b, value, gap, "", map[any]bool{}) {
		return Undefined()
	}
	return b.String()
}

// stringify writes value; it returns false for a value JSON omits.
func (js *JS) stringify(site string, b *strings.Builder, value any, gap, indent string, seen map[any]bool) bool {
	if js.Failed() {
		return false
	}
	switch v := jsValue(value).(type) {
	case nil:
		b.WriteString("null")
	case UndefinedValue:
		return false
	case bool:
		b.WriteString(strconv.FormatBool(v))
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			b.WriteString("null")
		} else {
			b.WriteString(String(v))
		}
	case string:
		quoteJSON(b, v)
	case Func:
		return false
	case *Promise:
		b.WriteString("{}")
	case []any:
		key := fmt.Sprintf("%p", v)
		if len(v) > 0 && seen[key] {
			js.throw("TypeError", "Converting circular structure to JSON", site)
			return false
		}
		seen[key] = true
		defer delete(seen, key)
		if len(v) == 0 {
			b.WriteString("[]")
			return true
		}
		inner := indent + gap
		b.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				b.WriteByte(',')
			}
			if gap != "" {
				b.WriteString("\n" + inner)
			}
			if !js.stringify(site, b, item, gap, inner, seen) {
				if js.Failed() {
					return false
				}
				b.WriteString("null")
			}
		}
		if gap != "" {
			b.WriteString("\n" + indent)
		}
		b.WriteByte(']')
	case *Object:
		if seen[v] {
			js.throw("TypeError", "Converting circular structure to JSON", site)
			return false
		}
		seen[v] = true
		defer delete(seen, v)
		inner := indent + gap
		wrote := false
		b.WriteByte('{')
		keys, vals := v.entries()
		for i, key := range keys {
			var member strings.Builder
			if !js.stringify(site, &member, vals[i], gap, inner, seen) {
				if js.Failed() {
					return false
				}
				continue
			}
			if wrote {
				b.WriteByte(',')
			}
			if gap != "" {
				b.WriteString("\n" + inner)
			}
			quoteJSON(b, key)
			b.WriteByte(':')
			if gap != "" {
				b.WriteByte(' ')
			}
			b.WriteString(member.String())
			wrote = true
		}
		if wrote && gap != "" {
			b.WriteString("\n" + indent)
		}
		b.WriteByte('}')
	default:
		return js.stringify(site, b, jsonView(v), gap, indent, seen)
	}
	return true
}

// quoteJSON quotes a string as JSON.stringify does: no HTML escaping, and
// lone surrogates as \u escapes.
func quoteJSON(b *strings.Builder, s string) {
	b.WriteByte('"')
	u := units(s)
	for i := 0; i < len(u); i++ {
		c := u[i]
		switch {
		case c == '"':
			b.WriteString(`\"`)
		case c == '\\':
			b.WriteString(`\\`)
		case c == '\b':
			b.WriteString(`\b`)
		case c == '\f':
			b.WriteString(`\f`)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\r':
			b.WriteString(`\r`)
		case c == '\t':
			b.WriteString(`\t`)
		case c < 0x20:
			fmt.Fprintf(b, `\u%04x`, c)
		case c >= 0xD800 && c <= 0xDBFF && i+1 < len(u) && u[i+1] >= 0xDC00 && u[i+1] <= 0xDFFF:
			b.WriteString(fromUnits(u[i : i+2]))
			i++
		case c >= 0xD800 && c <= 0xDFFF:
			fmt.Fprintf(b, `\u%04x`, c)
		default:
			b.WriteString(fromUnits(u[i : i+1]))
		}
	}
	b.WriteByte('"')
}

// JSONParse is JSON.parse(text): objects keep their key order as *Object;
// invalid text throws SyntaxError.
func (js *JS) JSONParse(site string, text any) any {
	decoder := json.NewDecoder(strings.NewReader(String(jsValue(text))))
	decoder.UseNumber()
	value, err := parseJSONValue(decoder)
	if err == nil {
		if _, extra := decoder.Token(); extra != io.EOF {
			err = fmt.Errorf("unexpected non-whitespace character after JSON")
		}
	}
	if err != nil {
		return js.throw("SyntaxError", "JSON.parse: "+err.Error(), site)
	}
	return value
}

func parseJSONValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("unexpected end of JSON input")
		}
		return nil, err
	}
	switch t := token.(type) {
	case json.Delim:
		switch t {
		case '[':
			out := []any{}
			for decoder.More() {
				item, err := parseJSONValue(decoder)
				if err != nil {
					return nil, err
				}
				out = append(out, item)
			}
			_, err := decoder.Token()
			return out, err
		case '{':
			out := NewObject()
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				value, err := parseJSONValue(decoder)
				if err != nil {
					return nil, err
				}
				out.Set(key.(string), value)
			}
			_, err := decoder.Token()
			return out, err
		}
		return nil, fmt.Errorf("unexpected token %v", t)
	case json.Number:
		// Out-of-range numbers are ±Infinity, as in JavaScript.
		value, _ := strconv.ParseFloat(string(t), 64)
		return value, nil
	case string, bool, nil:
		return t, nil
	}
	return nil, fmt.Errorf("unexpected token %v", token)
}

// GlobalFunc is a global function used as a value, as in
// items.filter(Boolean) or values.map(String).
func (js *JS) GlobalFunc(site, name string) Func {
	one := func(f func(any) any) Func {
		return func(args ...any) any { return f(Arg(args, 0)) }
	}
	switch name {
	case "String":
		return func(args ...any) any {
			if len(args) == 0 {
				return ""
			}
			return ToStringValue(args[0])
		}
	case "Number":
		return func(args ...any) any {
			if len(args) == 0 {
				return 0.0
			}
			return ToNumber(jsValue(args[0]))
		}
	case "Boolean":
		return one(func(v any) any { return ToBoolean(v) })
	case "parseInt":
		return func(args ...any) any { return ParseInt(Arg(args, 0), Arg(args, 1)) }
	case "parseFloat":
		return one(func(v any) any { return ParseFloat(v) })
	case "isNaN":
		return one(func(v any) any { return IsNaN(v) })
	case "isFinite":
		return one(func(v any) any { return IsFinite(v) })
	case "encodeURIComponent":
		return one(func(v any) any { return js.EncodeURIComponent(site, v) })
	case "encodeURI":
		return one(func(v any) any { return js.EncodeURI(site, v) })
	case "decodeURIComponent":
		return one(func(v any) any { return js.DecodeURIComponent(site, v) })
	case "decodeURI":
		return one(func(v any) any { return js.DecodeURI(site, v) })
	}
	return func(...any) any { return js.throw("TypeError", name+" is not a function", site) }
}
