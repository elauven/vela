package analyzer

import (
	"strings"

	"github.com/elauven/vela/internal/jsast"
)

// lazy() components (task 071): the compiler and the server renderer
// recognise lazy(() => import('./X.vela')) the same way.

// LazyLocalName is the local name of lazy imported from 'vela', or "".
func LazyLocalName(prog *jsast.JsProgram) string {
	if prog == nil {
		return ""
	}
	for _, stmt := range prog.Body {
		imp, ok := stmt.(*jsast.JsImportDecl)
		if !ok || imp.Source != "vela" {
			continue
		}
		for _, spec := range imp.Specifiers {
			if spec.Imported == "lazy" {
				if spec.Local != "" {
					return spec.Local
				}
				return spec.Imported
			}
		}
	}
	return ""
}

// LazyImportSpecifier returns the literal path of `() => import('<path>')`.
func LazyImportSpecifier(arg jsast.JsExpr) (string, bool) {
	arrow, ok := arg.(*jsast.JsArrowFunc)
	if !ok || len(arrow.Params) != 0 || arrow.Async {
		return "", false
	}
	call, ok := arrow.Body.(*jsast.JsCallExpr)
	if !ok || len(call.Args) != 1 {
		return "", false
	}
	if callee, ok := call.Callee.(*jsast.JsIdent); !ok || callee.Name != "import" {
		return "", false
	}
	literal, ok := call.Args[0].(*jsast.JsLiteral)
	if !ok || literal.Kind != "string" {
		return "", false
	}
	value := literal.Value
	if !literal.Decoded {
		if len(literal.Raw) < 2 || strings.Contains(literal.Raw, "\\") {
			return "", false
		}
		value = literal.Raw[1 : len(literal.Raw)-1]
	}
	if !strings.HasSuffix(value, ".vela") || !(strings.HasPrefix(value, "./") || strings.HasPrefix(value, "../")) {
		return "", false
	}
	return value, true
}

// ScriptProgram is the parsed script, or nil.
func (a *Analysis) ScriptProgram() *jsast.JsProgram {
	if a == nil || a.Script == nil {
		return nil
	}
	return a.Script.Program
}
