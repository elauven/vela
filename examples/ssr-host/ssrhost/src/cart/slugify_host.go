// Package cart holds the host implementations for functions declared in
// src/Cart.vela (src/Cart.vela → ssrhost/src/cart).
package cart

import (
	"regexp"
	"strings"
)

var (
	nonAlnum   = regexp.MustCompile(`[^a-z0-9]+`)
	edgeDashes = regexp.MustCompile(`^-|-$`)
)

// Slugify is the server implementation of slugify in src/Cart.vela.
//
//vela:host slugify
func Slugify(text string) string {
	return edgeDashes.ReplaceAllString(nonAlnum.ReplaceAllString(strings.ToLower(text), "-"), "")
}
