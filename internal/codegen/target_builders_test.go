package codegen

import (
	"strings"
	"testing"

	"github.com/elauven/vela/internal/css"
	"github.com/elauven/vela/internal/jsast"
	"github.com/elauven/vela/internal/sfcast"
)

func TestTypedStyleInjectionUsesDecodedCSSLiteral(t *testing.T) {
	g := &ASTGen{sfc: &sfcast.SFC{Style: `.card::before { content: "'"; }`}, scopeID: "vela-test"}
	printed := jsast.PrintJS(&jsast.JsProgram{Body: g.buildStyleInjection()}, jsast.PrintPretty)
	scoped := css.ScopeCSS(g.sfc.Style, g.scopeID)
	if !strings.Contains(printed, "textContent = "+jsast.EncodeGeneratedString(scoped)) {
		t.Fatalf("CSS payload was not emitted through decoded literal builder:\n%s", printed)
	}
	if strings.Contains(printed, "jsStmt") {
		t.Fatalf("printer output unexpectedly contains helper text:\n%s", printed)
	}
}
