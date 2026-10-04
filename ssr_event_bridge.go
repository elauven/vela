package main

import "fmt"

const SSRStaticEventDiagnosticCode = "VELA-SSR-EVENT-REACTIVE"

// SSRStaticEventError reports a handler that cannot preserve its behavior in
// the standalone pre-hydration event module.
type SSRStaticEventError struct {
	Pos     int
	Message string
}

func (e *SSRStaticEventError) Error() string {
	return SSRStaticEventDiagnosticCode + ": " + e.Message
}

func ssrStaticEventError(pos int, format string, args ...interface{}) error {
	return &SSRStaticEventError{Pos: pos, Message: fmt.Sprintf(format, args...)}
}

// BuildSSRStaticEventBridge creates a standalone client module from planned
// DOM events. It accepts only closed inline arrow functions with no reactive
// or ambient component references. This prevents static SSR from advertising
// interactivity that needs a component closure or reactive DOM update.
func BuildSSRStaticEventBridge(plan *SSRRenderPlan) (*JsProgram, error) {
	if plan == nil {
		return nil, fmt.Errorf("ssr event bridge: missing render plan")
	}
	if len(plan.Events) == 0 {
		return nil, nil
	}
	props := make([]*JsProp, 0, len(plan.Events))
	for _, event := range plan.Events {
		handler, err := ssrStaticEventHandler(event, plan)
		if err != nil {
			return nil, err
		}
		spec := &JsObjectExpr{BaseExpr: BaseExpr{BaseNode: genNode()}, Props: []*JsProp{
			{BaseNode: genNode(), Key: strLit("event"), Value: strLit(event.Event)},
			{BaseNode: genNode(), Key: strLit("handler"), Value: handler},
		}}
		props = append(props, &JsProp{
			BaseNode: genNode(), Key: strLit(event.EventKey), Value: genArray(spec),
		})
	}
	specs := &JsObjectExpr{BaseExpr: BaseExpr{BaseNode: genNode()}, Props: props}
	entries := genComputedMember(id("$$specs"), genCall(genMember(id("$$node"), "getAttribute"), strLit("data-vela-event")))
	attach := genCall(genMember(id("$$node"), "addEventListener"), genMember(id("$$entry"), "event"), genMember(id("$$entry"), "handler"))
	eachEntry := genForOf("$$entry", id("$$entries"), genBlock(genExprStmt(attach)))
	eachNode := genForOf("$$node", genCall(genMember(id("root"), "querySelectorAll"), strLit("[data-vela-event]")), genBlock(
		genConst("$$entries", entries),
		genIf(id("$$entries"), genBlock(eachEntry)),
	))
	fn := &JsFuncDecl{BaseStmt: BaseStmt{BaseNode: genNode()}, Name: "attachSSRStaticEvents", Params: []JsExpr{id("root")}, Body: genBlock(
		genConst("$$specs", specs),
		eachNode,
	)}
	return &JsProgram{BaseNode: genNode(), Body: []JsStmt{
		&JsExportDecl{BaseStmt: BaseStmt{BaseNode: genNode()}, Declaration: fn},
	}}, nil
}

func ssrStaticEventHandler(event SSRStaticEvent, plan *SSRRenderPlan) (JsExpr, error) {
	if event.Handler == nil || event.Handler.Root() == nil {
		return nil, ssrStaticEventError(event.Span.Start, "event %q has no cached handler expression", event.Event)
	}
	handler, ok := event.Handler.Root().(*JsArrowFunc)
	if !ok {
		return nil, ssrStaticEventError(event.Handler.Pos, "event %q requires an inline arrow handler until hydration", event.Event)
	}
	allowed := map[string]bool{
		"alert": true, "confirm": true, "prompt": true,
		"undefined": true, "NaN": true, "Infinity": true,
	}
	for _, param := range handler.Params {
		name := identName(param)
		if name == "" {
			return nil, ssrStaticEventError(event.Handler.Pos, "event %q uses a non-identifier handler parameter", event.Event)
		}
		allowed[name] = true
	}
	reactive := map[string]bool{}
	for _, name := range planReactiveNames(plan) {
		reactive[name] = true
	}
	usesContext := false
	WalkNode(handler, func(node JsNode) bool {
		switch node.(type) {
		case *JsThisExpr, *JsSuperExpr:
			usesContext = true
		}
		return !usesContext
	})
	if usesContext {
		return nil, ssrStaticEventError(event.Handler.Pos, "event %q captures lexical this or super, which is unavailable in the standalone bridge", event.Event)
	}
	for _, name := range CollectIdents(handler) {
		if reactive[name] {
			return nil, ssrStaticEventError(event.Handler.Pos, "event %q references reactive binding %q before hydration", event.Event, name)
		}
		if !allowed[name] {
			return nil, ssrStaticEventError(event.Handler.Pos, "event %q captures or calls %q, which is unavailable in the standalone bridge", event.Event, name)
		}
	}
	return handler, nil
}

func planReactiveNames(plan *SSRRenderPlan) []string {
	if plan == nil {
		return nil
	}
	return plan.ReactiveNames
}
