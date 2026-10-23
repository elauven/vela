package lsp

import (
	"path/filepath"
	"testing"
)

func TestPositionsCountUTF16(t *testing.T) {
	src := "a🙂b\r\nсé\n\nend"
	tx := newText(src)
	for _, c := range []struct {
		off int
		pos Position
	}{
		{0, Position{0, 0}},
		{len("a🙂"), Position{0, 3}}, // a surrogate pair is two units
		{len("a🙂b\r\n"), Position{1, 0}},
		{len("a🙂b\r\nс"), Position{1, 1}},
		{len("a🙂b\r\nсé\n\n"), Position{3, 0}},
		{len(src), Position{3, 3}},
	} {
		if got := tx.position(c.off); got != c.pos {
			t.Errorf("position(%d) = %+v, want %+v", c.off, got, c.pos)
		}
		if got := tx.offset(c.pos); got != c.off {
			t.Errorf("offset(%+v) = %d, want %d", c.pos, got, c.off)
		}
	}
	// Past the end of a line or the document, positions clamp.
	if got := tx.offset(Position{0, 99}); got != len("a🙂b") {
		t.Errorf("past the line: %d", got)
	}
	if got := tx.offset(Position{9, 0}); got != len(src) {
		t.Errorf("past the end: %d", got)
	}
	changed := tx.apply(&Range{Start: Position{0, 1}, End: Position{0, 3}}, "x")
	if changed.src != "axb\r\nсé\n\nend" {
		t.Errorf("apply: %q", changed.src)
	}
}

func TestURIs(t *testing.T) {
	path, err := filepath.Abs(filepath.Join("dir with space", "App.vela"))
	if err != nil {
		t.Fatal(err)
	}
	uri := pathToURI(path)
	if got := uriToPath(uri); got != path {
		t.Errorf("round trip of %s: %s via %s", path, got, uri)
	}
	if uriToPath("untitled:Untitled-1") != "" {
		t.Error("a non-file URI has a path")
	}
}
