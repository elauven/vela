package codegen

import (
	"github.com/elauven/vela/internal/jsast"
	"github.com/elauven/vela/internal/sfcast"
)

// Event modifiers (task 101). @event.modifier… compiles to a wrapper that
// runs the checks of the modifiers in their order, inline, before it calls
// the handler: .prevent and .stop call the event's method, the others
// return when the event does not match. .capture and .passive are options
// of the listener; .once is a flag of the wrapper, set when the handler
// runs, so the events the other modifiers reject do not use it up. The
// wrapper reads the handler when the event comes, so one listener serves a
// handler that changes between updates.

// blocksListener returns the event name, the listener and its options
// (nil without options) of an @event attribute of an element or of
// <vela:window> / <vela:document>.
func (g *ASTGen) blocksListener(attr *sfcast.Attr) (string, jsast.JsExpr, jsast.JsExpr) {
	event, names := sfcast.SplitEventName(attr.Name)
	handler := g.blocksEventHandler(attr)
	if len(names) == 0 {
		return event, handler, nil
	}
	modifiers, problem := sfcast.ResolveEventModifiers(event, names, false)
	if problem != nil {
		panic("vela: an invalid event modifier reached code generation: " + problem.Message)
	}
	var options []*jsast.JsProp
	var body []jsast.JsStmt
	e := jsast.GenIdent("$event")
	member := func(name string) jsast.JsExpr { return jsast.GenMember(jsast.GenIdent("$event"), name) }
	reject := func(test jsast.JsExpr) {
		body = append(body, jsast.GenIf(test, jsast.GenReturn(nil)))
	}
	once := ""
	listed := map[string]bool{}
	for _, m := range modifiers {
		listed[m.Name] = true
	}
	keysDone := false
	for _, m := range modifiers {
		switch m.Kind {
		case sfcast.ModBehaviour:
			switch m.Name {
			case "prevent":
				body = append(body, jsast.GenExprStmt(jsast.GenCall(member("preventDefault"))))
			case "stop":
				body = append(body, jsast.GenExprStmt(jsast.GenCall(member("stopPropagation"))))
			case "self":
				reject(jsast.GenBinary("!==", member("target"), member("currentTarget")))
			case "trusted":
				reject(jsast.GenUnary("!", member("isTrusted")))
			}
		case sfcast.ModOption:
			if m.Name == "once" {
				once = g.blocks.next("$$once")
				// A spent listener does nothing, not even .prevent.
				body = append([]jsast.JsStmt{jsast.GenIf(jsast.GenIdent(once), jsast.GenReturn(nil))}, body...)
				continue
			}
			options = append(options, &jsast.JsProp{BaseNode: jsast.GenNode(), Key: jsast.GenIdent(m.Name), Value: jsast.GenBoolLit(true)})
		case sfcast.ModSystem:
			reject(jsast.GenUnary("!", member(m.Name+"Key")))
		case sfcast.ModExact:
			var test jsast.JsExpr
			for _, key := range []string{"ctrl", "shift", "alt", "meta"} {
				if listed[key] {
					continue
				}
				check := member(key + "Key")
				if test == nil {
					test = check
				} else {
					test = jsast.GenLogical("||", test, check)
				}
			}
			if test != nil {
				reject(test)
			}
		case sfcast.ModKey:
			// The key modifiers of one attribute are alternatives.
			if keysDone {
				continue
			}
			keysDone = true
			var match jsast.JsExpr
			for _, k := range modifiers {
				if k.Kind != sfcast.ModKey {
					continue
				}
				for _, key := range k.Keys {
					value := member("key")
					if len(key) == 1 && key != " " {
						value = jsast.GenCall(jsast.GenMember(member("key"), "toLowerCase"))
					}
					check := jsast.GenBinary("===", value, jsast.GenStrLit(key))
					if match == nil {
						match = check
					} else {
						match = jsast.GenLogical("||", match, check)
					}
				}
			}
			// event.key is missing on synthetic events and autofill.
			reject(jsast.GenLogical("||", jsast.GenUnary("!", member("key")), jsast.GenUnary("!", match)))
		case sfcast.ModButton:
			reject(jsast.GenBinary("!==", member("button"), jsast.GenNumLit(m.Button)))
		}
	}
	if once != "" {
		g.blocksEmit(jsast.GenLet(once, jsast.GenBoolLit(false)))
		body = append(body, jsast.GenAssignStmt(jsast.GenIdent(once), jsast.GenBoolLit(true)))
	}
	body = append(body, g.blocksInvoke(attr, handler, e)...)
	var optionsExpr jsast.JsExpr
	if len(options) > 0 {
		optionsExpr = &jsast.JsObjectExpr{BaseExpr: jsast.BaseExpr{BaseNode: jsast.GenNode()}, Props: options}
	}
	return event, blocksArrow([]jsast.JsExpr{jsast.GenIdent("$event")}, jsast.GenBlock(body...)), optionsExpr
}

// blocksInvoke runs a handler from blocksEventHandler with the event: a
// handler value is called, a statement handler is inlined.
func (g *ASTGen) blocksInvoke(attr *sfcast.Attr, handler, event jsast.JsExpr) []jsast.JsStmt {
	if _, isIdent := TemplateExprIdent(attr.Expr); !isIdent && !templateExprIsHandler(attr.Expr) {
		if arrow, ok := handler.(*jsast.JsArrowFunc); ok {
			if block, ok := arrow.Body.(*jsast.JsBlock); ok {
				return block.Body
			}
		}
	}
	return []jsast.JsStmt{jsast.GenExprStmt(jsast.GenCall(handler, event))}
}

// listenCall is `$$c.listen(target, event, listener[, options])`.
func listenCall(target jsast.JsExpr, event string, listener, options jsast.JsExpr) jsast.JsStmt {
	args := []jsast.JsExpr{target, jsast.GenStrLit(event), listener}
	if options != nil {
		args = append(args, options)
	}
	return jsast.GenExprStmt(jsast.GenCall(jsast.GenMember(jsast.GenIdent("$$c"), "listen"), args...))
}
