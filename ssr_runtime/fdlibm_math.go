package ssr_runtime

import "math"

// Ports of the fdlibm / FreeBSD msun functions V8 uses for Math (src/base/
// ieee754.cc), so that the transcendental functions match JavaScript bit
// for bit (TestTranscendentalMatchesNode). Go's math differs: its amd64
// assembly for Log and Exp, its Log2 near 1, its trigonometric reduction
// for large arguments, and its hyperbolic functions. Log1p and Expm1 are
// Go's algorithms, copied (fdlibm_log1p.go) because the standard library
// may be built with fused multiply-add on other architectures; math.Sqrt
// is exact. Every floating-point product in this package is wrapped in an
// explicit conversion, which prevents the compiler from fusing it
// (TestNoFusedMultiplyAdd).

var (
	fdHuge = 1.0e300
	fdTiny = 1.0e-300
	fdZero = 0.0
)

// ── Trigonometric kernels ────────────────────────────────────────────────

const (
	kS1 = -1.66666666666666324348e-01
	kS2 = 8.33333333332248946124e-03
	kS3 = -1.98412698298579493134e-04
	kS4 = 2.75573137070700676789e-06
	kS5 = -2.50507602534068634195e-08
	kS6 = 1.58969099521155010221e-10

	kC1 = 4.16666666666666019037e-02
	kC2 = -1.38888888888741095749e-03
	kC3 = 2.48015872894767294178e-05
	kC4 = -2.75573143513906633035e-07
	kC5 = 2.08757232129817482790e-09
	kC6 = -1.13596475577881948265e-11
)

func kernelSin(x, y float64, iy int) float64 {
	ix := hi(x) & 0x7fffffff
	if ix < 0x3e400000 && int(x) == 0 {
		return x
	}
	z := float64(x * x)
	v := float64(z * x)
	r := kS2 + float64(z*(kS3+float64(z*(kS4+float64(z*(kS5+float64(z*kS6)))))))
	if iy == 0 {
		return x + float64(v*(kS1+float64(z*r)))
	}
	return x - ((float64(z*(float64(0.5*y)-float64(v*r))) - y) - float64(v*kS1))
}

func kernelCos(x, y float64) float64 {
	ix := hi(x) & 0x7fffffff
	if ix < 0x3e400000 && int(x) == 0 {
		return 1
	}
	z := float64(x * x)
	r := float64(z * (kC1 + float64(z*(kC2+float64(z*(kC3+float64(z*(kC4+float64(z*(kC5+float64(z*kC6)))))))))))
	if ix < 0x3FD33333 {
		return 1 - (float64(0.5*z) - (float64(z*r) - float64(x*y)))
	}
	var qx float64
	if ix > 0x3fe90000 {
		qx = 0.28125
	} else {
		qx = fromWords(ix-0x00200000, 0)
	}
	hz := float64(0.5*z) - qx
	a := 1 - qx
	return a - (hz - (float64(z*r) - float64(x*y)))
}

var kTan = [...]float64{
	3.33333333333334091986e-01,
	1.33333333333201242699e-01,
	5.39682539762260521377e-02,
	2.18694882948595424599e-02,
	8.86323982359930005737e-03,
	3.59207910759131235356e-03,
	1.45620945432529025516e-03,
	5.88041240820264096874e-04,
	2.46463134818469906812e-04,
	7.81794442939557092300e-05,
	7.14072491382608190305e-05,
	-1.85586374855275456654e-05,
	2.59073051863633712884e-05,
}

const (
	kPio4   = 7.85398163397448278999e-01
	kPio4lo = 3.06161699786838301793e-17
)

func kernelTan(x, y float64, iy int) float64 {
	hx := hi(x)
	ix := hx & 0x7fffffff
	if ix < 0x3e300000 && int(x) == 0 {
		if (uint32(ix)|lo(x))|uint32(iy+1) == 0 {
			return 1 / math.Abs(x)
		}
		if iy == 1 {
			return x
		}
		w := x + y
		z := withLow(w, 0)
		v := y - (z - x)
		a := -1 / w
		t := withLow(a, 0)
		s := 1 + float64(t*z)
		return t + float64(a*(s+float64(t*v)))
	}
	if ix >= 0x3FE59428 {
		if hx < 0 {
			x, y = -x, -y
		}
		z := kPio4 - x
		w := kPio4lo - y
		x = z + w
		y = 0
	}
	z := float64(x * x)
	w := float64(z * z)
	r := kTan[1] + float64(w*(kTan[3]+float64(w*(kTan[5]+float64(w*(kTan[7]+float64(w*(kTan[9]+float64(w*kTan[11])))))))))
	v := float64(z * (kTan[2] + float64(w*(kTan[4]+float64(w*(kTan[6]+float64(w*(kTan[8]+float64(w*(kTan[10]+float64(w*kTan[12])))))))))))
	s := float64(z * x)
	r = y + float64(z*(float64(s*(r+v))+y))
	r += float64(kTan[0] * s)
	w = x + r
	if ix >= 0x3FE59428 {
		v = float64(iy)
		return float64(float64(1-((hx>>30)&2)) * (v - float64(2.0*(x-(float64(w*w)/(w+v)-r)))))
	}
	if iy == 1 {
		return w
	}
	z = withLow(w, 0)
	v = r - (z - x)
	a := -1 / w
	t := withLow(a, 0)
	s = 1 + float64(t*z)
	return t + float64(a*(s+float64(t*v)))
}

// ── Argument reduction ───────────────────────────────────────────────────

var twoOverPi = [...]int32{
	0xA2F983, 0x6E4E44, 0x1529FC, 0x2757D1, 0xF534DD, 0xC0DB62,
	0x95993C, 0x439041, 0xFE5163, 0xABDEBB, 0xC561B7, 0x246E3A,
	0x424DD2, 0xE00649, 0x2EEA09, 0xD1921C, 0xFE1DEB, 0x1CB129,
	0xA73EE8, 0x8235F5, 0x2EBB44, 0x84E99C, 0x7026B4, 0x5F7E41,
	0x3991D6, 0x398353, 0x39F49C, 0x845F8B, 0xBDF928, 0x3B1FF8,
	0x97FFDE, 0x05980F, 0xEF2F11, 0x8B5A0A, 0x6D1F6D, 0x367ECF,
	0x27CB09, 0xB74F46, 0x3F669E, 0x5FEA2D, 0x7527BA, 0xC7EBE5,
	0xF17B3D, 0x0739F7, 0x8A5292, 0xEA6BFB, 0x5FB11F, 0x8D5D08,
	0x560330, 0x46FC7B, 0x6BABF0, 0xCFBC20, 0x9AF436, 0x1DA9E3,
	0x91615E, 0xE61B08, 0x659985, 0x5F14A0, 0x68408D, 0xFFD880,
	0x4D7327, 0x310606, 0x1556CA, 0x73A8C9, 0x60E27B, 0xC08C6B,
}

var npio2HW = [...]int32{
	0x3FF921FB, 0x400921FB, 0x4012D97C, 0x401921FB, 0x401F6A7A, 0x4022D97C,
	0x4025FDBB, 0x402921FB, 0x402C463A, 0x402F6A7A, 0x4031475C, 0x4032D97C,
	0x40346B9C, 0x4035FDBB, 0x40378FDB, 0x403921FB, 0x403AB41B, 0x403C463A,
	0x403DD85A, 0x403F6A7A, 0x40407E4C, 0x4041475C, 0x4042106C, 0x4042D97C,
	0x4043A28C, 0x40446B9C, 0x404534AC, 0x4045FDBB, 0x4046C6CB, 0x40478FDB,
	0x404858EB, 0x404921FB,
}

const (
	rTwo24   = 1.67772160000000000000e+07
	rTwon24  = 5.96046447753906250000e-08
	rInvpio2 = 6.36619772367581382433e-01
	rPio2_1  = 1.57079632673412561417e+00
	rPio2_1t = 6.07710050650619224932e-11
	rPio2_2  = 6.07710050630396597660e-11
	rPio2_2t = 2.02226624879595063154e-21
	rPio2_3  = 2.02226624871116645580e-21
	rPio2_3t = 8.47842766036889956997e-32
)

var pio2Parts = [...]float64{
	1.57079625129699707031e+00,
	7.54978941586159635335e-08,
	5.39030252995776476554e-15,
	3.28200341580791294123e-22,
	1.27065575308067607349e-29,
	1.22933308981111328932e-36,
	2.73370053816464559624e-44,
	2.16741683877804819444e-51,
}

// remPio2 is __ieee754_rem_pio2: x = n·π/2 + (y0 + y1).
func remPio2(x float64) (int, float64, float64) {
	hx := hi(x)
	ix := hx & 0x7fffffff
	if ix <= 0x3fe921fb {
		return 0, x, 0
	}
	if ix < 0x4002d97c {
		if hx > 0 {
			z := x - rPio2_1
			if ix != 0x3ff921fb {
				y0 := z - rPio2_1t
				return 1, y0, (z - y0) - rPio2_1t
			}
			z -= rPio2_2
			y0 := z - rPio2_2t
			return 1, y0, (z - y0) - rPio2_2t
		}
		z := x + rPio2_1
		if ix != 0x3ff921fb {
			y0 := z + rPio2_1t
			return -1, y0, (z - y0) + rPio2_1t
		}
		z += rPio2_2
		y0 := z + rPio2_2t
		return -1, y0, (z - y0) + rPio2_2t
	}
	if ix <= 0x413921fb {
		t := math.Abs(x)
		n := int(float64(t*rInvpio2) + 0.5)
		fn := float64(n)
		r := t - float64(fn*rPio2_1)
		w := float64(fn * rPio2_1t)
		var y0 float64
		if n < 32 && ix != npio2HW[n-1] {
			y0 = r - w
		} else {
			j := ix >> 20
			y0 = r - w
			i := j - ((hi(y0) >> 20) & 0x7ff)
			if i > 16 {
				t = r
				w = float64(fn * rPio2_2)
				r = t - w
				w = float64(fn*rPio2_2t) - ((t - r) - w)
				y0 = r - w
				i = j - ((hi(y0) >> 20) & 0x7ff)
				if i > 49 {
					t = r
					w = float64(fn * rPio2_3)
					r = t - w
					w = float64(fn*rPio2_3t) - ((t - r) - w)
					y0 = r - w
				}
			}
		}
		y1 := (r - y0) - w
		if hx < 0 {
			return -n, -y0, -y1
		}
		return n, y0, y1
	}
	if ix >= 0x7ff00000 {
		return 0, x - x, x - x
	}
	e0 := (ix >> 20) - 1046
	z := fromWords(ix-(e0<<20), lo(x))
	var tx [3]float64
	for i := 0; i < 2; i++ {
		tx[i] = float64(int32(z))
		z = float64((z - tx[i]) * rTwo24)
	}
	tx[2] = z
	nx := 3
	for tx[nx-1] == 0 {
		nx--
	}
	n, y0, y1 := kernelRemPio2(tx[:nx], int(e0))
	if hx < 0 {
		return -n, -y0, -y1
	}
	return n, y0, y1
}

// kernelRemPio2 is __kernel_rem_pio2 with prec 2.
func kernelRemPio2(x []float64, e0 int) (int, float64, float64) {
	const jk = 4
	jp := jk
	nx := len(x)
	jx := nx - 1
	jv := (e0 - 3) / 24
	if jv < 0 {
		jv = 0
	}
	q0 := e0 - 24*(jv+1)
	var f, q, fq [20]float64
	var iq [20]int32
	j := jv - jx
	m := jx + jk
	for i := 0; i <= m; i, j = i+1, j+1 {
		if j < 0 {
			f[i] = 0
		} else {
			f[i] = float64(twoOverPi[j])
		}
	}
	for i := 0; i <= jk; i++ {
		fw := 0.0
		for j := 0; j <= jx; j++ {
			fw += float64(x[j] * f[jx+i-j])
		}
		q[i] = fw
	}
	jz := jk
	var z float64
	var n, ih int32
	for {
		// Distill q[] into iq[] reversingly.
		i := 0
		z = q[jz]
		for j := jz; j > 0; i, j = i+1, j-1 {
			fw := float64(int32(float64(rTwon24 * z)))
			iq[i] = int32(z - float64(rTwo24*fw))
			z = q[j-1] + fw
		}
		z = math.Ldexp(z, q0)
		z -= float64(8.0 * math.Floor(float64(z*0.125)))
		n = int32(z)
		z -= float64(n)
		ih = 0
		if q0 > 0 {
			i := iq[jz-1] >> uint(24-q0)
			n += i
			iq[jz-1] -= i << uint(24-q0)
			ih = iq[jz-1] >> uint(23-q0)
		} else if q0 == 0 {
			ih = iq[jz-1] >> 23
		} else if z >= 0.5 {
			ih = 2
		}
		if ih > 0 {
			n++
			carry := int32(0)
			for i := 0; i < jz; i++ {
				j := iq[i]
				if carry == 0 {
					if j != 0 {
						carry = 1
						iq[i] = 0x1000000 - j
					}
				} else {
					iq[i] = 0xffffff - j
				}
			}
			if q0 > 0 {
				switch q0 {
				case 1:
					iq[jz-1] &= 0x7fffff
				case 2:
					iq[jz-1] &= 0x3fffff
				}
			}
			if ih == 2 {
				z = 1 - z
				if carry != 0 {
					z -= math.Ldexp(1, q0)
				}
			}
		}
		if z == 0 {
			j := int32(0)
			for i := jz - 1; i >= jk; i-- {
				j |= iq[i]
			}
			if j == 0 {
				k := 1
				for iq[jk-k] == 0 {
					k++
				}
				for i := jz + 1; i <= jz+k; i++ {
					f[jx+i] = float64(twoOverPi[jv+i])
					fw := 0.0
					for j := 0; j <= jx; j++ {
						fw += float64(x[j] * f[jx+i-j])
					}
					q[i] = fw
				}
				jz += k
				continue
			}
		}
		break
	}
	if z == 0 {
		jz--
		q0 -= 24
		for iq[jz] == 0 {
			jz--
			q0 -= 24
		}
	} else {
		z = math.Ldexp(z, -q0)
		if z >= rTwo24 {
			fw := float64(int32(float64(rTwon24 * z)))
			iq[jz] = int32(z - float64(rTwo24*fw))
			jz++
			q0 += 24
			iq[jz] = int32(fw)
		} else {
			iq[jz] = int32(z)
		}
	}
	fw := math.Ldexp(1, q0)
	for i := jz; i >= 0; i-- {
		q[i] = float64(fw * float64(iq[i]))
		fw = float64(fw * (rTwon24))
	}
	for i := jz; i >= 0; i-- {
		fw := 0.0
		for k := 0; k <= jp && k <= jz-i; k++ {
			fw += float64(pio2Parts[k] * q[i+k])
		}
		fq[jz-i] = fw
	}
	fw = 0
	for i := jz; i >= 0; i-- {
		fw += fq[i]
	}
	y0 := fw
	if ih != 0 {
		y0 = -fw
	}
	fw = fq[0] - fw
	for i := 1; i <= jz; i++ {
		fw += fq[i]
	}
	y1 := fw
	if ih != 0 {
		y1 = -fw
	}
	return int(n & 7), y0, y1
}

// ── sin, cos, tan ────────────────────────────────────────────────────────

func fdSin(x float64) float64 {
	ix := hi(x) & 0x7fffffff
	if ix <= 0x3fe921fb {
		return kernelSin(x, 0, 0)
	}
	if ix >= 0x7ff00000 {
		return x - x
	}
	n, y0, y1 := remPio2(x)
	switch n & 3 {
	case 0:
		return kernelSin(y0, y1, 1)
	case 1:
		return kernelCos(y0, y1)
	case 2:
		return -kernelSin(y0, y1, 1)
	}
	return -kernelCos(y0, y1)
}

func fdCos(x float64) float64 {
	ix := hi(x) & 0x7fffffff
	if ix <= 0x3fe921fb {
		return kernelCos(x, 0)
	}
	if ix >= 0x7ff00000 {
		return x - x
	}
	n, y0, y1 := remPio2(x)
	switch n & 3 {
	case 0:
		return kernelCos(y0, y1)
	case 1:
		return -kernelSin(y0, y1, 1)
	case 2:
		return -kernelCos(y0, y1)
	}
	return kernelSin(y0, y1, 1)
}

func fdTan(x float64) float64 {
	ix := hi(x) & 0x7fffffff
	if ix <= 0x3fe921fb {
		return kernelTan(x, 0, 1)
	}
	if ix >= 0x7ff00000 {
		return x - x
	}
	n, y0, y1 := remPio2(x)
	return kernelTan(y0, y1, 1-((n&1)<<1))
}

// ── asin, acos, atan, atan2 ──────────────────────────────────────────────

const (
	aPio2Hi = 1.57079632679489655800e+00
	aPio2Lo = 6.12323399573676603587e-17
	aPio4Hi = 7.85398163397448278999e-01
	aPi     = 3.14159265358979311600e+00
	pS0     = 1.66666666666666657415e-01
	pS1     = -3.25565818622400915405e-01
	pS2     = 2.01212532134862925881e-01
	pS3     = -4.00555345006794114027e-02
	pS4     = 7.91534994289814532176e-04
	pS5     = 3.47933107596021167570e-05
	qS1     = -2.40339491173441421878e+00
	qS2     = 2.02094576023350569471e+00
	qS3     = -6.88283971605453293030e-01
	qS4     = 7.70381505559019352791e-02
)

func fdAsin(x float64) float64 {
	hx := hi(x)
	ix := hx & 0x7fffffff
	if ix >= 0x3ff00000 {
		if uint32(ix-0x3ff00000)|lo(x) == 0 {
			return float64(x*aPio2Hi) + float64(x*aPio2Lo)
		}
		return (x - x) / (x - x)
	}
	if ix < 0x3fe00000 {
		if ix < 0x3e400000 {
			return x
		}
		t := float64(x * x)
		p := float64(t * (pS0 + float64(t*(pS1+float64(t*(pS2+float64(t*(pS3+float64(t*(pS4+float64(t*pS5)))))))))))
		q := 1 + float64(t*(qS1+float64(t*(qS2+float64(t*(qS3+float64(t*qS4)))))))
		return x + float64(x*(p/q))
	}
	w := 1 - math.Abs(x)
	t := float64(w * 0.5)
	p := float64(t * (pS0 + float64(t*(pS1+float64(t*(pS2+float64(t*(pS3+float64(t*(pS4+float64(t*pS5)))))))))))
	q := 1 + float64(t*(qS1+float64(t*(qS2+float64(t*(qS3+float64(t*qS4)))))))
	s := math.Sqrt(t)
	if ix >= 0x3FEF3333 {
		w = p / q
		t = aPio2Hi - (float64(2.0*(s+float64(s*w))) - aPio2Lo)
	} else {
		w = withLow(s, 0)
		c := (t - float64(w*w)) / (s + w)
		r := p / q
		p = float64(float64(2.0*s)*r) - (aPio2Lo - float64(2.0*c))
		q = aPio4Hi - float64(2.0*w)
		t = aPio4Hi - (p - q)
	}
	if hx > 0 {
		return t
	}
	return -t
}

func fdAcos(x float64) float64 {
	hx := hi(x)
	ix := hx & 0x7fffffff
	if ix >= 0x3ff00000 {
		if uint32(ix-0x3ff00000)|lo(x) == 0 {
			if hx > 0 {
				return 0
			}
			return aPi + 2.0*aPio2Lo
		}
		return (x - x) / (x - x)
	}
	if ix < 0x3fe00000 {
		if ix <= 0x3c600000 {
			return aPio2Hi + aPio2Lo
		}
		z := float64(x * x)
		p := float64(z * (pS0 + float64(z*(pS1+float64(z*(pS2+float64(z*(pS3+float64(z*(pS4+float64(z*pS5)))))))))))
		q := 1 + float64(z*(qS1+float64(z*(qS2+float64(z*(qS3+float64(z*qS4)))))))
		r := p / q
		return aPio2Hi - (x - (aPio2Lo - float64(x*r)))
	}
	if hx < 0 {
		z := float64((1 + x) * 0.5)
		p := float64(z * (pS0 + float64(z*(pS1+float64(z*(pS2+float64(z*(pS3+float64(z*(pS4+float64(z*pS5)))))))))))
		q := 1 + float64(z*(qS1+float64(z*(qS2+float64(z*(qS3+float64(z*qS4)))))))
		s := math.Sqrt(z)
		r := p / q
		w := float64(r*s) - aPio2Lo
		return aPi - float64(2.0*(s+w))
	}
	z := float64((1 - x) * 0.5)
	s := math.Sqrt(z)
	df := withLow(s, 0)
	c := (z - float64(df*df)) / (s + df)
	p := float64(z * (pS0 + float64(z*(pS1+float64(z*(pS2+float64(z*(pS3+float64(z*(pS4+float64(z*pS5)))))))))))
	q := 1 + float64(z*(qS1+float64(z*(qS2+float64(z*(qS3+float64(z*qS4)))))))
	r := p / q
	w := float64(r*s) + c
	return float64(2.0 * (df + w))
}

var atanHi = [...]float64{
	4.63647609000806093515e-01,
	7.85398163397448278999e-01,
	9.82793723247329054082e-01,
	1.57079632679489655800e+00,
}

var atanLo = [...]float64{
	2.26987774529616870924e-17,
	3.06161699786838301793e-17,
	1.39033110312309984516e-17,
	6.12323399573676603587e-17,
}

var aT = [...]float64{
	3.33333333333329318027e-01,
	-1.99999999998764832476e-01,
	1.42857142725034663711e-01,
	-1.11111104054623557880e-01,
	9.09088713343650656196e-02,
	-7.69187620504482999495e-02,
	6.66107313738753120669e-02,
	-5.83357013379057348645e-02,
	4.97687799461593236017e-02,
	-3.65315727442169155270e-02,
	1.62858201153657823623e-02,
}

func fdAtan(x float64) float64 {
	hx := hi(x)
	ix := hx & 0x7fffffff
	id := -1
	if ix >= 0x44100000 {
		if ix > 0x7ff00000 || ix == 0x7ff00000 && lo(x) != 0 {
			return x + x
		}
		if hx > 0 {
			return atanHi[3] + atanLo[3]
		}
		return -atanHi[3] - atanLo[3]
	}
	if ix < 0x3fdc0000 {
		if ix < 0x3e400000 {
			return x
		}
	} else {
		x = math.Abs(x)
		if ix < 0x3ff30000 {
			if ix < 0x3fe60000 {
				id = 0
				x = (float64(2.0*x) - 1) / (2.0 + x)
			} else {
				id = 1
				x = (x - 1) / (x + 1)
			}
		} else if ix < 0x40038000 {
			id = 2
			x = (x - 1.5) / (1 + float64(1.5*x))
		} else {
			id = 3
			x = -1.0 / x
		}
	}
	z := float64(x * x)
	w := float64(z * z)
	s1 := float64(z * (aT[0] + float64(w*(aT[2]+float64(w*(aT[4]+float64(w*(aT[6]+float64(w*(aT[8]+float64(w*aT[10])))))))))))
	s2 := float64(w * (aT[1] + float64(w*(aT[3]+float64(w*(aT[5]+float64(w*(aT[7]+float64(w*aT[9])))))))))
	if id < 0 {
		return x - float64(x*(s1+s2))
	}
	z = atanHi[id] - ((float64(x*(s1+s2)) - atanLo[id]) - x)
	if hx < 0 {
		return -z
	}
	return z
}

const (
	a2PiO4 = 7.8539816339744827900e-01
	a2PiO2 = 1.5707963267948965580e+00
	a2Pi   = 3.1415926535897931160e+00
	a2PiLo = 1.2246467991473531772e-16
)

func fdAtan2(y, x float64) float64 {
	if math.IsNaN(x) || math.IsNaN(y) {
		return x + y
	}
	hx, lx := hi(x), lo(x)
	hy, ly := hi(y), lo(y)
	ix, iy := hx&0x7fffffff, hy&0x7fffffff
	if uint32(hx-0x3ff00000)|lx == 0 {
		return fdAtan(y)
	}
	m := ((hy >> 31) & 1) | ((hx >> 30) & 2)
	if uint32(iy)|ly == 0 {
		switch m {
		case 0, 1:
			return y
		case 2:
			return a2Pi + fdTiny
		}
		return -a2Pi - fdTiny
	}
	if uint32(ix)|lx == 0 {
		if hy < 0 {
			return -a2PiO2 - fdTiny
		}
		return a2PiO2 + fdTiny
	}
	if ix == 0x7ff00000 {
		if iy == 0x7ff00000 {
			switch m {
			case 0:
				return a2PiO4 + fdTiny
			case 1:
				return -a2PiO4 - fdTiny
			case 2:
				return 3.0*a2PiO4 + fdTiny
			}
			return -3.0*a2PiO4 - fdTiny
		}
		switch m {
		case 0:
			return 0
		case 1:
			return math.Copysign(0, -1)
		case 2:
			return a2Pi + fdTiny
		}
		return -a2Pi - fdTiny
	}
	if iy == 0x7ff00000 {
		if hy < 0 {
			return -a2PiO2 - fdTiny
		}
		return a2PiO2 + fdTiny
	}
	k := (iy - ix) >> 20
	var z float64
	switch {
	case k > 60:
		z = a2PiO2 + 0.5*a2PiLo
		m &= 1
	case hx < 0 && k < -60:
		z = 0
	default:
		z = fdAtan(math.Abs(y / x))
	}
	switch m {
	case 0:
		return z
	case 1:
		return -z
	case 2:
		return a2Pi - (z - a2PiLo)
	}
	return (z - a2PiLo) - a2Pi
}

// ── exp, log, log2, log10 ────────────────────────────────────────────────

const (
	eOThreshold = 7.09782712893383973096e+02
	eUThreshold = -7.45133219101941108420e+02
	eLn2Hi      = 6.93147180369123816490e-01
	eLn2Lo      = 1.90821492927058770002e-10
	eInvln2     = 1.44269504088896338700e+00
	eP1         = 1.66666666666666019037e-01
	eP2         = -2.77777777770155933842e-03
	eP3         = 6.61375632143793436117e-05
	eP4         = -1.65339022054652515390e-06
	eP5         = 4.13813679705723846039e-08
	eTwom1000   = 9.33263618503218878990e-302
)

func fdExp(x float64) float64 {
	hx := uint32(hi(x))
	xsb := int((hx >> 31) & 1)
	hx &= 0x7fffffff
	if hx >= 0x40862E42 {
		if hx >= 0x7ff00000 {
			if (hx&0xfffff)|lo(x) != 0 {
				return x + x
			}
			if xsb == 0 {
				return x
			}
			return 0
		}
		if x > eOThreshold {
			return float64(fdHuge * fdHuge)
		}
		if x < eUThreshold {
			return eTwom1000 * eTwom1000
		}
	}
	var hiPart, loPart float64
	k := 0
	if hx > 0x3fd62e42 {
		if hx < 0x3FF0A2B2 {
			// V8 returns E for exp(1): the reduction below gets its last bit
			// wrong.
			if x == 1.0 {
				return 2.718281828459045
			}
			if xsb == 0 {
				hiPart, loPart, k = x-eLn2Hi, eLn2Lo, 1
			} else {
				hiPart, loPart, k = x+eLn2Hi, -eLn2Lo, -1
			}
		} else {
			half := 0.5
			if xsb == 1 {
				half = -0.5
			}
			k = int(float64(eInvln2*x) + half)
			t := float64(k)
			hiPart = x - float64(t*eLn2Hi)
			loPart = float64(t * eLn2Lo)
		}
		x = hiPart - loPart
	} else if hx < 0x3e300000 {
		return 1 + x
	}
	t := float64(x * x)
	c := x - float64(t*(eP1+float64(t*(eP2+float64(t*(eP3+float64(t*(eP4+float64(t*eP5)))))))))
	if k == 0 {
		return 1 - ((float64(x*c))/(c-2.0) - x)
	}
	y := 1 - ((loPart - (float64(x*c))/(2.0-c)) - hiPart)
	if k >= -1021 {
		if k == 1024 {
			return float64(float64(y*2.0) * math.Ldexp(1, 1023))
		}
		return withHigh(y, hi(y)+int32(k<<20))
	}
	return float64(withHigh(y, hi(y)+int32((k+1000)<<20)) * eTwom1000)
}

const (
	lTwo54 = 1.80143985094819840000e+16
	lg1    = 6.666666666666735130e-01
	lg2    = 3.999999999940941908e-01
	lg3    = 2.857142874366239149e-01
	lg4    = 2.222219843214978396e-01
	lg5    = 1.818357216161805012e-01
	lg6    = 1.531383769920937332e-01
	lg7    = 1.479819860511658591e-01
)

func fdLog(x float64) float64 {
	hx := hi(x)
	lx := lo(x)
	k := int32(0)
	if hx < 0x00100000 {
		if uint32(hx&0x7fffffff)|lx == 0 {
			return math.Inf(-1)
		}
		if hx < 0 {
			return math.NaN()
		}
		k -= 54
		x = float64(x * (lTwo54))
		hx = hi(x)
	}
	if hx >= 0x7ff00000 {
		return x + x
	}
	k += (hx >> 20) - 1023
	hx &= 0x000fffff
	i := (hx + 0x95f64) & 0x100000
	x = withHigh(x, hx|(i^0x3ff00000))
	k += i >> 20
	f := x - 1.0
	if (0x000fffff & (2 + hx)) < 3 {
		if f == 0 {
			if k == 0 {
				return 0
			}
			dk := float64(k)
			return float64(dk*eLn2Hi) + float64(dk*eLn2Lo)
		}
		r := float64(float64(f*f) * (0.5 - float64(0.33333333333333333*f)))
		if k == 0 {
			return f - r
		}
		dk := float64(k)
		return float64(dk*eLn2Hi) - ((r - float64(dk*eLn2Lo)) - f)
	}
	s := f / (2.0 + f)
	dk := float64(k)
	z := float64(s * s)
	i = hx - 0x6147a
	w := float64(z * z)
	j := 0x6b851 - hx
	t1 := float64(w * (lg2 + float64(w*(lg4+float64(w*lg6)))))
	t2 := float64(z * (lg1 + float64(w*(lg3+float64(w*(lg5+float64(w*lg7)))))))
	i |= j
	r := t2 + t1
	if i > 0 {
		hfsq := float64(float64(0.5*f) * f)
		if k == 0 {
			return f - (hfsq - float64(s*(hfsq+r)))
		}
		return float64(dk*eLn2Hi) - ((hfsq - (float64(s*(hfsq+r)) + float64(dk*eLn2Lo))) - f)
	}
	if k == 0 {
		return f - float64(s*(f-r))
	}
	return float64(dk*eLn2Hi) - ((float64(s*(f-r)) - float64(dk*eLn2Lo)) - f)
}

// kLog1p is k_log1p of FreeBSD's k_log.h.
func kLog1p(f float64) float64 {
	s := f / (2.0 + f)
	z := float64(s * s)
	w := float64(z * z)
	t1 := float64(w * (lg2 + float64(w*(lg4+float64(w*lg6)))))
	t2 := float64(z * (lg1 + float64(w*(lg3+float64(w*(lg5+float64(w*lg7)))))))
	r := t2 + t1
	hfsq := float64(float64(0.5*f) * f)
	return float64(s * (hfsq + r))
}

// logParts reduces x for log2 and log10: x = 2^k·(1+f).
func logParts(x float64) (k int32, f float64, special bool, value float64) {
	hx := hi(x)
	lx := lo(x)
	if hx < 0x00100000 {
		if uint32(hx&0x7fffffff)|lx == 0 {
			return 0, 0, true, math.Inf(-1)
		}
		if hx < 0 {
			return 0, 0, true, math.NaN()
		}
		k -= 54
		x = float64(x * (lTwo54))
		hx = hi(x)
	}
	if hx >= 0x7ff00000 {
		return 0, 0, true, x + x
	}
	if hx == 0x3ff00000 && lo(x) == 0 {
		return 0, 0, true, 0
	}
	k += (hx >> 20) - 1023
	hx &= 0x000fffff
	i := (hx + 0x95f64) & 0x100000
	x = withHigh(x, hx|(i^0x3ff00000))
	k += i >> 20
	return k, x - 1.0, false, 0
}

const (
	ivln2hi   = 1.44269504072144627571e+00
	ivln2lo   = 1.67517131648865118353e-10
	ivln10    = 4.34294481903251816668e-01
	log10_2hi = 3.01029995663611771306e-01
	log10_2lo = 3.69423907715893078616e-13
)

func fdLog2(x float64) float64 {
	k, f, special, value := logParts(x)
	if special {
		return value
	}
	y := float64(k)
	hfsq := float64(float64(0.5*f) * f)
	r := kLog1p(f)
	h := withLow(f-hfsq, 0)
	l := (f - h) - hfsq + r
	valHi := float64(h * ivln2hi)
	valLo := float64((l+h)*ivln2lo) + float64(l*ivln2hi)
	w := y + valHi
	valLo += (y - w) + valHi
	valHi = w
	return valLo + valHi
}

// fdLog10 is fdlibm's e_log10.c (V8 does not use the FreeBSD variant).
func fdLog10(x float64) float64 {
	hx := hi(x)
	lx := lo(x)
	k := int32(0)
	if hx < 0x00100000 {
		if uint32(hx&0x7fffffff)|lx == 0 {
			return math.Inf(-1)
		}
		if hx < 0 {
			return math.NaN()
		}
		k -= 54
		x = float64(x * (lTwo54))
		hx = hi(x)
	}
	if hx >= 0x7ff00000 {
		return x + x
	}
	k += (hx >> 20) - 1023
	i := int32((uint32(k) & 0x80000000) >> 31)
	hx = (hx & 0x000fffff) | ((0x3ff - i) << 20)
	y := float64(k + i)
	x = withHigh(x, hx)
	z := float64(y*log10_2lo) + float64(ivln10*fdLog(x))
	return z + float64(y*log10_2hi)
}

// ── Hyperbolic functions ─────────────────────────────────────────────────

func fdSinh(x float64) float64 {
	jx := hi(x)
	ix := jx & 0x7fffffff
	if ix >= 0x7ff00000 {
		return x + x
	}
	h := 0.5
	if jx < 0 {
		h = -h
	}
	if ix < 0x40360000 {
		if ix < 0x3e300000 {
			return x
		}
		t := fdlibmExpm1(math.Abs(x))
		if ix < 0x3ff00000 {
			return float64(h * (float64(2.0*t) - float64(t*t)/(t+1)))
		}
		return float64(h * (t + t/(t+1)))
	}
	if ix < 0x40862E42 {
		return float64(h * fdExp(math.Abs(x)))
	}
	if ix < 0x408633CE || ix == 0x408633ce && lo(x) <= 0x8fb9f87d {
		w := fdExp(float64(0.5 * math.Abs(x)))
		t := float64(h * w)
		return float64(t * w)
	}
	return float64(x * 1.0e307)
}

func fdCosh(x float64) float64 {
	ix := hi(x) & 0x7fffffff
	if ix >= 0x7ff00000 {
		return float64(x * x)
	}
	if ix < 0x3fd62e43 {
		t := fdlibmExpm1(math.Abs(x))
		w := 1 + t
		if ix < 0x3c800000 {
			return w
		}
		return 1 + (float64(t*t))/(w+w)
	}
	if ix < 0x40360000 {
		t := fdExp(math.Abs(x))
		return float64(0.5*t) + 0.5/t
	}
	if ix < 0x40862E42 {
		return float64(0.5 * fdExp(math.Abs(x)))
	}
	if ix < 0x408633CE || ix == 0x408633ce && lo(x) <= 0x8fb9f87d {
		w := fdExp(float64(0.5 * math.Abs(x)))
		t := float64(0.5 * w)
		return float64(t * w)
	}
	return float64(fdHuge * fdHuge)
}

func fdTanh(x float64) float64 {
	jx := hi(x)
	ix := jx & 0x7fffffff
	if ix >= 0x7ff00000 {
		if jx >= 0 {
			return 1/x + 1
		}
		return 1/x - 1
	}
	var z float64
	if ix < 0x40360000 {
		if ix < 0x3e300000 {
			return x
		}
		if ix >= 0x3ff00000 {
			t := fdlibmExpm1(float64(2 * math.Abs(x)))
			z = 1 - 2/(t+2)
		} else {
			t := fdlibmExpm1(float64(-2 * math.Abs(x)))
			z = -t / (t + 2)
		}
	} else {
		z = 1 - fdTiny
	}
	if jx >= 0 {
		return z
	}
	return -z
}

const hLn2 = 6.93147180559945286227e-01

func fdAsinh(x float64) float64 {
	hx := hi(x)
	ix := hx & 0x7fffffff
	if ix >= 0x7ff00000 {
		return x + x
	}
	if ix < 0x3e300000 {
		return x
	}
	var w float64
	switch {
	case ix > 0x41b00000:
		w = fdLog(math.Abs(x)) + hLn2
	case ix > 0x40000000:
		t := math.Abs(x)
		w = fdLog(float64(2.0*t) + 1/(math.Sqrt(float64(x*x)+1)+t))
	default:
		t := float64(x * x)
		w = fdlibmLog1p(math.Abs(x) + t/(1+math.Sqrt(1+t)))
	}
	if hx > 0 {
		return w
	}
	return -w
}

func fdAcosh(x float64) float64 {
	hx := hi(x)
	switch {
	case hx < 0x3ff00000:
		return (x - x) / (x - x)
	case hx >= 0x41b00000:
		if hx >= 0x7ff00000 {
			return x + x
		}
		return fdLog(x) + hLn2
	case uint32(hx-0x3ff00000)|lo(x) == 0:
		return 0
	case hx > 0x40000000:
		t := float64(x * x)
		return fdLog(float64(2.0*x) - 1/(x+math.Sqrt(t-1)))
	}
	t := x - 1
	return fdlibmLog1p(t + math.Sqrt(float64(2.0*t)+float64(t*t)))
}

func fdAtanh(x float64) float64 {
	hx := hi(x)
	lx := lo(x)
	ix := hx & 0x7fffffff
	if uint32(ix)|((lx|-lx)>>31) > 0x3ff00000 {
		return (x - x) / (x - x)
	}
	if ix == 0x3ff00000 {
		return x / fdZero
	}
	if ix < 0x3e300000 {
		return x
	}
	x = withHigh(x, ix)
	var t float64
	if ix < 0x3fe00000 {
		t = x + x
		t = float64(0.5 * fdlibmLog1p(t+float64(t*x)/(1-x)))
	} else {
		t = float64(0.5 * fdlibmLog1p((x+x)/(1-x)))
	}
	if hx >= 0 {
		return t
	}
	return -t
}
