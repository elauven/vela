// Package source holds positions in .vela component source shared by the
// compiler layers.
package source

// SectionSpan is an immutable half-open byte range in the full .vela source.
type SectionSpan struct {
	Start int
	End   int
}
