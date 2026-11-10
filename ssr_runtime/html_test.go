package ssr_runtime

import (
	"strings"
	"testing"
)

func TestHTMLTag(t *testing.T) {
	cases := []struct {
		value any
		want  string
	}{
		{"<b>x</b>", "<b>x</b>"},
		{nil, ""},
		{Undefined(), ""},
		{3.5, "3.5"},
		{true, "true"},
	}
	for _, c := range cases {
		var b strings.Builder
		if err := WriteHTMLTag(&b, c.value); err != nil {
			t.Fatal(err)
		}
		want := "<!--[h" + HTMLHash(c.want) + "-->" + c.want + "<!--]-->"
		if b.String() != want {
			t.Errorf("WriteHTMLTag(%#v) = %q, want %q", c.value, b.String(), want)
		}
	}
	var static strings.Builder
	sw, root := BeginComponent(StaticWriter(&static), "Page", nil)
	if err := WriteHTMLTag(sw, "<i>a</i>"); err != nil {
		t.Fatal(err)
	}
	if err := EndComponent(sw, root, nil); err != nil {
		t.Fatal(err)
	}
	if static.String() != "<i>a</i>" {
		t.Fatalf("static output = %q", static.String())
	}
}

// TestHTMLHash pins the hash the hydration runtime computes over UTF-16
// code units (runtime-hydrate.js htmlHash).
func TestHTMLHash(t *testing.T) {
	cases := map[string]string{
		"":         "ztntfp",
		"<b>x</b>": "1ly7s24",
		"emoji 😀":  "1lifl7u",
		"ünï":      "4xltcw",
		// A lone surrogate is written, and hashed, as U+FFFD.
		HTMLTagString(string([]byte{0xed, 0xa0, 0x80})): HTMLHash("\ufffd"),
	}
	for in, want := range cases {
		if got := HTMLHash(in); got != want {
			t.Errorf("HTMLHash(%q) = %q, want %q", in, got, want)
		}
	}
}
