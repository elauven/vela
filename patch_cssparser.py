#!/usr/bin/env python3
"""
patch_cssparser.py  — Fix CSS scoping bugs in cssparser.go.

Run from the vela compiler directory:
    python3 patch_cssparser.py [cssparser.go] [main.go]

Fixes applied:
  1. :root / html / body / * never get a component scope attribute
  2. Scope attribute inserted before :pseudo (not just ::pseudo-element)
  3. Scope ID uses relative path (stable across machines)
"""
import re, os, sys

css_file  = sys.argv[1] if len(sys.argv) > 1 else "cssparser.go"
main_file = sys.argv[2] if len(sys.argv) > 2 else "main.go"

if not os.path.exists(css_file):
    print(f"ERROR: {css_file} not found."); sys.exit(1)

# ── Append helpers if not already present ──────────────────────────────────
css_src = open(css_file).read()

HELPERS = r'''
// ── Scope helpers injected by patch_cssparser.py ─────────────────────────

// isGlobalSelector returns true for selectors that must never be scoped.
func isGlobalSelector(sel string) bool {
	s := strings.TrimSpace(sel)
	if s == "" || strings.HasPrefix(s, "@") { return true }
	for _, pfx := range []string{":root", ":root ", ":root:", ":root.", ":root#", ":root[", ":root>", ":root+", ":root~"} {
		if s == pfx || (len(pfx) > 5 && strings.HasPrefix(s, pfx)) { return true }
	}
	if s == ":root" { return true }
	end := strings.IndexAny(s, " >+~.#[:(")
	first := s; if end >= 0 { first = s[:end] }
	return first == "html" || first == "body" || first == "*"
}

// insertScopeAttr inserts [id] before the first : pseudo, respecting ( ) [ ].
func insertScopeAttr(sel, id string) string {
	attr := "[" + id + "]"
	d, b := 0, 0
	for i, c := range sel {
		switch c {
		case '(': d++
		case ')': d--
		case '[': b++
		case ']': b--
		case ':':
			if d == 0 && b == 0 { return sel[:i] + attr + sel[i:] }
		}
	}
	return sel + attr
}

// scopeOneSel scopes a single selector token, handling :global() and globals.
func scopeOneSel(sel, scopeID string) string {
	s := strings.TrimSpace(sel)
	if strings.HasPrefix(s, "@") { return s }
	if strings.HasPrefix(s, ":global(") && strings.HasSuffix(s, ")") {
		return strings.TrimSpace(s[8 : len(s)-1])
	}
	if isGlobalSelector(s) { return s }
	if !strings.Contains(s, ":global(") { return insertScopeAttr(s, scopeID) }
	// Inline :global() — scope parts outside it
	var out strings.Builder
	rem := s
	for {
		idx := strings.Index(rem, ":global(")
		if idx < 0 {
			if t := strings.TrimSpace(rem); t != "" {
				if isGlobalSelector(t) { out.WriteString(t) } else { out.WriteString(insertScopeAttr(t, scopeID)) }
			}
			break
		}
		if before := strings.TrimSpace(rem[:idx]); before != "" {
			if isGlobalSelector(before) { out.WriteString(before) } else { out.WriteString(insertScopeAttr(before, scopeID)) }
			out.WriteString(" ")
		}
		rest := rem[idx+8:]; depth := 1; i := 0
		for i < len(rest) {
			if rest[i] == '(' { depth++ } else if rest[i] == ')' { depth--; if depth == 0 { break } }; i++
		}
		out.WriteString(rest[:i]); rem = rest[i+1:]
	}
	return strings.TrimSpace(out.String())
}
'''

if 'func isGlobalSelector' not in css_src:
    open(css_file, 'a').write(HELPERS)
    print("  ✓ Added isGlobalSelector / scopeOneSel / insertScopeAttr helpers")
    css_src = open(css_file).read()
else:
    print("  ~ Helpers already present")

# ── Find and replace the scoping expression ───────────────────────────────
css_src = open(css_file).read()
changed = False

# Try several patterns
pats = [
    (r'(\w+)\s*\+\s*"\["\s*\+\s*(\w+)\s*\+\s*"\]"', lambda m: f'scopeOneSel({m.group(1)}, {m.group(2)})'),
    (r'fmt\.Sprintf\("%s\[%s\]",\s*(\w+),\s*(\w+)\)', lambda m: f'scopeOneSel({m.group(1)}, {m.group(2)})'),
    (r'(\w+)\s*\+=\s*"\["\s*\+\s*(\w+)\s*\+\s*"\]"', lambda m: f'{m.group(1)} = scopeOneSel({m.group(1)}, {m.group(2)})'),
    (r'fmt\.Sprintf\(`%s\[%s\]`,\s*(\w+),\s*(\w+)\)', lambda m: f'scopeOneSel({m.group(1)}, {m.group(2)})'),
]
for pat, repl in pats:
    m = re.search(pat, css_src)
    if m:
        old = m.group()
        new = repl(m)
        css_src = css_src.replace(old, new, 1)
        open(css_file, 'w').write(css_src)
        print(f"  ✓ Replaced scoping call: {repr(old)[:60]} → {repr(new)[:60]}")
        changed = True
        break

if not changed:
    print("  ✗ Could not auto-patch. Lines with scope attribute:")
    for i, l in enumerate(css_src.split('\n')):
        if ('[' in l or 'scope' in l.lower()) and ('+' in l or 'Sprintf' in l) and '//' not in l.strip()[:2]:
            print(f"    {i+1}: {l.rstrip()}")
    print("\n  MANUAL: replace expression with: scopeOneSel(selector, scopeID)")

# ── Fix scope ID to use relative path ─────────────────────────────────────
if os.path.exists(main_file):
    ms = open(main_file).read()
    lines = [(i+1, l) for i, l in enumerate(ms.split('\n'))
             if ('h.Write' in l or 'hash' in l.lower()) and ('path' in l.lower() or 'src' in l.lower())]
    if lines:
        print("\n  Scope ID hash lines in main.go:")
        for n, l in lines[:6]: print(f"    {n}: {l.rstrip()}")
        print("""
  MANUAL FIX — change hash input to relative path:
    rel, _ := filepath.Rel(rootDir, srcFile)
    if rel == "" { rel = filepath.Base(srcFile) }
    h.Write([]byte(filepath.ToSlash(rel)))
""")

print("\nDone. Rebuild: go build -o vela .")
