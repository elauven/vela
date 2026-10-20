package ssr

import (
	"github.com/elauven/vela/internal/jsast"
	"github.com/elauven/vela/internal/sfcast"

	"bytes"
	"fmt"
	goast "go/ast"
	"go/format"
	"go/token"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const SSRExpressionDiagnosticCode = "VELA-SSR-EXPR"

// SSRImpureDiagnosticCode marks a write inside the server-evaluated region
// (T-6.6a).
const SSRImpureDiagnosticCode = "VELA-SSR-IMPURE"

// SSRExpressionError carries a component-template-relative position, the
// diagnostic code, and the reason chain from the region site to the
// offending construct (§10.1). Error keeps the one-line form; the region
// report and the CLI add the chain.
type SSRExpressionError struct {
	Pos     int
	Message string
	// Code is SSRExpressionDiagnosticCode when empty.
	Code string
	// Reasons is the chain inside the site, outermost first, for example
	// [`$derived "paras"`, `call "split"`].
	Reasons []string
	// Cause is the innermost message when Message wraps another error.
	Cause string
	// Host suggests the host implementation that would fix the site.
	Host *ssrHostSuggestion
	// bare is the cause of a missing host without the "has no server-native
	// translation" prefix, so that nested host failures are not repeated.
	bare string
	// Source is the .vela offset of the script initializer or function body
	// the problem was found in, when SourceSet; otherwise the site position
	// applies.
	Source    int
	SourceSet bool
}

func (e *SSRExpressionError) Error() string {
	return e.code() + ": " + e.Message
}

func (e *SSRExpressionError) code() string {
	if e.Code == "" {
		return SSRExpressionDiagnosticCode
	}
	return e.Code
}

// innermost returns the message of the offending construct itself.
func (e *SSRExpressionError) innermost() string {
	if e.Cause != "" {
		return e.Cause
	}
	return e.Message
}

func ssrImpure(pos int, format string, args ...interface{}) error {
	return &SSRExpressionError{Pos: pos, Message: fmt.Sprintf(format, args...), Code: SSRImpureDiagnosticCode}
}

// ssrWithReason prefixes reason to the chain of err. When inner is a
// wrapped error, err takes over its code, chain, and innermost message.
func ssrWithReason(err, inner error, reason string) error {
	outer, ok := err.(*SSRExpressionError)
	if !ok {
		return err
	}
	result := *outer
	reasons := []string{reason}
	if problem, ok := inner.(*SSRExpressionError); ok {
		result.Code = problem.Code
		result.Cause = problem.innermost()
		result.Source, result.SourceSet = problem.Source, problem.SourceSet
		if result.Host == nil {
			result.Host = problem.Host
		}
		reasons = append(reasons, problem.Reasons...)
	} else {
		reasons = append(reasons, outer.Reasons...)
	}
	result.Reasons = reasons
	return &result
}

// SSRExprContext maps template identifiers to already-typed Go AST values.
// Props are constructed by NewSSRExprContext; each/await lowering will extend
// Bindings with lexical row and branch names rather than reparsing expressions.
type SSRExprContext struct {
	Props               map[string]SSRPlanProp
	Bindings            map[string]goast.Expr
	ReactiveNames       map[string]bool
	StateInitializers   map[string]jsast.JsExpr
	DerivedInitializers map[string]jsast.JsExpr
	PlainInitializers   map[string]jsast.JsExpr
	DeepInitializers    map[string]jsast.JsExpr
	Hosts               map[string]*SSRHostBinding
	HostDecls           map[string]*jsast.JsFuncDecl
	HostSymbols         map[string]SSRHostSymbol
	ModuleRefs          map[string]ssrModuleRef
	// localIndex is the declaration position of each local binding.
	localIndex map[string]int
	// funcs compiles multi-statement component functions (task 045).
	funcs              *ssrFuncTable
	Functions          map[string]*jsast.JsFuncDecl
	resolving          map[string]bool
	resolvingFunctions map[string]bool
	// scriptOffset places script AST node positions in the .vela source;
	// 0 when unknown.
	scriptOffset int
}

func NewSSRExprContext(plan *SSRRenderPlan) SSRExprContext {
	ctx := SSRExprContext{
		Props:               map[string]SSRPlanProp{},
		Bindings:            map[string]goast.Expr{},
		ReactiveNames:       map[string]bool{},
		StateInitializers:   map[string]jsast.JsExpr{},
		DerivedInitializers: map[string]jsast.JsExpr{},
		PlainInitializers:   map[string]jsast.JsExpr{},
		Functions:           map[string]*jsast.JsFuncDecl{},
	}
	if plan == nil {
		return ctx
	}
	for _, prop := range plan.Props {
		ctx.Props[prop.SourceName] = prop
	}
	for _, name := range plan.ReactiveNames {
		ctx.ReactiveNames[name] = true
	}
	for name, initializer := range plan.StateInitializers {
		ctx.StateInitializers[name] = initializer
	}
	for name, initializer := range plan.DerivedInitializers {
		ctx.DerivedInitializers[name] = initializer
	}
	for name, initializer := range plan.PlainInitializers {
		ctx.PlainInitializers[name] = initializer
	}
	for name, function := range plan.Functions {
		ctx.Functions[name] = function
	}
	ctx.scriptOffset = plan.ScriptOffset
	ctx.DeepInitializers = plan.DeepInitializers
	ctx.Hosts = plan.Hosts
	ctx.HostSymbols = plan.HostSymbols
	ctx.HostDecls = plan.HostDecls
	ctx.ModuleRefs = plan.ModuleRefs
	ctx.localIndex = map[string]int{}
	for index, name := range plan.LocalOrder {
		ctx.localIndex[name] = index
	}
	return ctx
}

// TranslateSSRExpression lowers one cached TemplateExpr into Go AST through
// the Server IR. It does not accept source text, so all renderer paths
// share the parser and expression ownership of ComponentAST.
func TranslateSSRExpression(expr *sfcast.TemplateExpr, ctx SSRExprContext) (goast.Expr, error) {
	ir, err := BuildSSRIR(expr, ctx)
	if err != nil {
		return nil, err
	}
	return lowerSSRIR(ir), nil
}

func translateSSRExpr(expr jsast.JsExpr, ctx SSRExprContext, pos int) (goast.Expr, error) {
	ir, err := buildSSRIR(expr, ctx, pos)
	if err != nil {
		return nil, err
	}
	return lowerSSRIR(ir), nil
}

func ssrGoExprKnownString(expr goast.Expr, ctx SSRExprContext) bool {
	switch node := expr.(type) {
	case *goast.BasicLit:
		return node.Kind == token.STRING
	case *goast.SelectorExpr:
		base, ok := node.X.(*goast.Ident)
		if !ok || base.Name != "props" {
			return false
		}
		for _, prop := range ctx.Props {
			if prop.GoName == node.Sel.Name {
				return prop.Type == SSRTypeString
			}
		}
	case *goast.BinaryExpr:
		return node.Op == token.ADD && ssrGoExprKnownString(node.X, ctx) && ssrGoExprKnownString(node.Y, ctx)
	}
	return false
}

func ssrGoExprKnownNumber(expr goast.Expr, ctx SSRExprContext) bool {
	switch node := expr.(type) {
	case *goast.BasicLit:
		return node.Kind == token.INT || node.Kind == token.FLOAT
	case *goast.SelectorExpr:
		base, ok := node.X.(*goast.Ident)
		if !ok || base.Name != "props" {
			return false
		}
		for _, prop := range ctx.Props {
			if prop.GoName == node.Sel.Name {
				return prop.Type == SSRTypeInt || prop.Type == SSRTypeFloat
			}
		}
	case *goast.CallExpr:
		name, ok := node.Fun.(*goast.Ident)
		return ok && name.Name == "float64"
	case *goast.BinaryExpr:
		return ssrGoExprKnownNumber(node.X, ctx) && ssrGoExprKnownNumber(node.Y, ctx)
	}
	return false
}

// ssrLazyExpr represents a source expression as a zero-argument Go closure.
// It is used only at JavaScript short-circuit boundaries, where Go evaluates
// function arguments eagerly but the runtime must decide which closure body
// to execute. Keeping this in AST form avoids recreating source text and
// guarantees that an unsafe unselected member/index read is never evaluated.
func ssrLazyExpr(value goast.Expr) goast.Expr {
	return &goast.FuncLit{
		Type: &goast.FuncType{
			Params:  &goast.FieldList{},
			Results: &goast.FieldList{List: []*goast.Field{{Type: goast.NewIdent("any")}}},
		},
		Body: &goast.BlockStmt{List: []goast.Stmt{
			&goast.ReturnStmt{Results: []goast.Expr{value}},
		}},
	}
}

func translateSSRObjectKey(key jsast.JsExpr, pos int) (string, error) {
	switch node := key.(type) {
	case *jsast.JsIdent:
		return node.Name, nil
	case *jsast.JsLiteral:
		if node.Kind == "number" {
			// A numeric key is its number's text: 0x10 is "16".
			if spelled, ok := ssrJSNumberLiteral(node.Raw); ok {
				if value, err := strconv.ParseFloat(spelled, 64); err == nil && value >= 0 && value < 1e21 && (value == 0 || value >= 1e-6) {
					return strconv.FormatFloat(value, 'f', -1, 64), nil
				}
			}
		}
		if node.Kind != "string" {
			return "", ssrExprUnsupported(pos, "only identifier, string, and number object property names are available to static SSR")
		}
		if node.Decoded {
			return node.Value, nil
		}
		value, err := decodeSSRJSStringLiteral(node.Raw)
		if err != nil {
			return "", ssrExprUnsupported(pos, "object property name cannot be translated safely")
		}
		return value, nil
	default:
		return "", ssrExprUnsupported(pos, "only identifier and string object property names are available to static SSR")
	}
}

// decodeSSRJSStringLiteral decodes the parser-owned token spelling without
// reparsing template/script source. Go's strconv.Unquote cannot accept a JS
// single-quoted string, so the small baseline decoder handles the shared JS
// escapes explicitly and leaves broader escape grammar for the JS capability
// matrix.
func decodeSSRJSStringLiteral(raw string) (string, error) {
	if len(raw) < 2 || (raw[0] != '\'' && raw[0] != '"') || raw[len(raw)-1] != raw[0] {
		return "", strconv.ErrSyntax
	}
	return decodeSSRJSStringContent(raw[1:len(raw)-1], false)
}

func decodeSSRTemplateQuasi(raw string) (string, error) {
	return decodeSSRJSStringContent(raw, true)
}

func decodeSSRJSStringContent(raw string, template bool) (string, error) {
	// The string is built as UTF-16 code units: escapes may spell surrogate
	// pairs (\ud83d\ude00) or lone surrogates, which keep their WTF-8 form
	// as in the runtime (T-6.3b).
	var out ssrUnitBuilder
	for index := 0; index < len(raw); index++ {
		char := raw[index]
		if char != '\\' {
			r, size := utf8.DecodeRuneInString(raw[index:])
			out.WriteRune(r)
			index += size - 1
			continue
		}
		index++
		if index >= len(raw) {
			return "", strconv.ErrSyntax
		}
		switch raw[index] {
		case '\'', '"', '\\':
			out.WriteCode(raw[index])
		case '`', '$':
			if !template {
				return "", strconv.ErrSyntax
			}
			out.WriteCode(raw[index])
		case 'n':
			out.WriteCode('\n')
		case 'r':
			out.WriteCode('\r')
		case 't':
			out.WriteCode('\t')
		case 'b':
			out.WriteCode('\b')
		case 'f':
			out.WriteCode('\f')
		case 'v':
			out.WriteCode('\v')
		case '0':
			if index+1 < len(raw) && raw[index+1] >= '0' && raw[index+1] <= '9' {
				return "", strconv.ErrSyntax
			}
			out.WriteCode(0)
		case 'x':
			if index+2 >= len(raw) {
				return "", strconv.ErrSyntax
			}
			n, err := strconv.ParseUint(raw[index+1:index+3], 16, 8)
			if err != nil {
				return "", strconv.ErrSyntax
			}
			out.WriteRune(rune(n))
			index += 2
		case 'u':
			if index+1 < len(raw) && raw[index+1] == '{' {
				end := strings.IndexByte(raw[index:], '}')
				if end < 0 {
					return "", strconv.ErrSyntax
				}
				n, err := strconv.ParseUint(raw[index+2:index+end], 16, 32)
				if err != nil || n > 0x10FFFF {
					return "", strconv.ErrSyntax
				}
				out.WriteRune(rune(n))
				index += end
				break
			}
			if index+4 >= len(raw) {
				return "", strconv.ErrSyntax
			}
			n, err := strconv.ParseUint(raw[index+1:index+5], 16, 16)
			if err != nil {
				return "", strconv.ErrSyntax
			}
			out.WriteUnit(uint16(n))
			index += 4
		default:
			return "", strconv.ErrSyntax
		}
	}
	return out.String(), nil
}

// ssrUnitBuilder accumulates UTF-16 code units and encodes them as the
// runtime does: pairs as UTF-8, lone surrogates as WTF-8.
type ssrUnitBuilder struct{ units []uint16 }

func (b *ssrUnitBuilder) WriteUnit(u uint16) { b.units = append(b.units, u) }
func (b *ssrUnitBuilder) WriteCode(c byte)   { b.units = append(b.units, uint16(c)) }
func (b *ssrUnitBuilder) WriteRune(r rune) {
	if r >= 0x10000 {
		high, low := utf16.EncodeRune(r)
		b.units = append(b.units, uint16(high), uint16(low))
		return
	}
	b.units = append(b.units, uint16(r))
}

func (b *ssrUnitBuilder) String() string {
	var out strings.Builder
	u := b.units
	for i := 0; i < len(u); i++ {
		c := rune(u[i])
		if c >= 0xD800 && c <= 0xDBFF && i+1 < len(u) && u[i+1] >= 0xDC00 && u[i+1] <= 0xDFFF {
			out.WriteRune(utf16.DecodeRune(c, rune(u[i+1])))
			i++
			continue
		}
		if c >= 0xD800 && c <= 0xDFFF {
			out.WriteByte(byte(0xE0 | c>>12))
			out.WriteByte(byte(0x80 | (c>>6)&0x3F))
			out.WriteByte(byte(0x80 | c&0x3F))
			continue
		}
		out.WriteRune(c)
	}
	return out.String()
}

func ssrBinaryToken(op string) (token.Token, bool) {
	switch op {
	case "+":
		return token.ADD, true
	case "-":
		return token.SUB, true
	case "*":
		return token.MUL, true
	case "/":
		return token.QUO, true
	case "%":
		return token.REM, true
	case "<":
		return token.LSS, true
	case "<=":
		return token.LEQ, true
	case ">":
		return token.GTR, true
	case ">=":
		return token.GEQ, true
	default:
		return token.ILLEGAL, false
	}
}

func ssrExprUnsupported(pos int, format string, args ...interface{}) error {
	return &SSRExpressionError{Pos: pos, Message: fmt.Sprintf(format, args...)}
}

func FormatSSRGoExpr(expr goast.Expr) (string, error) {
	if expr == nil {
		return "", fmt.Errorf("nil Go expression")
	}
	var out bytes.Buffer
	if err := format.Node(&out, token.NewFileSet(), expr); err != nil {
		return "", err
	}
	return out.String(), nil
}
