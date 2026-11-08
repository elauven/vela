package compiler

import (
	"github.com/elauven/vela/internal/analyzer"
	"github.com/elauven/vela/internal/diag"
	"github.com/elauven/vela/internal/jsast"
	"github.com/elauven/vela/internal/sfcast"

	"path"
	"strings"
)

// lazy() components (task 071): lazy(() => import('./X.vela')) is a
// component value for <vela:component> whose module loads on demand.

// lazyComponentID is the $$id of the component a lazy import names,
// relative to the importing component's ID.
func lazyComponentID(componentID, specifier string) string {
	return path.Clean(path.Join(path.Dir(componentID), strings.TrimSuffix(specifier, ".vela")))
}

// ResolveLazyComponents checks the lazy() calls of a component script and
// returns them; the argument must be `() => import('<literal .vela path>')`
// (VELA-LAZY-ARGUMENT). Positions are relative to the script.
func ResolveLazyComponents(prog *jsast.JsProgram, componentID string, ds *diag.DiagSet) []sfcast.LazyComponent {
	local := analyzer.LazyLocalName(prog)
	if local == "" {
		return nil
	}
	var out []sfcast.LazyComponent
	jsast.WalkNode(prog, func(n jsast.JsNode) bool {
		call, ok := n.(*jsast.JsCallExpr)
		if !ok {
			return true
		}
		if callee, ok := call.Callee.(*jsast.JsIdent); !ok || callee.Name != local {
			return true
		}
		specifier := ""
		valid := len(call.Args) == 1
		if valid {
			specifier, valid = analyzer.LazyImportSpecifier(call.Args[0])
		}
		if !valid {
			errorOver(ds, call, "VELA-LAZY-ARGUMENT", "Write "+local+"(() => import('./Component.vela')) with a literal path.", "the argument of %s() must be () => import('<relative .vela path>'): the server and the component ID need the literal path", local)
			return true
		}
		out = append(out, sfcast.LazyComponent{Local: local, Call: call, Specifier: specifier, ID: lazyComponentID(componentID, specifier)})
		return true
	})
	return out
}

// lazyBindings are the script's top-level names bound to a lazy() call.
func lazyBindings(prog *jsast.JsProgram, lazy []sfcast.LazyComponent) map[string]bool {
	calls := map[*jsast.JsCallExpr]bool{}
	for _, l := range lazy {
		calls[l.Call] = true
	}
	out := map[string]bool{}
	if prog == nil || len(calls) == 0 {
		return out
	}
	for _, stmt := range prog.Body {
		decl, ok := stmt.(*jsast.JsVarDecl)
		if !ok {
			continue
		}
		for _, d := range decl.Decls {
			id, isIdent := d.Id.(*jsast.JsIdent)
			call, isCall := d.Init.(*jsast.JsCallExpr)
			if isIdent && isCall && calls[call] {
				out[id.Name] = true
			}
		}
	}
	return out
}

// ValidateLazyTags rejects a lazy binding used as a tag (VELA-LAZY-TAG): a
// lazy value renders only through <vela:component :this={…}>. Positions
// are relative to the template.
func ValidateLazyTags(nodes []sfcast.Node, lazy map[string]bool, templateOffset int, ds *diag.DiagSet) {
	if len(lazy) == 0 {
		return
	}
	walkTemplateComponents(nodes, func(c *sfcast.ComponentNode) {
		if lazy[c.Name] {
			pos := c.Span.Start - templateOffset + 1
			ds.Report(diag.SevError, pos, pos+len(c.Name), "VELA-LAZY-TAG", "Write <vela:component :this={"+c.Name+"} />.", "%s is a lazy component; render it with <vela:component :this={%s}>", c.Name, c.Name)
		}
	})
}

func walkTemplateComponents(nodes []sfcast.Node, visit func(*sfcast.ComponentNode)) {
	for _, node := range nodes {
		switch n := node.(type) {
		case *sfcast.ElementNode:
			walkTemplateComponents(n.Children, visit)
		case *sfcast.ComponentNode:
			visit(n)
			for _, slot := range n.Slots {
				walkTemplateComponents(slot, visit)
			}
		case *sfcast.IfChainNode:
			for _, b := range n.Branches {
				walkTemplateComponents(b.Children, visit)
			}
		case *sfcast.EachBlockNode:
			walkTemplateComponents(n.Children, visit)
			walkTemplateComponents(n.ElseChildren, visit)
		case *sfcast.AwaitBlockNode:
			walkTemplateComponents(n.PendingChildren, visit)
			walkTemplateComponents(n.ThenChildren, visit)
			walkTemplateComponents(n.CatchChildren, visit)
		case *sfcast.HeadNode:
			walkTemplateComponents(n.Children, visit)
		case *sfcast.BoundaryBlockNode:
			walkTemplateComponents(n.Children, visit)
			walkTemplateComponents(n.CatchChildren, visit)
		case *sfcast.SlotNode:
			walkTemplateComponents(n.Fallback, visit)
		}
	}
}
