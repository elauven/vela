package ssr_runtime

import (
	"io"
	"strconv"
	"unicode/utf8"
)

// HTMLTagString is the markup {@html value} inserts (task 100): nothing for
// null and undefined, JavaScript String(value) for any other value. The
// markup is not sanitized.
func HTMLTagString(value any) string {
	switch value.(type) {
	case nil, UndefinedValue:
		return ""
	}
	s := String(value)
	if !utf8.ValidString(s) {
		// Lone surrogates are written as U+FFFD (T-6.3b).
		s = WellFormed(s)
	}
	return s
}

// HTMLHash is the hash of {@html} markup on its hydration marker: 32-bit
// FNV-1a over the UTF-16 code units, in base 36. The hydration runtime
// computes the same value of the client string (runtime-hydrate.js
// htmlHash).
func HTMLHash(markup string) string {
	hash := uint32(0x811c9dc5)
	for _, r := range markup {
		if r >= 0x10000 {
			r -= 0x10000
			hash = (hash ^ uint32(0xd800+(r>>10))) * 0x01000193
			hash = (hash ^ uint32(0xdc00+(r&0x3ff))) * 0x01000193
			continue
		}
		hash = (hash ^ uint32(r)) * 0x01000193
	}
	return strconv.FormatUint(uint64(hash), 36)
}

// WriteHTMLTag writes {@html value} (task 100): the markup unescaped in the
// hydration range <!--[h{hash}-->markup<!--]-->, or alone in a static
// render. The payload records nothing; the client adopts the nodes and
// compares the hash.
func WriteHTMLTag(w io.Writer, value any) error {
	markup := HTMLTagString(value)
	if sw, ok := w.(*streamWriter); !ok || !sw.static {
		if err := OpenRange(w, "h"+HTMLHash(markup)); err != nil {
			return err
		}
	}
	if err := WriteString(w, markup); err != nil {
		return err
	}
	return CloseRange(w)
}
