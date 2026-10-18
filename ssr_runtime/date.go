package ssr_runtime

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Deterministic Date (docs/SSR_TRANSLATION_CONTRACT.md T-6.6e, task 057).
// A Date holds a time value: milliseconds since the epoch in UTC, NaN for
// an invalid date, within ±8.64e15 (TimeClip). Everything computed here is
// UTC; what depends on the time zone of the machine (local getters and
// setters, toString, a date-time string without an offset) is not
// translated: it fails the render with an UnsupportedError, which catch
// does not receive, because the browser computes it in another zone.

// Date is a JavaScript Date object created by translated code.
type Date struct {
	time float64
	js   *JS
}

// UnsupportedError is an operation that has no deterministic server
// translation and is only known to be reached at run time. It is not a
// JavaScript exception: catch does not receive it.
type UnsupportedError struct {
	Message string
	Site    string
}

func (e *UnsupportedError) Error() string {
	text := "VELA-SSR-UNSUPPORTED: " + e.Message
	if e.Site != "" {
		return e.Site + ": " + text
	}
	return text
}

func (js *JS) unsupported(message, site string) UndefinedValue {
	return js.fail(&UnsupportedError{Message: message, Site: site})
}

const (
	msPerDay    = 86400000.0
	maxTimeClip = 8.64e15
)

// timeClip is TimeClip: NaN beyond ±8.64e15, integral otherwise (-0 is +0).
func timeClip(t float64) float64 {
	if math.IsNaN(t) || math.IsInf(t, 0) || math.Abs(t) > maxTimeClip {
		return math.NaN()
	}
	return math.Trunc(t) + 0
}

func day(t float64) float64 { return math.Floor(t / msPerDay) }

func timeWithinDay(t float64) float64 {
	r := math.Mod(t, msPerDay)
	if r < 0 {
		r += msPerDay
	}
	return r
}

func daysInYear(y float64) float64 {
	if math.Mod(y, 4) != 0 {
		return 365
	}
	if math.Mod(y, 100) != 0 {
		return 366
	}
	if math.Mod(y, 400) != 0 {
		return 365
	}
	return 366
}

func dayFromYear(y float64) float64 {
	// float64(…) keeps each product rounded (no fused multiply-add).
	return float64(365*(y-1970)) + math.Floor((y-1969)/4) - math.Floor((y-1901)/100) + math.Floor((y-1601)/400)
}

func timeFromYear(y float64) float64 { return float64(msPerDay * dayFromYear(y)) }

func yearFromTime(t float64) float64 {
	y := math.Floor(t/float64(msPerDay*365.2425)) + 1970
	for timeFromYear(y) > t {
		y--
	}
	for timeFromYear(y+1) <= t {
		y++
	}
	return y
}

// monthStarts are the first day within the year of each month, without
// and with a leap day.
var monthStarts = [2][13]float64{
	{0, 31, 59, 90, 120, 151, 181, 212, 243, 273, 304, 334, 365},
	{0, 31, 60, 91, 121, 152, 182, 213, 244, 274, 305, 335, 366},
}

func leap(y float64) int {
	if daysInYear(y) == 366 {
		return 1
	}
	return 0
}

// yearMonthDate splits a time value into year, month (0–11) and date.
func yearMonthDate(t float64) (float64, float64, float64) {
	y := yearFromTime(t)
	within := day(t) - dayFromYear(y)
	starts := monthStarts[leap(y)]
	m := 0
	for m < 11 && within >= starts[m+1] {
		m++
	}
	return y, float64(m), within - starts[m] + 1
}

// makeTime, makeDay and makeDate are the abstract operations of the same
// names: arguments that are not finite give NaN, fractions are truncated.
func makeTime(h, m, s, ms float64) float64 {
	for _, v := range []float64{h, m, s, ms} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return math.NaN()
		}
	}
	return float64(math.Trunc(h)*3600000) + float64(math.Trunc(m)*60000) + float64(math.Trunc(s)*1000) + math.Trunc(ms)
}

func makeDay(year, month, date float64) float64 {
	for _, v := range []float64{year, month, date} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return math.NaN()
		}
	}
	y, m, dt := math.Trunc(year), math.Trunc(month), math.Trunc(date)
	ym := y + math.Floor(m/12)
	if math.Abs(ym) > 400000 {
		return math.NaN()
	}
	mn := math.Mod(m, 12)
	if mn < 0 {
		mn += 12
	}
	return dayFromYear(ym) + monthStarts[leap(ym)][int(mn)] + dt - 1
}

func makeDate(day, time float64) float64 {
	if math.IsNaN(day) || math.IsInf(day, 0) || math.IsNaN(time) || math.IsInf(time, 0) {
		return math.NaN()
	}
	t := float64(day*msPerDay) + time
	if math.IsInf(t, 0) {
		return math.NaN()
	}
	return t
}

// dateArgs converts the arguments of Date.UTC and the setters: missing
// ones keep their defaults.
func dateArgs(args []any, defaults []float64) []float64 {
	out := append([]float64{}, defaults...)
	for i := range out {
		if i < len(args) {
			out[i] = ToNumber(jsValue(args[i]))
		}
	}
	return out
}

// DateUTC is Date.UTC(year, month, date, hours, minutes, seconds, ms).
func DateUTC(args ...any) float64 {
	v := dateArgs(args, []float64{math.NaN(), 0, 1, 0, 0, 0, 0})
	y := v[0]
	if !math.IsNaN(y) {
		if yi := math.Trunc(y); yi >= 0 && yi <= 99 {
			y = 1900 + yi
		}
	}
	return timeClip(makeDate(makeDay(y, v[1], v[2]), makeTime(v[3], v[4], v[5], v[6])))
}

// NewDate is new Date(value) and new Date(): a number is a time value, a
// Date is copied, a string is parsed (Date.parse), and other values are
// converted with ToPrimitive first. new Date() reads the clock and is
// rejected by the compiler.
func (js *JS) NewDate(site string, value any) *Date {
	if d, ok := value.(*Date); ok {
		return &Date{time: d.time, js: js}
	}
	primitive := toPrimitive(value)
	if js.Failed() {
		return &Date{time: math.NaN(), js: js}
	}
	if text, ok := primitive.(string); ok {
		return &Date{time: js.parseDate(site, text), js: js}
	}
	return &Date{time: timeClip(ToNumber(primitive)), js: js}
}

// DateParse is Date.parse(text).
func (js *JS) DateParse(site string, text any) float64 {
	s := String(toPrimitiveHint(text, "string"))
	if js.Failed() {
		return math.NaN()
	}
	return js.parseDate(site, s)
}

// parseDate accepts the date time string format of ECMAScript
// (YYYY, YYYY-MM, YYYY-MM-DD, ±YYYYYY…, then THH:mm, THH:mm:ss or
// THH:mm:ss.sss and Z or ±HH:mm). A date-only form is UTC. A date-time
// without an offset is local time and any other text is parsed by rules of
// the JavaScript engine: both fail the render as unsupported.
func (js *JS) parseDate(site, text string) float64 {
	t, ok, local := parseISODate(text)
	switch {
	case local:
		js.unsupported(fmt.Sprintf("the date-time %q has no offset, so JavaScript reads it in the browser's time zone; add Z or an offset such as +02:00", text), site)
		return math.NaN()
	case !ok:
		js.unsupported(fmt.Sprintf("the date %q is not in the ECMAScript date time string format (2026-10-18T09:30:00Z); other formats are parsed by rules of the JavaScript engine", text), site)
		return math.NaN()
	}
	return t
}

// parseISODate parses the ECMAScript date time string format. ok is false
// for other text; local is set for a date-time without an offset. A string
// in the format with a value out of range (month 13, hour 25, year
// -000000) is a valid parse of NaN.
func parseISODate(s string) (t float64, ok, local bool) {
	p := 0
	digits := func(n int) (float64, bool) {
		if p+n > len(s) {
			return 0, false
		}
		v := 0
		for i := 0; i < n; i++ {
			c := s[p+i]
			if c < '0' || c > '9' {
				return 0, false
			}
			v = v*10 + int(c-'0')
		}
		p += n
		return float64(v), true
	}
	expect := func(c byte) bool {
		if p < len(s) && s[p] == c {
			p++
			return true
		}
		return false
	}
	var year float64
	if p < len(s) && (s[p] == '+' || s[p] == '-') {
		negative := s[p] == '-'
		p++
		y, good := digits(6)
		if !good {
			return 0, false, false
		}
		if negative && y == 0 {
			// -000000 is not in the format; V8 reads it by other rules.
			return 0, false, false
		}
		if negative {
			y = -y
		}
		year = y
	} else {
		y, good := digits(4)
		if !good {
			return 0, false, false
		}
		year = y
	}
	month, date := 1.0, 1.0
	if expect('-') {
		m, good := digits(2)
		if !good {
			return 0, false, false
		}
		month = m
		if expect('-') {
			d, good := digits(2)
			if !good {
				return 0, false, false
			}
			date = d
		}
	}
	hours, minutes, seconds, ms := 0.0, 0.0, 0.0, 0.0
	offset := 0.0
	if expect('T') {
		h, good := digits(2)
		if !good || !expect(':') {
			return 0, false, false
		}
		m, good := digits(2)
		if !good {
			return 0, false, false
		}
		hours, minutes = h, m
		if expect(':') {
			sec, good := digits(2)
			if !good {
				return 0, false, false
			}
			seconds = sec
			if expect('.') {
				start := p
				for p < len(s) && s[p] >= '0' && s[p] <= '9' {
					p++
				}
				if p == start {
					return 0, false, false
				}
				// Digits after the third are ignored, as V8 does.
				fraction := s[start:p] + "00"
				v, _ := strconv.Atoi(fraction[:3])
				ms = float64(v)
			}
		}
		switch {
		case expect('Z'):
		case p < len(s) && (s[p] == '+' || s[p] == '-'):
			sign := 1.0
			if s[p] == '-' {
				sign = -1
			}
			p++
			oh, good := digits(2)
			if !good || !expect(':') {
				return 0, false, false
			}
			om, good := digits(2)
			if !good {
				return 0, false, false
			}
			if oh > 23 || om > 59 {
				t = math.NaN()
			}
			offset = sign * float64((float64(oh*60)+om)*60000)
		default:
			local = true
		}
	}
	if p != len(s) {
		return 0, false, false
	}
	if local {
		return 0, true, true
	}
	// As V8: a day up to 31 in any month (February 30 is March 1 or 2).
	if math.IsNaN(t) || month < 1 || month > 12 || date < 1 || date > 31 || hours > 24 || minutes > 59 || seconds > 59 {
		return math.NaN(), true, false
	}
	if hours == 24 && (minutes != 0 || seconds != 0 || ms != 0) {
		return math.NaN(), true, false
	}
	return timeClip(makeDate(makeDay(year, month-1, date), makeTime(hours, minutes, seconds, ms)) - offset), true, false
}

// isoString is Date.prototype.toISOString of a finite time value.
func isoString(t float64) string {
	y, m, d := yearMonthDate(t)
	within := timeWithinDay(t)
	h := math.Floor(within / 3600000)
	mi := math.Mod(math.Floor(within/60000), 60)
	s := math.Mod(math.Floor(within/1000), 60)
	ms := math.Mod(within, 1000)
	var year string
	switch {
	case y >= 0 && y <= 9999:
		year = fmt.Sprintf("%04d", int(y))
	case y < 0:
		year = fmt.Sprintf("-%06d", int(-y))
	default:
		year = fmt.Sprintf("+%06d", int(y))
	}
	return fmt.Sprintf("%s-%02d-%02dT%02d:%02d:%02d.%03dZ", year, int(m)+1, int(d), int(h), int(mi), int(s), int(ms))
}

// dateFields are the UTC components of a time value, in the order of the
// setter arguments.
func dateFields(t float64) (year, month, date, hours, minutes, seconds, ms float64) {
	year, month, date = yearMonthDate(t)
	within := timeWithinDay(t)
	return year, month, date, math.Floor(within / 3600000), math.Mod(math.Floor(within/60000), 60), math.Mod(math.Floor(within/1000), 60), math.Mod(within, 1000)
}

// dateMethodNames are the methods of a Date that Method dispatches; the
// local-time methods are rejected by the compiler.
var dateMethodNames = map[string]bool{
	"getTime": true, "valueOf": true, "toISOString": true, "toJSON": true,
	"getUTCFullYear": true, "getUTCMonth": true, "getUTCDate": true, "getUTCDay": true,
	"getUTCHours": true, "getUTCMinutes": true, "getUTCSeconds": true, "getUTCMilliseconds": true,
	"setTime": true, "setUTCFullYear": true, "setUTCMonth": true, "setUTCDate": true,
	"setUTCHours": true, "setUTCMinutes": true, "setUTCSeconds": true, "setUTCMilliseconds": true,
	"toString": true,
}

// dateMethod calls a method of a Date.
func (js *JS) dateMethod(site string, d *Date, name string, args []any) (any, bool) {
	t := d.time
	nan := math.IsNaN(t)
	field := func(f func(float64) float64) any {
		if nan {
			return math.NaN()
		}
		return f(t)
	}
	switch name {
	case "getTime", "valueOf":
		return t, true
	case "toISOString":
		if nan {
			return js.throw("RangeError", "Invalid time value", site), true
		}
		return isoString(t), true
	case "toJSON":
		if nan {
			return nil, true
		}
		return isoString(t), true
	case "getUTCFullYear":
		return field(yearFromTime), true
	case "getUTCMonth":
		return field(func(t float64) float64 { _, m, _ := yearMonthDate(t); return m }), true
	case "getUTCDate":
		return field(func(t float64) float64 { _, _, d := yearMonthDate(t); return d }), true
	case "getUTCDay":
		return field(func(t float64) float64 { return math.Mod(math.Mod(day(t)+4, 7)+7, 7) }), true
	case "getUTCHours":
		return field(func(t float64) float64 { return math.Floor(timeWithinDay(t) / 3600000) }), true
	case "getUTCMinutes":
		return field(func(t float64) float64 { return math.Mod(math.Floor(timeWithinDay(t)/60000), 60) }), true
	case "getUTCSeconds":
		return field(func(t float64) float64 { return math.Mod(math.Floor(timeWithinDay(t)/1000), 60) }), true
	case "getUTCMilliseconds":
		return field(func(t float64) float64 { return math.Mod(timeWithinDay(t), 1000) }), true
	case "toString":
		return js.unsupported("Date.prototype.toString formats the date in the browser's time zone; use toISOString() or the getUTC… methods", site), true
	}
	if !strings.HasPrefix(name, "set") {
		return nil, false
	}
	// The setters convert every argument first; a NaN date stays NaN,
	// except setUTCFullYear, which starts from +0.
	y, mo, dt, h, mi, s, ms := dateFields(t)
	numbers := make([]float64, len(args))
	for i, arg := range args {
		numbers[i] = ToNumber(jsValue(arg))
	}
	if js.Failed() {
		return Undefined(), true
	}
	arg := func(i int, fallback float64) float64 {
		if i < len(numbers) {
			return numbers[i]
		}
		return fallback
	}
	var result float64
	switch name {
	case "setTime":
		result = timeClip(arg(0, math.NaN()))
	case "setUTCFullYear":
		if nan {
			y, mo, dt, h, mi, s, ms = dateFields(0)
		}
		result = makeDate(makeDay(arg(0, math.NaN()), arg(1, mo), arg(2, dt)), makeTime(h, mi, s, ms))
	case "setUTCMonth":
		result = makeDate(makeDay(y, arg(0, math.NaN()), arg(1, dt)), makeTime(h, mi, s, ms))
	case "setUTCDate":
		result = makeDate(makeDay(y, mo, arg(0, math.NaN())), makeTime(h, mi, s, ms))
	case "setUTCHours":
		result = makeDate(makeDay(y, mo, dt), makeTime(arg(0, math.NaN()), arg(1, mi), arg(2, s), arg(3, ms)))
	case "setUTCMinutes":
		result = makeDate(makeDay(y, mo, dt), makeTime(h, arg(0, math.NaN()), arg(1, s), arg(2, ms)))
	case "setUTCSeconds":
		result = makeDate(makeDay(y, mo, dt), makeTime(h, mi, arg(0, math.NaN()), arg(1, ms)))
	case "setUTCMilliseconds":
		result = makeDate(makeDay(y, mo, dt), makeTime(h, mi, s, arg(0, math.NaN())))
	default:
		return nil, false
	}
	if nan && name != "setUTCFullYear" && name != "setTime" {
		result = math.NaN()
	}
	d.time = timeClip(result)
	return d.time, true
}

// toPrimitive is Date.prototype[Symbol.toPrimitive]: the hint "number"
// gives the time value; "string" and "default" format the date in the
// local time zone, which is not translated.
func (d *Date) toPrimitive(hint string) any {
	if hint == "number" {
		return d.time
	}
	return d.js.unsupported("converting a Date to text formats it in the browser's time zone; use toISOString() or the getUTC… methods", "")
}

// MarshalJSON writes the date as JSON.stringify does: its ISO text, or
// null for an invalid date.
func (d *Date) MarshalJSON() ([]byte, error) {
	if math.IsNaN(d.time) {
		return []byte("null"), nil
	}
	return []byte(strconv.Quote(isoString(d.time))), nil
}
