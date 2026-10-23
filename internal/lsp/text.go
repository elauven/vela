package lsp

import (
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// text is a document with its line starts, for conversions between byte
// offsets and LSP positions.
type text struct {
	src   string
	lines []int // byte offset of each line start
}

func newText(src string) *text {
	t := &text{src: src, lines: []int{0}}
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			t.lines = append(t.lines, i+1)
		}
	}
	return t
}

// offset is the byte offset of p, clamped to the document.
func (t *text) offset(p Position) int {
	if p.Line < 0 {
		return 0
	}
	if p.Line >= len(t.lines) {
		return len(t.src)
	}
	at := t.lines[p.Line]
	end := len(t.src)
	if p.Line+1 < len(t.lines) {
		end = t.lines[p.Line+1] - 1
		if end > at && t.src[end-1] == '\r' {
			end-- // \r\n ends the line
		}
	}
	for units := 0; at < end && units < p.Character; {
		r, size := utf8.DecodeRuneInString(t.src[at:])
		units += utf16.RuneLen(r)
		at += size
	}
	return at
}

// position is the LSP position of the byte offset off.
func (t *text) position(off int) Position {
	off = min(max(off, 0), len(t.src))
	line := 0
	for lo, hi := 0, len(t.lines)-1; lo <= hi; {
		mid := (lo + hi) / 2
		if t.lines[mid] <= off {
			line, lo = mid, mid+1
		} else {
			hi = mid - 1
		}
	}
	return Position{Line: line, Character: len(utf16.Encode([]rune(t.src[t.lines[line]:off])))}
}

func (t *text) rangeOf(start, end int) Range {
	return Range{Start: t.position(start), End: t.position(end)}
}

// apply applies an incremental change: the text replacing r, or the whole
// document without a range.
func (t *text) apply(r *Range, change string) *text {
	if r == nil {
		return newText(change)
	}
	start, end := t.offset(r.Start), t.offset(r.End)
	if end < start {
		start, end = end, start
	}
	return newText(t.src[:start] + change + t.src[end:])
}

// uriToPath is the file path of a file URI, "" for another scheme.
func uriToPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return ""
	}
	path := u.Path
	// file:///C:/dir → C:/dir
	if runtime.GOOS == "windows" && len(path) > 2 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	return filepath.Clean(filepath.FromSlash(path))
}

func pathToURI(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	slashed := filepath.ToSlash(path)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	return (&url.URL{Scheme: "file", Path: slashed}).String()
}

func isIdentByte(c byte) bool {
	return c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 0x80
}

// wordAt is the identifier around the byte offset off: [start, end).
func wordAt(src string, off int) (start, end int) {
	start, end = off, off
	for start > 0 && isIdentByte(src[start-1]) {
		start--
	}
	for end < len(src) && isIdentByte(src[end]) {
		end++
	}
	return start, end
}
