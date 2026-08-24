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
		m := map[string]interface{}{"name": a.Name, "value": a.Value}
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
	if n.IfCond != "" {
		m["ifCond"] = n.IfCond
	}
	if n.EachExpr != "" {
		m["eachExpr"] = n.EachExpr
	}
	if n.EachKey != "" {
		m["eachKey"] = n.EachKey
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
		if b.Elem != nil {
			bm["elem"] = map[string]interface{}{
				"tag":   b.Elem.Tag,
				"attrs": attrJSON(b.Elem.Attrs),
			}
		}
		if b.Comp != nil {
			bm["comp"] = b.Comp.Name
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
		"type":     "EachBlock",
		"eachExpr": n.EachExpr,
		"children": nodesToJSON(n.Children),
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
	if n.EachExpr != "" {
		m["eachExpr"] = n.EachExpr
	}
	if n.EachKey != "" {
		m["eachKey"] = n.EachKey
	}
	if n.IfCond != "" {
		m["ifCond"] = n.IfCond
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

func analysisToJSON(a *Analysis) ScriptJSON {
	out := ScriptJSON{}

	for _, sv := range a.StateVars {
		out.StateVars = append(out.StateVars, StateVarJSON{
			Name:    sv.Name,
			Initial: sv.Initial,
		})
	}
	for _, p := range a.Props {
		out.Props = append(out.Props, PropJSON{
			Name:     p.Name,
			Bindable: p.Bindable,
			Default:  p.Default,
		})
	}
	for _, dv := range a.DerivedVars {
		out.DerivedVars = append(out.DerivedVars, DerivedVarJSON{
			Name: dv.Name,
			Expr: dv.Expr,
			Deps: dv.Deps,
		})
	}
	for _, eff := range a.Effects {
		out.Effects = append(out.Effects, EffectJSON{
			Body: strings.TrimSpace(eff.Body),
			Deps: eff.Deps,
		})
	}
	for _, lc := range a.Lifecycles {
		out.Lifecycles = append(out.Lifecycles, LifecycleJSON{
			Kind: lc.Kind,
			Body: strings.TrimSpace(lc.Body),
		})
	}
	out.CleanScript = strings.TrimSpace(a.CleanScript)
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
//  Top-level dump function
// ─────────────────────────────────────────────────────────────────────────────

// DumpAST parses the given .vela file and returns a pretty-printed JSON string
// containing the full template AST, script analysis, and CSS.
func DumpAST(filename, src string) (string, error) {
	sfc, err := parseSFC(src)
	if err != nil {
		return "", fmt.Errorf("parseSFC: %w", err)
	}

	templateNodes, err := parseTemplate(sfc.Template)
	if err != nil {
		return "", fmt.Errorf("parseTemplate: %w", err)
	}

	analysis := analyzeScriptAST(sfc.ScriptAST)

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
