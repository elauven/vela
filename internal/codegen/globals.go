package codegen

import (
	"github.com/elauven/vela/internal/jsast"
	"github.com/elauven/vela/internal/sfcast"
)

// GlobalTarget is the JavaScript global a special element listens on.
func GlobalTarget(tag string) string {
	if tag == sfcast.DocumentTag {
		return "document"
	}
	return "window"
}

// buildBlocksGlobal lowers <vela:window> and <vela:document> (task 095):
// the element renders nothing; its listeners go on window or document and
// leave with the component, and every binding writes the live value into
// its state through $$global, after the hydration commit on hydration.
func (g *ASTGen) buildBlocksGlobal(el *sfcast.ElementNode) {
	target := jsast.GenIdent(GlobalTarget(el.Tag))
	for _, attr := range el.Attrs {
		switch {
		case attr.IsEvent:
			g.blocksEmit(jsast.GenExprStmt(jsast.GenCall(jsast.GenMember(jsast.GenIdent("$$c"), "listen"), target, jsast.GenStrLit(attr.Name), g.blocksEventHandler(attr))))
		case attr.IsTwoWay:
			name, _ := TemplateExprIdent(attr.Expr)
			write := blocksArrow([]jsast.JsExpr{jsast.GenIdent("$$v")}, jsast.GenCall(jsast.GenIdent("$$set_"+name), jsast.GenIdent("$$v")))
			g.blocksEmit(jsast.GenExprStmt(jsast.GenCall(jsast.GenIdent("$$global"), jsast.GenIdent("$$c"), target, jsast.GenStrLit(attr.Name), write)))
		}
	}
}
