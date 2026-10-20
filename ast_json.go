package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ─────────────────────────────────────────────────────────────────────────────
//  AST → JSON serialization for the --ast debug flag
//
//  Converts the parsed SFC (template nodes, script analysis, CSS) into a
//  JSON structure that can be pretty-printed for debugging.
// ─────────────────────────────────────────────────────────────────────────────

// SFCJSON is the top-level debug dump emitted by --ast.
type SFCJSON struct {
	File     string        `json:"file"`
	Script   ScriptJSON    `json:"script"`
	Template []interface{} `json:"template"`
	Style    string        `json:"style,omitempty"`
}

type ScriptJSON struct {
	StateVars   []StateVarJSON   `json:"stateVars"`
	Props       []PropJSON       `json:"props"`
	DerivedVars []DerivedVarJSON `json:"derivedVars"`
	Effects     []EffectJSON     `json:"effects"`
	Lifecycles  []LifecycleJSON  `json:"lifecycles"`
	Imports     []string         `json:"imports,omitempty"`
	CleanScript string           `json:"cleanScript,omitempty"`
}

type StateVarJSON struct {
	Name    string `json:"name"`
	Initial string `json:"initial,omitempty"`
}

type PropJSON struct {
	Name     string `json:"name"`
	Bindable bool   `json:"bindable,omitempty"`
	Default  string `json:"default,omitempty"`
}

type DerivedVarJSON struct {
	Name string   `json:"name"`
	Expr string   `json:"expr"`
	Deps []string `json:"deps"`
}

type EffectJSON struct {
	Body string   `json:"body"`
	Deps []string `json:"deps"`
}

type LifecycleJSON struct {
	Kind string `json:"kind"`
	Body string `json:"body"`
}

// ─────────────────────────────────────────────────────────────────────────────
//  Node → map[string]interface{}
// ─────────────────────────────────────────────────────────────────────────────

func nodeToJSON(node Node) interface{} {
	if node == nil {
		return nil
	}
	switch n := node.(type) {
	case *TextNode:
		return textNodeJSON(n)
	case *ElementNode:
		return elementNodeJSON(n)
	case *IfChainNode:
		return ifChainNodeJSON(n)
	case *EachBlockNode:
		return eachBlockNodeJSON(n)
	case *ComponentNode:
		return componentNodeJSON(n)
	case *SlotNode:
		return slotNodeJSON(n)
	default:
		return map[string]interface{}{"type": "unknown"}
	}
}

func nodesToJSON(nodes []Node) []interface{} {
	out := make([]interface{}, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, nodeToJSON(n))
	}
	return out
}

func textNodeJSON(n *TextNode) interface{} {
	var parts []interface{}
	for _, p := range n.Parts {
		if p.Static {
			parts = append(parts, map[string]interface{}{"static": p.Value})
		} else {
			parts = append(parts, map[string]interface{}{"expr": p.Value})
		}
	}
	return map[string]interface{}{
		"type":  "Text",
		"parts": parts,
	}
}

func attrJSON(attrs []*Attr) []interface{} {
	out := make([]interface{}, 0, len(attrs))
	for _, a := range attrs {
		m := map[string]interface{}{"name": a.Name, "value": a.Value, "valueKind": a.ValueKind.String()}
		if a.IsEvent {
			m["kind"] = "event"
		} else if a.IsBind {
			m["kind"] = "bind"
		} else {
			m["kind"] = "static"
		}
		out = append(out, m)
	}
	return out
}

func elementNodeJSON(n *ElementNode) interface{} {
	m := map[string]interface{}{
		"type":  "Element",
		"tag":   n.Tag,
		"attrs": attrJSON(n.Attrs),
	}
	if len(n.Children) > 0 {
		m["children"] = nodesToJSON(n.Children)
	}
	return m
}

func ifChainNodeJSON(n *IfChainNode) interface{} {
	branches := make([]interface{}, 0, len(n.Branches))
	for _, b := range n.Branches {
		bm := map[string]interface{}{
			"cond":     b.Cond,
			"children": nodesToJSON(b.Children),
		}
		branches = append(branches, bm)
	}
	return map[string]interface{}{
		"type":     "IfChain",
		"branches": branches,
	}
}

func eachBlockNodeJSON(n *EachBlockNode) interface{} {
	m := map[string]interface{}{
		"type":      "EachBlock",
		"eachExpr":  n.EachExpr,
		"itemName":  n.ItemName,
		"indexName": n.IndexName,
		"children":  nodesToJSON(n.Children),
	}
	if n.EachKey != "" {
		m["eachKey"] = n.EachKey
	}
	if len(n.ElseChildren) > 0 {
		m["elseChildren"] = nodesToJSON(n.ElseChildren)
	}
	return m
}

func componentNodeJSON(n *ComponentNode) interface{} {
	m := map[string]interface{}{
		"type":  "Component",
		"name":  n.Name,
		"attrs": attrJSON(n.Attrs),
	}
	if len(n.Slots) > 0 {
		slots := map[string]interface{}{}
		for k, v := range n.Slots {
			slots[k] = nodesToJSON(v)
		}
		m["slots"] = slots
	}
	return m
}

func slotNodeJSON(n *SlotNode) interface{} {
	m := map[string]interface{}{
		"type": "Slot",
		"name": n.Name,
	}
	if len(n.Fallback) > 0 {
		m["fallback"] = nodesToJSON(n.Fallback)
	}
	return m
}

// ─────────────────────────────────────────────────────────────────────────────
//  Analysis → JSON
// ─────────────────────────────────────────────────────────────────────────────

func sourceExpr(expr JsExpr) string {
	if expr == nil {
		return ""
	}
	return strings.TrimSpace(printNode(expr))
}

func sourceBlock(block *JsBlock) string {
	if block == nil {
		return ""
	}
	return strings.TrimSpace(printNode(block))
}

func analysisToJSON(a *Analysis) ScriptJSON {
	out := ScriptJSON{}

	for _, sv := range a.StateVars {
		out.StateVars = append(out.StateVars, StateVarJSON{
			Name:    sv.Name,
			Initial: sourceExpr(sv.InitialAST),
		})
	}
	for _, p := range a.Props {
		out.Props = append(out.Props, PropJSON{
			Name:     p.Name,
			Bindable: p.Bindable,
			Default:  sourceExpr(p.DefaultAST),
		})
	}
	for _, dv := range a.DerivedVars {
		out.DerivedVars = append(out.DerivedVars, DerivedVarJSON{
			Name: dv.Name,
			Expr: sourceExpr(dv.ExprAST),
			Deps: dv.Deps,
		})
	}
	for _, eff := range a.Effects {
		out.Effects = append(out.Effects, EffectJSON{
			Body: sourceBlock(eff.BodyAST),
			Deps: eff.Deps,
		})
	}
	for _, lc := range a.Lifecycles {
		out.Lifecycles = append(out.Lifecycles, LifecycleJSON{
			Kind: lc.Kind,
			Body: sourceBlock(lc.BodyAST),
		})
	}
	out.CleanScript = strings.TrimSpace(a.CleanScript())
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
//  Top-level dump function
// ─────────────────────────────────────────────────────────────────────────────

// DumpAST is the standalone compatibility entry point for tooling that only
// has source text. Compilation itself must use DumpParsedAST so --ast does not
// parse the component a second time.
func DumpAST(filename, src string) (string, error) {
	sfc, err := ParseSFC(src)
	if err != nil {
		return "", fmt.Errorf("parseSFC: %w", err)
	}

	if sfc.TemplateAST == nil {
		return "", fmt.Errorf("missing template AST")
	}
	if len(sfc.TemplateAST.Diagnostics) > 0 {
		return "", fmt.Errorf("parseTemplate: %s", sfc.TemplateAST.Diagnostics[0].Message)
	}

	analysis := analyzeScriptAST(sfc.ScriptAST)
	return DumpParsedAST(filename, sfc, sfc.TemplateAST.Nodes, analysis)
}

// DumpParsedAST serializes an already parsed and analyzed component. Keeping
// this boundary typed is important: diagnostic/debug output must not silently
// introduce a second script parse into the normal compiler pipeline.
func DumpParsedAST(filename string, sfc *SFC, templateNodes []Node, analysis *Analysis) (string, error) {
	if sfc == nil || sfc.ScriptAST == nil {
		return "", fmt.Errorf("missing parsed component AST")
	}
	if analysis == nil {
		return "", fmt.Errorf("missing component analysis")
	}
	dump := SFCJSON{
		File:     filename,
		Script:   analysisToJSON(analysis),
		Template: nodesToJSON(templateNodes),
		Style:    strings.TrimSpace(sfc.Style),
	}

	out, err := json.MarshalIndent(dump, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}
