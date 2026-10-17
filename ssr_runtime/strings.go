package ssr_runtime

import (
	"fmt"
	"math"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// String built-ins (docs/SSR_TRANSLATION_MATRIX.md §2.2). JavaScript
// strings are UTF-16: positions and lengths count UTF-16 code units
// (T-6.3a). Go strings hold UTF-8; a lone surrogate an operation produces
// (cutting a pair) is kept as its three-byte WTF-8 sequence so that later
// operations see the same unit, and it is written to HTML as U+FFFD
// (T-6.3b).

// units returns the UTF-16 code units of s.
func units(s string) []uint16 {
	out := make([]uint16, 0, len(s))
	for i := 0; i < len(s); {
		// WTF-8 surrogate: ED A0..BF xx.
		if s[i] == 0xED && i+2 < len(s) && s[i+1] >= 0xA0 && s[i+1] <= 0xBF && s[i+2]&0xC0 == 0x80 {
			out = append(out, uint16(0xD000|uint16(s[i+1]&0x3F)<<6|uint16(s[i+2]&0x3F)))
			i += 3
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		if r >= 0x10000 {
			high, low := utf16.EncodeRune(r)
			out = append(out, uint16(high), uint16(low))
			continue
		}
		out = append(out, uint16(r))
	}
	return out
}

// fromUnits encodes UTF-16 code units; surrogate pairs become one code
// point and lone surrogates keep their WTF-8 form.
func fromUnits(u []uint16) string {
	var b strings.Builder
	b.Grow(len(u))
	for i := 0; i < len(u); i++ {
		c := rune(u[i])
		if c >= 0xD800 && c <= 0xDBFF && i+1 < len(u) && u[i+1] >= 0xDC00 && u[i+1] <= 0xDFFF {
			b.WriteRune(utf16.DecodeRune(c, rune(u[i+1])))
			i++
			continue
		}
		if c >= 0xD800 && c <= 0xDFFF {
			b.WriteByte(byte(0xE0 | c>>12))
			b.WriteByte(byte(0x80 | (c>>6)&0x3F))
			b.WriteByte(byte(0x80 | c&0x3F))
			continue
		}
		b.WriteRune(c)
	}
	return b.String()
}

// WellFormed replaces lone surrogates with U+FFFD and joins surrogate
// halves that concatenation placed next to each other (T-6.3b).
func WellFormed(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	u := units(s)
	runes := utf16.Decode(u)
	return string(runes)
}

// toIntegerOrInfinity is ToIntegerOrInfinity: NaN is 0, others truncate.
func toIntegerOrInfinity(value any) float64 {
	n := ToNumber(value)
	if math.IsNaN(n) {
		return 0
	}
	if math.IsInf(n, 0) {
		return n
	}
	return math.Trunc(n)
}

// relative resolves a relative index argument against length: negative
// values count from the end, the result is clamped to [0, length].
func relative(value any, length int) int {
	n := toIntegerOrInfinity(value)
	if n < 0 {
		n += float64(length)
		if n < 0 {
			return 0
		}
		return int(n)
	}
	if n > float64(length) {
		return length
	}
	return int(n)
}

// clamp converts a position argument to [0, length].
func clamp(value any, length int) int {
	n := toIntegerOrInfinity(value)
	if n < 0 {
		return 0
	}
	if n > float64(length) {
		return length
	}
	return int(n)
}

func undefinedArg(args []any, i int) bool {
	if i >= len(args) {
		return true
	}
	_, undefined := args[i].(UndefinedValue)
	return undefined
}

func indexUnits(haystack, needle []uint16, from int) int {
	for i := from; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func lastIndexUnits(haystack, needle []uint16, from int) int {
	if from > len(haystack)-len(needle) {
		from = len(haystack) - len(needle)
	}
	for i := from; i >= 0; i-- {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// stringMethod calls method name of string s.
func (js *JS) stringMethod(site, s, name string, args []any) (any, bool) {
	arg := func(i int) any { return Arg(args, i) }
	text := func(i int) string { return String(jsValue(arg(i))) }
	u := units(s)
	switch name {
	case "toString", "valueOf":
		return s, true
	case "at":
		k := toIntegerOrInfinity(arg(0))
		if k < 0 {
			k += float64(len(u))
		}
		if k < 0 || k >= float64(len(u)) {
			return Undefined(), true
		}
		return fromUnits(u[int(k) : int(k)+1]), true
	case "charAt":
		k := toIntegerOrInfinity(arg(0))
		if k < 0 || k >= float64(len(u)) {
			return "", true
		}
		return fromUnits(u[int(k) : int(k)+1]), true
	case "charCodeAt":
		k := toIntegerOrInfinity(arg(0))
		if k < 0 || k >= float64(len(u)) {
			return math.NaN(), true
		}
		return float64(u[int(k)]), true
	case "codePointAt":
		k := toIntegerOrInfinity(arg(0))
		if k < 0 || k >= float64(len(u)) {
			return Undefined(), true
		}
		i := int(k)
		c := u[i]
		if c >= 0xD800 && c <= 0xDBFF && i+1 < len(u) && u[i+1] >= 0xDC00 && u[i+1] <= 0xDFFF {
			return float64(utf16.DecodeRune(rune(c), rune(u[i+1]))), true
		}
		return float64(c), true
	case "includes":
		return indexUnits(u, units(text(0)), clamp(arg(1), len(u))) >= 0, true
	case "startsWith", "endsWith":
		search := units(text(0))
		if name == "startsWith" {
			start := clamp(arg(1), len(u))
			return start+len(search) <= len(u) && indexUnits(u[start:start+len(search)], search, 0) == 0, true
		}
		end := len(u)
		if !undefinedArg(args, 1) {
			end = clamp(arg(1), len(u))
		}
		start := end - len(search)
		return start >= 0 && indexUnits(u[start:end], search, 0) == 0, true
	case "indexOf":
		return float64(indexUnits(u, units(text(0)), clamp(arg(1), len(u)))), true
	case "lastIndexOf":
		from := len(u)
		if n := ToNumber(arg(1)); !math.IsNaN(n) {
			from = clamp(n, len(u))
		}
		return float64(lastIndexUnits(u, units(text(0)), from)), true
	case "slice":
		start := relative(arg(0), len(u))
		end := len(u)
		if !undefinedArg(args, 1) {
			end = relative(arg(1), len(u))
		}
		if start >= end {
			return "", true
		}
		return fromUnits(u[start:end]), true
	case "substring":
		start := clamp(arg(0), len(u))
		end := len(u)
		if !undefinedArg(args, 1) {
			end = clamp(arg(1), len(u))
		}
		if start > end {
			start, end = end, start
		}
		return fromUnits(u[start:end]), true
	case "toUpperCase":
		return caseMap(s, upperCase, false), true
	case "toLowerCase":
		return caseMap(s, lowerCase, true), true
	case "trim":
		return strings.TrimFunc(s, jsWhitespace), true
	case "trimStart":
		return strings.TrimLeftFunc(s, jsWhitespace), true
	case "trimEnd":
		return strings.TrimRightFunc(s, jsWhitespace), true
	case "padStart", "padEnd":
		target := toIntegerOrInfinity(arg(0))
		filler := " "
		if !undefinedArg(args, 1) {
			filler = text(1)
		}
		if target <= float64(len(u)) || filler == "" {
			return s, true
		}
		if target > 1<<29 {
			return js.throw("RangeError", "Invalid string length", site), true
		}
		fill := units(filler)
		pad := make([]uint16, 0, int(target)-len(u))
		for len(pad) < int(target)-len(u) {
			pad = append(pad, fill[:min(len(fill), int(target)-len(u)-len(pad))]...)
		}
		if name == "padStart" {
			return fromUnits(pad) + s, true
		}
		return s + fromUnits(pad), true
	case "repeat":
		n := toIntegerOrInfinity(arg(0))
		if n < 0 || math.IsInf(n, 1) {
			return js.throw("RangeError", fmt.Sprintf("Invalid count value: %s", String(ToNumber(arg(0)))), site), true
		}
		if float64(n*float64(len(u))) > 1<<29 {
			return js.throw("RangeError", "Invalid string length", site), true
		}
		return strings.Repeat(s, int(n)), true
	case "concat":
		var b strings.Builder
		b.WriteString(s)
		for i := range args {
			b.WriteString(text(i))
		}
		return b.String(), true
	case "split":
		return js.split(s, u, args), true
	case "replace", "replaceAll":
		return js.replace(site, s, u, name == "replaceAll", args), true
	case "normalize":
		return js.normalize(site, s, args), true
	}
	return nil, false
}

// split is String.prototype.split with a string separator.
func (js *JS) split(s string, u []uint16, args []any) any {
	limit := uint32(math.MaxUint32)
	if !undefinedArg(args, 1) {
		limit = toUint32(ToNumber(args[1]))
	}
	out := []any{}
	if limit == 0 {
		return out
	}
	if undefinedArg(args, 0) {
		return []any{s}
	}
	sep := units(String(jsValue(args[0])))
	if len(sep) == 0 {
		for i := 0; i < len(u) && uint32(len(out)) < limit; i++ {
			out = append(out, fromUnits(u[i:i+1]))
		}
		return out
	}
	start := 0
	for {
		i := indexUnits(u, sep, start)
		if i < 0 {
			break
		}
		out = append(out, fromUnits(u[start:i]))
		if uint32(len(out)) == limit {
			return out
		}
		start = i + len(sep)
	}
	return append(out, fromUnits(u[start:]))
}

func toUint32(n float64) uint32 {
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return 0
	}
	return uint32(int64(math.Mod(math.Trunc(n), 4294967296)))
}

// replace is String.prototype.replace and replaceAll with a string
// pattern: a function replacement receives (match, position, string); a
// string replacement expands $$, $&, $` and $' (GetSubstitution).
func (js *JS) replace(site, s string, u []uint16, all bool, args []any) any {
	pattern := units(String(jsValue(Arg(args, 0))))
	replacement := Arg(args, 1)
	fn, isFunc := replacement.(Func)
	template := ""
	if !isFunc {
		template = String(jsValue(replacement))
	}
	var positions []int
	for from := 0; from <= len(u); {
		i := indexUnits(u, pattern, from)
		if i < 0 {
			break
		}
		positions = append(positions, i)
		if !all {
			break
		}
		from = i + max(1, len(pattern))
	}
	if len(positions) == 0 {
		return s
	}
	var b strings.Builder
	last := 0
	for _, p := range positions {
		b.WriteString(fromUnits(u[last:p]))
		matched := fromUnits(u[p : p+len(pattern)])
		if isFunc {
			result := fn(matched, float64(p), s)
			if js.Failed() {
				return Undefined()
			}
			b.WriteString(String(jsValue(result)))
		} else {
			b.WriteString(substitute(template, matched, u, p, p+len(pattern)))
		}
		last = p + len(pattern)
	}
	b.WriteString(fromUnits(u[last:]))
	return b.String()
}

// substitute expands a replacement template for a string pattern.
func substitute(template, matched string, u []uint16, start, end int) string {
	if !strings.Contains(template, "$") {
		return template
	}
	var b strings.Builder
	for i := 0; i < len(template); i++ {
		if template[i] != '$' || i+1 == len(template) {
			b.WriteByte(template[i])
			continue
		}
		switch template[i+1] {
		case '$':
			b.WriteByte('$')
		case '&':
			b.WriteString(matched)
		case '`':
			b.WriteString(fromUnits(u[:start]))
		case '\'':
			b.WriteString(fromUnits(u[end:]))
		default:
			b.WriteByte('$')
			continue
		}
		i++
	}
	return b.String()
}

// caseMap applies a full case mapping table; lower applies the
// context-sensitive final sigma rule of SpecialCasing.
func caseMap(s string, table map[rune]string, lower bool) string {
	var b strings.Builder
	b.Grow(len(s))
	// Code points from UTF-16 units: a lone surrogate (WTF-8) stays one
	// unit, which no table maps.
	var runes []rune
	u := units(s)
	for i := 0; i < len(u); i++ {
		if u[i] >= 0xD800 && u[i] <= 0xDBFF && i+1 < len(u) && u[i+1] >= 0xDC00 && u[i+1] <= 0xDFFF {
			runes = append(runes, utf16.DecodeRune(rune(u[i]), rune(u[i+1])))
			i++
			continue
		}
		runes = append(runes, rune(u[i]))
	}
	for i, r := range runes {
		if r >= 0xD800 && r <= 0xDFFF {
			b.WriteString(fromUnits([]uint16{uint16(r)}))
			continue
		}
		if lower && r == 0x3A3 && finalSigma(runes, i) {
			b.WriteRune(0x3C2)
			continue
		}
		if mapped, ok := table[r]; ok {
			b.WriteString(mapped)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// finalSigma: a cased letter (skipping case-ignorable ones) precedes and
// none follows.
func finalSigma(runes []rune, i int) bool {
	before := false
	for j := i - 1; j >= 0; j-- {
		if caseIgnorable(runes[j]) {
			continue
		}
		before = cased(runes[j])
		break
	}
	if !before {
		return false
	}
	for j := i + 1; j < len(runes); j++ {
		if caseIgnorable(runes[j]) {
			continue
		}
		return !cased(runes[j])
	}
	return true
}

func cased(r rune) bool {
	_, upper := upperCase[r]
	_, lower := lowerCase[r]
	return upper || lower || unicode.In(r, unicode.Lt, unicode.Other_Lowercase, unicode.Other_Uppercase)
}

// caseIgnorable approximates Case_Ignorable: marks, format characters,
// modifier letters and symbols, and the word-internal punctuation of
// Word_Break MidLetter and MidNumLet.
func caseIgnorable(r rune) bool {
	switch r {
	case '\'', '.', ':', 0xB7, 0x387, 0x55F, 0x5F4, 0x2018, 0x2019, 0x2024, 0x2027, 0xFE13, 0xFE52, 0xFE55, 0xFF07, 0xFF0E, 0xFF1A:
		return true
	}
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk)
}

// codePoints splits a string into its code points, as spread and for…of
// iterate it; a lone surrogate is one item.
func codePoints(s string) []any {
	u := units(s)
	items := make([]any, 0, len(u))
	for i := 0; i < len(u); i++ {
		if u[i] >= 0xD800 && u[i] <= 0xDBFF && i+1 < len(u) && u[i+1] >= 0xDC00 && u[i+1] <= 0xDFFF {
			items = append(items, fromUnits(u[i:i+2]))
			i++
			continue
		}
		items = append(items, fromUnits(u[i:i+1]))
	}
	return items
}
