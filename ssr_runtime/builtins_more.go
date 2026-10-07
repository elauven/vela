package ssr_runtime

import (
	"context"
	"math"
	"math/big"
	"math/bits"
	"strconv"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// Built-ins admitted by the decisions of docs/SSR_TRANSLATION_MATRIX.md §5
// (2026-10-07): String.prototype.normalize, Number.prototype.toString with
// a radix, toPrecision, toExponential, the transcendental Math functions
// (ports of the fdlibm functions V8 uses), and console.*.

// normalize is String.prototype.normalize(form).
func (js *JS) normalize(site, s string, args []any) any {
	form := "NFC"
	if !undefinedArg(args, 0) {
		form = String(jsValue(args[0]))
	}
	switch form {
	case "NFC":
		return norm.NFC.String(s)
	case "NFD":
		return norm.NFD.String(s)
	case "NFKC":
		return norm.NFKC.String(s)
	case "NFKD":
		return norm.NFKD.String(s)
	}
	return js.throw("RangeError", "The normalization form should be one of NFC, NFD, NFKC, NFKD.", site)
}

const radixDigits = "0123456789abcdefghijklmnopqrstuvwxyz"

// radixString is V8's DoubleToRadixCString: fraction digits up to the
// precision of the double, rounded to even.
func radixString(value float64, radix int) string {
	if math.IsNaN(value) {
		return "NaN"
	}
	if math.IsInf(value, 0) {
		if value > 0 {
			return "Infinity"
		}
		return "-Infinity"
	}
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	integer := math.Floor(value)
	fraction := value - integer
	delta := 0.5 * (math.Nextafter(value, math.Inf(1)) - value)
	delta = math.Max(math.Nextafter(0, 1), delta)
	var frac []byte
	if fraction >= delta {
		for {
			fraction *= float64(radix)
			delta *= float64(radix)
			digit := int(fraction)
			frac = append(frac, radixDigits[digit])
			fraction -= float64(digit)
			if fraction > 0.5 || fraction == 0.5 && digit&1 == 1 {
				if fraction+delta > 1 {
					// Round up, carrying into earlier digits.
					for {
						if len(frac) == 0 {
							integer++
							break
						}
						last := strings.IndexByte(radixDigits, frac[len(frac)-1])
						frac = frac[:len(frac)-1]
						if last+1 < radix {
							frac = append(frac, radixDigits[last+1])
							break
						}
					}
					break
				}
			}
			if fraction < delta {
				break
			}
		}
	}
	var digits []byte
	for exponentOf(integer/float64(radix)) > 0 {
		integer /= float64(radix)
		digits = append(digits, '0')
	}
	for {
		remainder := math.Mod(integer, float64(radix))
		digits = append(digits, radixDigits[int(remainder)])
		integer = (integer - remainder) / float64(radix)
		if integer <= 0 {
			break
		}
	}
	for i, j := 0, len(digits)-1; i < j; i, j = i+1, j-1 {
		digits[i], digits[j] = digits[j], digits[i]
	}
	out := string(digits)
	if len(frac) > 0 {
		out += "." + string(frac)
	}
	if negative {
		out = "-" + out
	}
	return out
}

// exponentOf is the binary exponent of the double's integer significand
// (V8's Double::Exponent): positive from 2^53.
func exponentOf(x float64) int {
	b := math.Float64bits(x)
	biased := int(b>>52) & 0x7ff
	if biased == 0 {
		return -1074
	}
	return biased - 0x3ff - 52
}

// decimalDigits returns the p significant decimal digits of x > 0 and the
// decimal exponent e of the first, rounding the exact binary value half up
// (Number::toPrecision and toExponential).
func decimalDigits(x float64, p int) (string, int) {
	r := new(big.Rat).SetFloat64(x)
	ten := big.NewRat(10, 1)
	pow := func(e int) *big.Rat {
		out := big.NewRat(1, 1)
		base := ten
		if e < 0 {
			base, e = big.NewRat(1, 10), -e
		}
		for i := 0; i < e; i++ {
			out.Mul(out, base)
		}
		return out
	}
	e := int(math.Floor(math.Log10(x)))
	for pow(e).Cmp(r) > 0 {
		e--
	}
	for pow(e+1).Cmp(r) <= 0 {
		e++
	}
	scaled := new(big.Rat).Quo(r, pow(e-p+1))
	scaled.Add(scaled, big.NewRat(1, 2))
	n := new(big.Int).Quo(scaled.Num(), scaled.Denom())
	text := n.String()
	if len(text) > p {
		text, e = text[:p], e+1
	}
	return text, e
}

func exponentText(e int) string {
	if e >= 0 {
		return "e+" + strconv.Itoa(e)
	}
	return "e-" + strconv.Itoa(-e)
}

func (js *JS) toPrecision(site string, x float64, precision any) any {
	if _, undefined := precision.(UndefinedValue); undefined || precision == nil {
		return String(x)
	}
	p := toIntegerOrInfinity(precision)
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return String(x)
	}
	if p < 1 || p > 100 {
		return js.throw("RangeError", "toPrecision() argument must be between 1 and 100", site)
	}
	sign := ""
	if x < 0 {
		sign, x = "-", -x
	}
	if x == 0 {
		if p == 1 {
			return sign + "0"
		}
		return sign + "0." + strings.Repeat("0", int(p)-1)
	}
	digits, e := decimalDigits(x, int(p))
	if e < -6 || e >= int(p) {
		out := digits[:1]
		if len(digits) > 1 {
			out += "." + digits[1:]
		}
		return sign + out + exponentText(e)
	}
	if e == int(p)-1 {
		return sign + digits
	}
	if e >= 0 {
		return sign + digits[:e+1] + "." + digits[e+1:]
	}
	return sign + "0." + strings.Repeat("0", -(e+1)) + digits
}

func (js *JS) toExponential(site string, x float64, fraction any) any {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return String(x)
	}
	_, undefined := fraction.(UndefinedValue)
	undefined = undefined || fraction == nil
	f := toIntegerOrInfinity(fraction)
	if f < 0 || f > 100 {
		return js.throw("RangeError", "toExponential() argument must be between 0 and 100", site)
	}
	sign := ""
	if x < 0 {
		sign, x = "-", -x
	}
	var digits string
	var e int
	switch {
	case x == 0:
		digits = strings.Repeat("0", int(f)+1)
		if undefined {
			digits = "0"
		}
	case undefined:
		// As many digits as the shortest round trip needs.
		shortest := strconv.FormatFloat(x, 'e', -1, 64)
		mantissa, exponent, _ := strings.Cut(shortest, "e")
		digits = strings.ReplaceAll(mantissa, ".", "")
		e, _ = strconv.Atoi(exponent)
	default:
		digits, e = decimalDigits(x, int(f)+1)
	}
	out := digits[:1]
	if len(digits) > 1 {
		out += "." + digits[1:]
	}
	return sign + out + exponentText(e)
}

// ── Transcendental Math functions (ports of V8's fdlibm, fdlibm_math.go) ─

func MathLog(value any) float64   { return fdLog(num(value)) }
func MathLog2(value any) float64  { return fdLog2(num(value)) }
func MathLog10(value any) float64 { return fdLog10(num(value)) }
func MathLog1p(value any) float64 { return math.Log1p(num(value)) }
func MathExp(value any) float64   { return fdExp(num(value)) }
func MathExpm1(value any) float64 { return math.Expm1(num(value)) }
func MathSin(value any) float64   { return fdSin(num(value)) }
func MathCos(value any) float64   { return fdCos(num(value)) }
func MathTan(value any) float64   { return fdTan(num(value)) }
func MathAsin(value any) float64  { return fdAsin(num(value)) }
func MathAcos(value any) float64  { return fdAcos(num(value)) }
func MathAtan(value any) float64  { return fdAtan(num(value)) }
func MathSinh(value any) float64  { return fdSinh(num(value)) }
func MathCosh(value any) float64  { return fdCosh(num(value)) }
func MathTanh(value any) float64  { return fdTanh(num(value)) }
func MathAsinh(value any) float64 { return fdAsinh(num(value)) }
func MathAcosh(value any) float64 { return fdAcosh(num(value)) }
func MathAtanh(value any) float64 { return fdAtanh(num(value)) }

func MathAtan2(y, x any) float64 { return fdAtan2(num(y), num(x)) }

// MathFround, MathClz32, and MathImul are exact.
func MathFround(value any) float64 { return float64(float32(num(value))) }
func MathClz32(value any) float64 {
	return float64(bits.LeadingZeros32(toUint32(num(value))))
}
func MathImul(a, b any) float64 {
	return float64(toInt32(num(a)) * toInt32(num(b)))
}

// ── console.* ────────────────────────────────────────────────────────────

type consoleObserverKey struct{}

// ConsoleObserver receives console calls made by translated code.
type ConsoleObserver func(method string, args []any)

// WithConsoleObserver passes the console.* calls of a render to observe;
// without one they do nothing.
func WithConsoleObserver(ctx context.Context, observe ConsoleObserver) context.Context {
	return context.WithValue(ctx, consoleObserverKey{}, observe)
}

// Console is console.<method>(args...).
func (js *JS) Console(ctx context.Context, method string, args ...any) UndefinedValue {
	if js.Failed() || ctx == nil {
		return Undefined()
	}
	if observe, ok := ctx.Value(consoleObserverKey{}).(ConsoleObserver); ok {
		observe(method, args)
	}
	return Undefined()
}

// ── Object.freeze and Object.isFrozen (decision of §5) ──────────────────

// ObjectFreeze is Object.freeze: an object rejects later writes; an array
// or a primitive is returned unchanged (a Go slice has no identity to
// mark, so Object.isFrozen rejects arrays).
func ObjectFreeze(value any) any {
	if object, ok := value.(*Object); ok {
		object.frozen = true
	}
	return value
}

// ObjectIsFrozen is Object.isFrozen.
func (js *JS) ObjectIsFrozen(site string, value any) bool {
	switch v := jsValue(value).(type) {
	case *Object:
		return v.frozen
	case []any:
		js.throw("TypeError", "Object.isFrozen on an array is not supported during server rendering", site)
		return false
	case nil, UndefinedValue, bool, float64, string:
		return true
	}
	return false
}
