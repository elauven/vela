package ssr_runtime

import "math"

// Port of the FreeBSD msun cbrt that V8 uses for Math.cbrt
// (src/base/ieee754.cc), so that results match JavaScript bit for bit;
// Go's math.Cbrt differs in the last place for many inputs (checked by
// TestFdlibmMatchesNode). Math.pow is in mathcr.go.

func hi(x float64) int32  { return int32(math.Float64bits(x) >> 32) }
func lo(x float64) uint32 { return uint32(math.Float64bits(x)) }

func fromWords(high int32, low uint32) float64 {
	return math.Float64frombits(uint64(uint32(high))<<32 | uint64(low))
}

func withLow(x float64, low uint32) float64 { return fromWords(hi(x), low) }
func withHigh(x float64, high int32) float64 {
	return fromWords(high, lo(x))
}

const (
	cbrtB1 = 715094163
	cbrtB2 = 696219795
	cbrtP0 = 1.87595182427177009643
	cbrtP1 = -1.88497979543377169875
	cbrtP2 = 1.621429720105354466140
	cbrtP3 = -0.758397934778766047437
	cbrtP4 = 0.145996192886612446982
)

// fdlibmCbrt is FreeBSD's s_cbrt.c, as in V8.
func fdlibmCbrt(x float64) float64 {
	hx := hi(x)
	low := lo(x)
	sign := uint32(hx) & 0x80000000
	hx = int32(uint32(hx) ^ sign)
	if hx >= 0x7ff00000 {
		return x + x
	}
	var t float64
	if hx < 0x00100000 {
		if uint32(hx)|low == 0 {
			return x
		}
		t = fromWords(0x43500000, 0) * x
		high := uint32(hi(t))
		t = fromWords(int32(sign|((high&0x7fffffff)/3+cbrtB2)), 0)
	} else {
		t = fromWords(int32(sign|(uint32(hx)/3+cbrtB1)), 0)
	}
	r := (t * t) * (t / x)
	t = t * ((cbrtP0 + r*(cbrtP1+r*cbrtP2)) + ((r*r)*r)*(cbrtP3+r*cbrtP4))
	bits := (math.Float64bits(t) + 0x80000000) & 0xffffffffc0000000
	t = math.Float64frombits(bits)
	s := t * t
	r = x / s
	w := t + t
	r = (r - t) / (w + r)
	return t + t*r
}
