package ssr

import (
	"github.com/elauven/vela/internal/analyzer"
	"github.com/elauven/vela/internal/jsast"
	"github.com/elauven/vela/internal/sfcast"
)

// TypeScript in server rendering (task 086, docs/TYPESCRIPT.md TS-7). The
// translation reads the erased tree of a TypeScript component or module,
// so it renders exactly as its JavaScript twin (TS-7.1). Only the
// annotations of props choose Go types (TS-7.2): the Go compiler checks
// the value a caller passes, so they are safe to trust. Other annotations
// never select the typed tier (TS-7.3).

// Type sources of --ssr-report (TS-7.5).
const (
	SSRTypeSourceTS       = "ts"
	SSRTypeSourceJSDoc    = "jsdoc"
	SSRTypeSourceInferred = "inferred"
	SSRTypeSourceGo       = "go"
)

// ssrTSPropAnnotation is the annotation of the prop name of a TypeScript
// script: `export let name: T`.
func ssrTSPropAnnotation(script *sfcast.ScriptAST, name string) (*jsast.TSType, bool) {
	if script == nil || !script.TypeScript || script.Program == nil {
		return nil, false
	}
	for _, stmt := range script.Program.Body {
		export, ok := stmt.(*jsast.JsExportDecl)
		if !ok {
			continue
		}
		decl, ok := export.Declaration.(*jsast.JsVarDecl)
		if !ok || decl.Kind != "let" {
			continue
		}
		for _, d := range decl.Decls {
			if analyzer.IdentName(d.Id) == name && d.Type != nil {
				return d.Type, true
			}
		}
	}
	return nil, false
}

// ssrTSValueType maps an annotation to the Go type of a prop field: string,
// number (float64), boolean, arrays and tuples (slices), object types,
// interfaces and Record (objects); literal types widen, a union of one kind
// is that kind, aliases and interfaces of the script resolve. Anything else
// (a union with null, any, generics, conditional and imported types) is any.
func ssrTSValueType(prog *jsast.JsProgram, t *jsast.TSType, depth int) SSRValueType {
	if t == nil || depth > 8 {
		return SSRTypeAny
	}
	switch t.Kind {
	case jsast.TSParen:
		if len(t.Args) == 1 {
			return ssrTSValueType(prog, t.Args[0], depth+1)
		}
	case jsast.TSKeyword:
		switch t.Name {
		case "string":
			return SSRTypeString
		case "number":
			return SSRTypeFloat
		case "boolean":
			return SSRTypeBool
		case "object":
			return SSRTypeObject
		}
	case jsast.TSLiteral:
		switch {
		case t.Name == "true" || t.Name == "false":
			return SSRTypeBool
		case len(t.Name) > 0 && (t.Name[0] == '"' || t.Name[0] == '\''):
			return SSRTypeString
		case len(t.Name) > 0 && t.Name[len(t.Name)-1] != 'n':
			return SSRTypeFloat
		}
	case jsast.TSTemplate:
		return SSRTypeString
	case jsast.TSArray, jsast.TSTuple:
		return SSRTypeSlice
	case jsast.TSObject, jsast.TSMapped:
		return SSRTypeObject
	case jsast.TSOperator:
		if t.Name == "readonly" && len(t.Args) == 1 {
			return ssrTSValueType(prog, t.Args[0], depth+1)
		}
	case jsast.TSUnion:
		kind := SSRTypeAny
		for i, arg := range t.Args {
			k := ssrTSValueType(prog, arg, depth+1)
			if k == SSRTypeAny || i > 0 && k != kind {
				return SSRTypeAny
			}
			kind = k
		}
		return kind
	case jsast.TSRefType:
		switch t.Name {
		case "Array", "ReadonlyArray":
			return SSRTypeSlice
		case "Record":
			return SSRTypeObject
		}
		if prog != nil && prog.TS != nil && len(t.Args) == 0 {
			for _, d := range prog.TS.Decls {
				if d.Name != t.Name || len(d.TypeParams) > 0 {
					continue
				}
				switch d.Kind {
				case jsast.TSDeclInterface:
					return SSRTypeObject
				case jsast.TSDeclTypeAlias:
					return ssrTSValueType(prog, d.Type, depth+1)
				}
			}
		}
	}
	return SSRTypeAny
}

// ssrPropType is the Go type of a prop and its source: the annotation of a
// TypeScript prop, the JSDoc @type of a JavaScript one, else what its
// default proves ("" when nothing does).
func ssrPropType(script *sfcast.ScriptAST, name string, fallback jsast.JsExpr) (SSRValueType, string) {
	if t, ok := ssrTSPropAnnotation(script, name); ok {
		return ssrTSValueType(script.Program, t, 0), SSRTypeSourceTS
	}
	if script != nil {
		for _, comment := range script.DocComments {
			if comment.TargetName != name {
				continue
			}
			switch comment.Type {
			case sfcast.JSDocTypeString:
				return SSRTypeString, SSRTypeSourceJSDoc
			case sfcast.JSDocTypeBool:
				return SSRTypeBool, SSRTypeSourceJSDoc
			case sfcast.JSDocTypeNumber:
				return SSRTypeFloat, SSRTypeSourceJSDoc
			case sfcast.JSDocTypeInteger:
				return SSRTypeInt, SSRTypeSourceJSDoc
			case sfcast.JSDocTypeSlice:
				return SSRTypeSlice, SSRTypeSourceJSDoc
			case sfcast.JSDocTypeObject:
				return SSRTypeObject, SSRTypeSourceJSDoc
			case sfcast.JSDocTypeAny:
				return SSRTypeAny, SSRTypeSourceJSDoc
			}
		}
	}
	if t := inferSSRValueType(fallback); t != SSRTypeAny {
		return t, SSRTypeSourceInferred
	}
	return SSRTypeAny, ""
}

// SSRPropRecord is a prop of a component in --ssr-report: its Go type and
// where the type comes from (TS-7.5).
type SSRPropRecord struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	TypeSource string `json:"typeSource,omitempty"`
}

func (t SSRValueType) goName() string {
	switch t {
	case SSRTypeString:
		return "string"
	case SSRTypeBool:
		return "bool"
	case SSRTypeInt:
		return "int"
	case SSRTypeFloat:
		return "float64"
	case SSRTypeSlice:
		return "[]any"
	case SSRTypeObject:
		return "map[string]any"
	}
	return "any"
}

// ssrTSGoType is the Go type of a host parameter or result annotated with
// t (TS-7.4): number float64, string, boolean bool, T[] and Array<T> []T,
// Record<string, T> map[string]T, object types map[string]any; an optional
// one (`T | undefined`, `x?: T`) a pointer. "" when the annotation does not
// map, and the stub keeps `any`.
func ssrTSGoType(t *jsast.TSType, depth int) string {
	if t == nil || depth > 8 {
		return ""
	}
	switch t.Kind {
	case jsast.TSParen:
		if len(t.Args) == 1 {
			return ssrTSGoType(t.Args[0], depth+1)
		}
	case jsast.TSKeyword:
		switch t.Name {
		case "string":
			return "string"
		case "number":
			return "float64"
		case "boolean":
			return "bool"
		case "object":
			return "map[string]any"
		case "any", "unknown":
			return "any"
		}
	case jsast.TSLiteral, jsast.TSTemplate:
		switch ssrTSValueType(nil, t, depth+1) {
		case SSRTypeString:
			return "string"
		case SSRTypeFloat:
			return "float64"
		case SSRTypeBool:
			return "bool"
		}
	case jsast.TSArray:
		if len(t.Args) == 1 {
			return "[]" + ssrTSGoElem(t.Args[0], depth)
		}
	case jsast.TSObject:
		return "map[string]any"
	case jsast.TSOperator:
		if t.Name == "readonly" && len(t.Args) == 1 {
			return ssrTSGoType(t.Args[0], depth+1)
		}
	case jsast.TSRefType:
		switch {
		case (t.Name == "Array" || t.Name == "ReadonlyArray") && len(t.Args) == 1:
			return "[]" + ssrTSGoElem(t.Args[0], depth)
		case t.Name == "Record" && len(t.Args) == 2 && ssrTSGoType(t.Args[0], depth+1) == "string":
			return "map[string]" + ssrTSGoElem(t.Args[1], depth)
		}
	case jsast.TSUnion:
		var kept []*jsast.TSType
		optional := false
		for _, arg := range t.Args {
			if arg.Kind == jsast.TSKeyword && arg.Name == "undefined" {
				optional = true
				continue
			}
			kept = append(kept, arg)
		}
		mapped := ""
		for i, arg := range kept {
			m := ssrTSGoType(arg, depth+1)
			if m == "" || i > 0 && m != mapped {
				return ""
			}
			mapped = m
		}
		if optional && mapped != "" && mapped != "any" {
			return "*" + mapped
		}
		return mapped
	}
	return ""
}

func ssrTSGoElem(t *jsast.TSType, depth int) string {
	if m := ssrTSGoType(t, depth+1); m != "" && m[0] != '*' {
		return m
	}
	return "any"
}

// ssrTSHostKind is the boundary kind (SSRHostParam.Kind) of a Go type that
// ssrTSGoType wrote, for comparing a TypeScript signature with a host's.
func ssrTSHostKind(goType string) string {
	goType = trimPointer(goType)
	switch {
	case goType == "string":
		return "string"
	case goType == "float64":
		return "number"
	case goType == "bool":
		return "boolean"
	case len(goType) > 2 && goType[:2] == "[]":
		return "array"
	case len(goType) > 4 && goType[:4] == "map[":
		return "object"
	}
	return "any"
}

func trimPointer(s string) string {
	for len(s) > 0 && s[0] == '*' {
		s = s[1:]
	}
	return s
}

// ssrTSHostSignatureProblem compares the annotated parameters of the
// JavaScript function decl with the parameters of its Go host (§8.3,
// TS-7.4): a parameter whose TypeScript type maps to another boundary kind
// than the Go one is a problem. Unannotated and `any` parameters, and Go
// parameters of type any, are not compared.
func ssrTSHostSignatureProblem(decl *jsast.JsFuncDecl, binding *SSRHostBinding) string {
	if decl == nil || decl.TS == nil {
		return ""
	}
	for i, param := range binding.Params {
		if i >= len(decl.TS.Params) || decl.TS.Params[i] == nil {
			break
		}
		want := ssrTSHostKind(ssrTSGoType(decl.TS.Params[i].Type, 0))
		if want == "any" || param.Kind == "any" || param.Kind == "time" {
			continue
		}
		if want != param.Kind {
			return "parameter " + param.Name + " is " + want + " in TypeScript but " + param.Type + " (" + param.Kind + ") in Go"
		}
	}
	return ""
}
