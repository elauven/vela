package main

import (
	"testing"
)

func TestIsGlobalSelector(t *testing.T) {
	global := []string{
		":root",
		":root .child",
		":root > div",
		":root:hover",
		"html",
		"body",
		"body.dark",
		"*",
		"* > div",
		"@keyframes fadeIn",
		"@font-face",
		"@media (max-width: 768px)",
	}
	for _, sel := range global {
		if !isGlobalSelector(sel) {
			t.Errorf("expected %q to be global, got scoped", sel)
		}
	}

	local := []string{
		".card",
		"#header",
		"div",
		"span.brand",
		".navbar:hover",
		".card::before",
		"[data-active]",
	}
	for _, sel := range local {
		if isGlobalSelector(sel) {
			t.Errorf("expected %q to be local (scopeable), got global", sel)
		}
	}
}

func TestScopeSelector(t *testing.T) {
	id := "vela-a1b2c3"
	cases := []struct {
		in   string
		want string
	}{
		// Normal selectors — get scoped
		{".card", ".card[vela-a1b2c3]"},
		{".card:hover", ".card[vela-a1b2c3]:hover"},
		{".card::before", ".card[vela-a1b2c3]::before"},
		{".card::after", ".card[vela-a1b2c3]::after"},
		{"div.item", "div.item[vela-a1b2c3]"},
		{"#header", "#header[vela-a1b2c3]"},

		// :root — must stay global
		{":root", ":root"},
		{":root > div", ":root > div"},

		// html / body — must stay global
		{"html", "html"},
		{"body", "body"},
		{"body.dark", "body.dark"},
		{"*", "*"},

		// :global() wrapper — unwrap and leave unscoped
		{":global(body.dark)", "body.dark"},
		{":global(*, *::before, *::after)", "*, *::before, *::after"},
		{":global(body)", "body"},

		// Compound with :global() — scope the local part
		{".card :global(a)", ".card[vela-a1b2c3] a"},

		// At-rules — pass through
		{"@keyframes fadeIn", "@keyframes fadeIn"},
		{"@font-face", "@font-face"},
	}

	for _, c := range cases {
		got := scopeSelector(c.in, id)
		if got != c.want {
			t.Errorf("scopeSelector(%q):\n  got  %q\n  want %q", c.in, got, c.want)
		}
	}
}

func TestInsertScopeAttr(t *testing.T) {
	id := "vela-x"
	cases := []struct{ in, want string }{
		{".card", ".card[vela-x]"},
		{".card:hover", ".card[vela-x]:hover"},
		{".card::before", ".card[vela-x]::before"},
		{".card::after", ".card[vela-x]::after"},
		{".card::placeholder", ".card[vela-x]::placeholder"},
		// Pseudo-classes stay AFTER the scope attr
		{".card:first-child", ".card[vela-x]:first-child"},
		{".card:nth-child(2)", ".card[vela-x]:nth-child(2)"},
	}
	for _, c := range cases {
		got := insertScopeAttr(c.in, id)
		if got != c.want {
			t.Errorf("insertScopeAttr(%q): got %q, want %q", c.in, got, c.want)
		}
	}
}
