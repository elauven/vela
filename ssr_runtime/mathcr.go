package ssr_runtime

import (
	"math"
	"math/big"
)

// Correctly rounded pow. V8 on Linux computes Math.pow with the platform
// libm, whose pow is correctly rounded in practice; Go's math.Pow and
// fdlibm's pow differ from it in the last place for many inputs
// (TestFdlibmMatchesNode). Integer exponents are computed exactly; other
// exponents as exp(y·ln|x|) in 256-bit arithmetic, rounded once.

const crPrec = 256

func bigFloat(x float64) *big.Float { return new(big.Float).SetPrec(crPrec).SetFloat64(x) }

var crLn2 = func() *big.Float {
	// ln 2 = 2·atanh(1/3).
	third := new(big.Float).SetPrec(crPrec).Quo(bigFloat(1), bigFloat(3))
	return atanhSeries(third, 2)
}()

// atanhSeries returns factor·atanh(z) = factor·(z + z³/3 + z⁵/5 + …) for
// small |z|.
func atanhSeries(z *big.Float, factor float64) *big.Float {
	sum := new(big.Float).SetPrec(crPrec).Set(z)
	z2 := new(big.Float).SetPrec(crPrec).Mul(z, z)
	power := new(big.Float).SetPrec(crPrec).Set(z)
	epsilon := new(big.Float).SetPrec(crPrec).SetMantExp(bigFloat(1), -crPrec-8)
	for k := 3; ; k += 2 {
		power.Mul(power, z2)
		term := new(big.Float).SetPrec(crPrec).Quo(power, bigFloat(float64(k)))
		sum.Add(sum, term)
		if new(big.Float).Abs(term).Cmp(epsilon) < 0 {
			break
		}
	}
	return sum.Mul(sum, bigFloat(factor))
}

// bigLn returns ln x for x > 0.
func bigLn(x float64) *big.Float {
	m, e := math.Frexp(x) // x = m·2^e, m in [0.5, 1)
	mantissa := bigFloat(m)
	num := new(big.Float).SetPrec(crPrec).Sub(mantissa, bigFloat(1))
	den := new(big.Float).SetPrec(crPrec).Add(mantissa, bigFloat(1))
	z := num.Quo(num, den)
	result := atanhSeries(z, 2)
	return result.Add(result, new(big.Float).SetPrec(crPrec).Mul(crLn2, bigFloat(float64(e))))
}

// bigExp returns exp(t) rounded to float64, with overflow and underflow.
func bigExp(t *big.Float) float64 {
	// t = k·ln2 + r with |r| <= ln2/2.
	q := new(big.Float).SetPrec(crPrec).Quo(t, crLn2)
	qf, _ := q.Float64()
	if qf > 1100 {
		return math.Inf(1)
	}
	if qf < -1200 {
		return 0
	}
	k := math.Round(qf)
	r := new(big.Float).SetPrec(crPrec).Sub(t, new(big.Float).SetPrec(crPrec).Mul(crLn2, bigFloat(k)))
	// exp(r) = exp(r/2^8)^(2^8).
	r.SetMantExp(r, -8)
	sum := bigFloat(1)
	term := bigFloat(1)
	epsilon := new(big.Float).SetPrec(crPrec).SetMantExp(bigFloat(1), -crPrec-8)
	for n := 1; ; n++ {
		term.Mul(term, r)
		term.Quo(term, bigFloat(float64(n)))
		sum.Add(sum, term)
		if new(big.Float).Abs(term).Cmp(epsilon) < 0 {
			break
		}
	}
	for i := 0; i < 8; i++ {
		sum.Mul(sum, sum)
	}
	sum.SetMantExp(sum, int(k))
	value, _ := sum.Float64()
	return value
}

// crPow is x**y for finite x > 0 and finite y; sign handling is the
// caller's.
func crPow(x, y float64) float64 {
	t := new(big.Float).SetPrec(crPrec).Mul(bigLn(x), bigFloat(y))
	return bigExp(t)
}

// jsPow implements Math.pow and **.
func jsPow(base, exponent float64) float64 {
	switch {
	case math.IsNaN(exponent):
		return math.NaN()
	case math.IsInf(exponent, 0) && math.Abs(base) == 1:
		return math.NaN()
	case exponent == 0:
		return 1
	}
	if exact, ok := integerPow(base, exponent); ok {
		return exact
	}
	if exponent == 0.5 && base > 0 && !math.IsInf(base, 0) {
		return math.Sqrt(base)
	}
	if math.IsNaN(base) || math.IsInf(base, 0) || base == 0 || math.IsInf(exponent, 0) || base < 0 && exponent != math.Trunc(exponent) {
		// Special values follow IEEE pow, as math.Pow does.
		return math.Pow(base, exponent)
	}
	sign := 1.0
	if base < 0 {
		// An integer exponent beyond the exact range: odd keeps the sign.
		if math.Mod(exponent, 2) != 0 {
			sign = -1
		}
		base = -base
	}
	return float64(sign * crPow(base, exponent))
}

// integerPow computes a finite base to a small integer power exactly and
// rounds once.
func integerPow(base, exponent float64) (float64, bool) {
	if exponent != math.Trunc(exponent) || math.Abs(exponent) > 64 || math.IsInf(base, 0) || math.IsNaN(base) || base == 0 {
		return 0, false
	}
	n := int(math.Abs(exponent))
	prec := uint(64*53 + 64)
	result := new(big.Float).SetPrec(prec).SetInt64(1)
	factor := new(big.Float).SetPrec(prec).SetFloat64(base)
	for i := 0; i < n; i++ {
		result.Mul(result, factor)
	}
	if exponent < 0 {
		result.Quo(new(big.Float).SetPrec(prec).SetInt64(1), result)
	}
	value, _ := result.Float64()
	return value, true
}
