package jsast

import (
	"math/rand"
	"strings"
	"testing"
	"time"
)

// Robustness of TypeScript parsing (task 080): whatever the input, the
// parser must not panic or hang, every node keeps a valid span, and
// speculative parsing must stay close to linear. The language server parses
// half-written TypeScript on every keystroke.

func tsCorpusSources() []string {
	var out []string
	for _, c := range tsValidCases {
		out = append(out, c.ts)
	}
	for _, c := range tsInvalidCases {
		out = append(out, c.ts)
	}
	return out
}

func tsNoCrash(t *testing.T, src string) bool {
	t.Helper()
	done := make(chan ctParsed, 1)
	go func() { done <- tsParse(src) }()
	limit := 3 * time.Second
	if raceEnabled {
		limit *= 10
	}
	select {
	case r := <-done:
		if r.panic != nil {
			t.Errorf("parser panicked on %q: %v", ctShorten(src), r.panic)
			return false
		}
		var bad []string
		var visit func(n JsNode)
		visit = func(n JsNode) {
			if n.NodePos() < 0 || n.NodeEnd() < n.NodePos() || n.NodeEnd() > len(src) {
				bad = append(bad, ctTypeName(n))
			}
			for _, c := range ctChildren(n) {
				visit(c)
			}
		}
		visit(r.prog)
		if len(bad) > 0 {
			t.Errorf("invalid spans (%v) for %q", bad, ctShorten(src))
			return false
		}
		return true
	case <-time.After(limit):
		t.Errorf("parser hung on %q", ctShorten(src))
		return false
	}
}

func TestTSMutatedInputNeverCrashes(t *testing.T) {
	corpus := tsCorpusSources()
	rng := rand.New(rand.NewSource(20261105))
	frags := []string{"<", ">", ">>", "<<", ":", "?", "!", "=>", "(", ")", "[", "]", "{", "}", ",", ";", "|", "&",
		"as", "satisfies", "type", "interface", "declare", "abstract", "enum", "namespace", "module", "keyof",
		"typeof", "infer", "extends", "implements", "readonly", "private", "public", "import", "export", "new",
		"`", "${", "'", "/", "\n", " ", "this", "is", "asserts", "unique", "const", "...", "=", "@"}
	failures := 0
	for iter := 0; iter < 8000 && failures < 5; iter++ {
		src := corpus[rng.Intn(len(corpus))]
		for m := 0; m < 1+rng.Intn(3); m++ {
			pos := rng.Intn(len(src) + 1)
			switch rng.Intn(4) {
			case 0:
				src = src[:pos] + frags[rng.Intn(len(frags))] + src[pos:]
			case 1:
				end := pos + rng.Intn(6)
				if end > len(src) {
					end = len(src)
				}
				src = src[:pos] + src[end:]
			case 2:
				end := pos + rng.Intn(12)
				if end > len(src) {
					end = len(src)
				}
				src = src[:end] + src[pos:end] + src[end:]
			case 3:
				if pos < len(src) {
					src = src[:pos] + frags[rng.Intn(len(frags))] + src[pos+1:]
				}
			}
		}
		if !tsNoCrash(t, src) {
			failures++
			t.Logf("reproduce with: %q", src)
		}
	}
}

func TestTSTruncatedInputNeverCrashes(t *testing.T) {
	for _, src := range tsCorpusSources() {
		for i := 0; i <= len(src); i++ {
			if !tsNoCrash(t, src[:i]) {
				return
			}
		}
	}
}

func TestTSPathologicalShapes(t *testing.T) {
	rep := strings.Repeat
	cases := []struct{ name, src string }{
		// Without the failure memo (tsparser.go) the `<` chain was
		// quadratic: 3000 comparisons took seconds.
		{"comparison chain", "a" + rep(" < a", 20000)},
		{"comparison alternation", "a" + rep(" < b > c", 8000)},
		{"nested type arguments", "let x: " + rep("A<", 3000) + "B" + rep(">", 3000)},
		{"unclosed type arguments", "f" + rep("<A", 5000)},
		{"nested parenthesized types", "let x: " + rep("(", 5000) + "X" + rep(")", 5000)},
		{"nested invalid parameter types", "let x: " + rep("(a: ", 3000) + "X" + rep(")", 3000)},
		{"nested function types", "let x: " + rep("(a: ", 2000) + "X" + rep(") => X", 2000)},
		{"nested typed arrows", "x = " + rep("(a: ", 3000) + "1" + rep(")", 3000)},
		{"generic arrow chain", "x = " + rep("<T>(a: T) => ", 3000) + "1"},
		{"conditional chain with parens", rep("a ? (b) : ", 5000) + "c"},
		{"conditional types", "type T = " + rep("A extends B ? C : ", 3000) + "D"},
		{"nested object types", "let x: " + rep("{ a: ", 3000) + "X" + rep(" }", 3000)},
		{"nested tuples", "let x: " + rep("[", 5000) + "X" + rep("]", 5000)},
		{"long union", "let x: A" + rep(" | A", 30000)},
		{"many interfaces", rep("interface I { a: number; b(): void }\n", 5000)},
		{"many annotated lets", rep("let a: Array<Map<string, number>> = x as any;\n", 5000)},
		{"non-null chain", "a" + rep("!", 20000)},
		{"as chain", "a" + rep(" as T", 20000)},
		{"class with typed members", "class A {" + rep("private x?: number; m<T>(a: T): T { return a } ", 3000) + "}"},
		{"nested template types", "type T = " + rep("`${", 100) + "string" + rep("}`", 100)},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			limit := 2500 * time.Millisecond
			if raceEnabled {
				limit *= 10
			}
			start := time.Now()
			tsNoCrash(t, c.src)
			if d := time.Since(start); d > limit {
				t.Errorf("took %v (possible super-linear behaviour)", d)
			}
		})
	}
}

func FuzzTSParse(f *testing.F) {
	for _, s := range tsCorpusSources() {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		tsNoCrash(t, src)
	})
}
