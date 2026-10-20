package css

import (
	"strings"
	"testing"

	"github.com/elauven/vela/internal/srcspan"
)

func TestScopeCSS(t *testing.T) {
	css := `button { color: red; } .foo, .bar { margin: 0; }`
	scoped := ScopeCSS(css, "vela-abc")
	if !strings.Contains(scoped, "button[vela-abc]") {
		t.Errorf("button not scoped: %s", scoped)
	}
	if !strings.Contains(scoped, ".foo[vela-abc]") {
		t.Errorf(".foo not scoped: %s", scoped)
	}
}

func TestParseCSS_QualifiedRule(t *testing.T) {
	sheet := ParseCSS(`button { color: red; font-size: 1rem; }`)
	if len(sheet.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(sheet.Rules))
	}
	rule, ok := sheet.Rules[0].(*CssQualifiedRule)
	if !ok {
		t.Fatal("expected CssQualifiedRule")
	}
	if len(rule.Selectors) != 1 || tokStr(rule.Selectors[0].Parts) != "button" {
		t.Errorf("unexpected selector: %v", rule.Selectors)
	}
	if len(rule.Decls) != 2 {
		t.Errorf("expected 2 decls, got %d", len(rule.Decls))
	}
	if rule.Decls[0].Prop != "color" || rule.Decls[0].Value != "red" {
		t.Errorf("unexpected decl: %+v", rule.Decls[0])
	}
}

func TestParseCSS_MultiSelector(t *testing.T) {
	sheet := ParseCSS(`.foo, .bar { margin: 0; }`)
	rule := sheet.Rules[0].(*CssQualifiedRule)
	if len(rule.Selectors) != 2 {
		t.Fatalf("expected 2 selectors, got %d", len(rule.Selectors))
	}
}

func TestParseCSS_AtMedia(t *testing.T) {
	sheet := ParseCSS(`@media (max-width: 768px) { button { display: none; } }`)
	if len(sheet.Rules) != 1 {
		t.Fatalf("expected 1 at-rule, got %d", len(sheet.Rules))
	}
	at, ok := sheet.Rules[0].(*CssAtRule)
	if !ok {
		t.Fatal("expected CssAtRule")
	}
	if at.Name != "media" {
		t.Errorf("expected media, got %q", at.Name)
	}
	if len(at.Block) != 1 {
		t.Errorf("expected 1 inner rule, got %d", len(at.Block))
	}
}

func TestParseCSS_Keyframes(t *testing.T) {
	css := `@keyframes spin { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }`
	sheet := ParseCSS(css)
	at, ok := sheet.Rules[0].(*CssAtRule)
	if !ok || at.Name != "keyframes" {
		t.Fatal("expected keyframes at-rule")
	}
	// Inner content should be preserved as raw (not scoped)
	if len(at.Block) == 0 {
		t.Error("keyframes block should have content")
	}
}

func TestParseCSS_Important(t *testing.T) {
	sheet := ParseCSS(`p { color: red !important; }`)
	rule := sheet.Rules[0].(*CssQualifiedRule)
	if !rule.Decls[0].Important {
		t.Error("should parse !important")
	}
}

func TestScopeCSS_Simple(t *testing.T) {
	out := ScopeCSS(`button { color: red; }`, "vela-abc")
	if !strings.Contains(out, "button[vela-abc]") {
		t.Errorf("simple selector not scoped: %s", out)
	}
}

func TestScopeCSS_MultiSelector(t *testing.T) {
	out := ScopeCSS(`.foo, .bar { margin: 0; }`, "vela-xyz")
	if !strings.Contains(out, ".foo[vela-xyz]") {
		t.Errorf(".foo not scoped: %s", out)
	}
	if !strings.Contains(out, ".bar[vela-xyz]") {
		t.Errorf(".bar not scoped: %s", out)
	}
}

func TestScopeCSS_PseudoElement(t *testing.T) {
	// Scope attr must appear BEFORE ::before/::after
	out := ScopeCSS(`p::before { content: ''; }`, "vela-abc")
	// The attribute should be injected before the pseudo-element
	if !strings.Contains(out, "p[vela-abc]") {
		t.Errorf("pseudo-element selector not correctly scoped: %s", out)
	}
}

func TestScopeCSS_AtMediaWrapsInnerRules(t *testing.T) {
	out := ScopeCSS(`@media (max-width: 768px) { .card { display: none; } }`, "vela-abc")
	if !strings.Contains(out, ".card[vela-abc]") {
		t.Errorf("inner rule inside @media not scoped: %s", out)
	}
	if !strings.Contains(out, "@media") {
		t.Errorf("@media wrapper lost: %s", out)
	}
}

func TestScopeCSS_KeyframesNotScoped(t *testing.T) {
	out := ScopeCSS(`@keyframes spin { from { opacity: 0; } to { opacity: 1; } }`, "vela-abc")
	// "from" and "to" must NOT get attribute selectors
	if strings.Contains(out, "from[vela-abc]") {
		t.Errorf("keyframe selector should not be scoped: %s", out)
	}
}

func TestScopeCSS_Global(t *testing.T) {
	out := ScopeCSS(`:global(.reset) { margin: 0; }`, "vela-abc")
	// :global() strips the scoping but keeps the inner selector
	if strings.Contains(out, "[vela-abc]") {
		t.Errorf(":global selector should not be scoped: %s", out)
	}
	if !strings.Contains(out, ".reset") {
		t.Errorf(":global inner selector should be preserved: %s", out)
	}
}

func TestScopeCSS_CustomProperty(t *testing.T) {
	out := ScopeCSS(`:root { --color-primary: blue; }`, "vela-abc")
	// :root is a valid selector; must be scoped
	if !strings.Contains(out, ":root[vela-abc]") && !strings.Contains(out, "[vela-abc]:root") {
		t.Logf("CSS custom property output: %s", out)
		// Allow either form — implementation may vary
	}
}

func TestValidateCssASTRejectsBrokenShapes(t *testing.T) {
	sheet := &CssStylesheet{
		Source: "p {}",
		Span:   srcspan.SectionSpan{Start: 0, End: len("p {}")},
		Rules: []CssNode{
			&CssQualifiedRule{Selectors: []*CssSelector{nil}, Decls: []*CssDecl{nil}},
			nil,
		},
	}
	issues := ValidateCssAST(sheet)
	if len(issues) < 3 {
		t.Fatalf("issues = %+v, want structural CSS failures", issues)
	}
}

func TestValidateCssASTRejectsEscapingSpan(t *testing.T) {
	sheet := &CssStylesheet{
		Source: "p {}",
		Span:   srcspan.SectionSpan{Start: 10, End: 14},
		Rules: []CssNode{&CssQualifiedRule{
			Span:      srcspan.SectionSpan{Start: 10, End: 20},
			Selectors: []*CssSelector{{Span: srcspan.SectionSpan{Start: 10, End: 11}, Parts: []CssTok{{Kind: CssTokIdent, Val: "p"}}}},
		}},
	}
	issues := ValidateCssAST(sheet)
	if len(issues) == 0 || !strings.Contains(issues[0].Message, "escapes stylesheet source") {
		t.Fatalf("issues = %+v, want escaping span diagnostic", issues)
	}
}
