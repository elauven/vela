// Package format holds the host implementations for src/format.js: the
// package path mirrors the JavaScript file (src/format.js → ssrhost/src/format).
package format

import (
	"fmt"
	"math"
	"strings"
)

// FormatPrice is the server implementation of formatPrice in src/format.js.
// It must return what the JavaScript function returns in the browser for
// every input it receives; this example supports en-US with USD and EUR.
//
//vela:host formatPrice
func FormatPrice(value float64, currency string) (string, error) {
	symbol, ok := map[string]string{"USD": "$", "EUR": "€"}[currency]
	if !ok {
		return "", fmt.Errorf("formatPrice: unsupported currency %q", currency)
	}
	sign := ""
	if value < 0 {
		sign, value = "-", -value
	}
	cents := int64(math.Round(value * 100))
	return sign + symbol + groupThousands(cents/100) + fmt.Sprintf(".%02d", cents%100), nil
}

// groupThousands writes 1234567 as 1,234,567.
func groupThousands(n int64) string {
	digits := fmt.Sprint(n)
	var b strings.Builder
	for i, d := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(d)
	}
	return b.String()
}
