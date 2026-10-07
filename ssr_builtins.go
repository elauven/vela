package main

import (
	"fmt"
	goast "go/ast"
	"go/token"
	"math"
	"sort"
	"strconv"
	"strings"
)

// The built-in catalogue (docs/SSR_TRANSLATION_CONTRACT.md V-11.1a, task
// 046): which JavaScript built-ins the server translates and how. Method
// calls on values dispatch on the receiver at run time (ssrJS.Method), so
// one entry serves strings and arrays alike; the compiler admits only the
// names listed here.

// ssrBuiltinMethods are the admitted methods of strings, arrays, and
// numbers, with the result facts that hold whatever the receiver is.
var ssrBuiltinMethods = map[string]ssrIRFacts{
	// Strings (§2.2) and arrays (§2.3).
	"at": {}, "includes": {Bool: true}, "indexOf": {Number: true}, "lastIndexOf": {Number: true},
	"slice": {}, "concat": {}, "toString": {String: true}, "valueOf": {},
	// Strings.
	"charAt": {String: true}, "charCodeAt": {Number: true}, "codePointAt": {},
	"startsWith": {Bool: true}, "endsWith": {Bool: true}, "substring": {String: true},
	"toUpperCase": {String: true}, "toLowerCase": {String: true},
	"trim": {String: true}, "trimStart": {String: true}, "trimEnd": {String: true},
	"padStart": {String: true}, "padEnd": {String: true}, "repeat": {String: true},
	"split": {Array: true}, "replace": {String: true}, "replaceAll": {String: true},
	// Arrays.
	"join": {String: true}, "map": {Array: true}, "filter": {Array: true}, "forEach": {},
	"some": {Bool: true}, "every": {Bool: true}, "find": {}, "findIndex": {Number: true},
	"findLast": {}, "findLastIndex": {Number: true}, "reduce": {}, "reduceRight": {},
	"flat": {Array: true}, "flatMap": {Array: true}, "toSorted": {Array: true}, "toReversed": {Array: true},
	// Numbers (§2.6).
	"toFixed": {String: true}, "toPrecision": {String: true}, "toExponential": {String: true},
	// Strings, by the decisions of §5.
	"normalize": {String: true},
}

// ssrMutatingMethods change the array they are called on (T-6.6b). sort,
// reverse, and fill keep the array's length and are admitted on any array
// the function created; the others change its length and also need the
// array not to be copied into another variable or value.
var ssrMutatingMethods = map[string]bool{"push": true, "pop": true, "shift": true, "unshift": true, "splice": true, "sort": false, "reverse": false, "fill": false}

// ssrFreshMethods return a new array or object.
var ssrFreshMethods = map[string]bool{"map": true, "filter": true, "slice": true, "concat": true, "flat": true, "flatMap": true, "toSorted": true, "toReversed": true, "split": true}

// ssrBuiltinGlobal lowers one global function or namespace function.
type ssrBuiltinGlobal struct {
	// Helper is vela.<Helper>(args...); JS is ssrJS.<Helper>(site, args...).
	Helper string
	JS     bool
	// Args fixes the number of arguments (missing ones are undefined);
	// -1 passes all arguments (variadic).
	Args  int
	Facts ssrIRFacts
}

var ssrBuiltinGlobals = map[string]ssrBuiltinGlobal{
	// §2.1
	"String":               {Helper: "ToStringValue", Args: 1, Facts: ssrIRFacts{String: true}},
	"Number":               {Helper: "ToNumber", Args: 1, Facts: ssrIRFacts{Number: true}},
	"Boolean":              {Helper: "ToBoolean", Args: 1, Facts: ssrIRFacts{Bool: true}},
	"parseInt":             {Helper: "ParseInt", Args: 2, Facts: ssrIRFacts{Number: true}},
	"parseFloat":           {Helper: "ParseFloat", Args: 1, Facts: ssrIRFacts{Number: true}},
	"isNaN":                {Helper: "IsNaN", Args: 1, Facts: ssrIRFacts{Bool: true}},
	"isFinite":             {Helper: "IsFinite", Args: 1, Facts: ssrIRFacts{Bool: true}},
	"encodeURIComponent":   {Helper: "EncodeURIComponent", JS: true, Args: 1},
	"encodeURI":            {Helper: "EncodeURI", JS: true, Args: 1},
	"decodeURIComponent":   {Helper: "DecodeURIComponent", JS: true, Args: 1},
	"decodeURI":            {Helper: "DecodeURI", JS: true, Args: 1},
	"Number.isNaN":         {Helper: "NumberIsNaN", Args: 1, Facts: ssrIRFacts{Bool: true}},
	"Number.isFinite":      {Helper: "NumberIsFinite", Args: 1, Facts: ssrIRFacts{Bool: true}},
	"Number.isInteger":     {Helper: "NumberIsInteger", Args: 1, Facts: ssrIRFacts{Bool: true}},
	"Number.isSafeInteger": {Helper: "NumberIsSafeInteger", Args: 1, Facts: ssrIRFacts{Bool: true}},
	"Number.parseInt":      {Helper: "ParseInt", Args: 2, Facts: ssrIRFacts{Number: true}},
	"Number.parseFloat":    {Helper: "ParseFloat", Args: 1, Facts: ssrIRFacts{Number: true}},
	"Array.isArray":        {Helper: "IsArray", Args: 1, Facts: ssrIRFacts{Bool: true}},
	// §2.3
	"Array.from": {Helper: "ArrayFrom", JS: true, Args: 2},
	"Array.of":   {Helper: "ArrayOf", Args: -1, Facts: ssrIRFacts{Array: true}},
	// §2.4
	"Object.keys":        {Helper: "ObjectKeys", JS: true, Args: 1, Facts: ssrIRFacts{}},
	"Object.values":      {Helper: "ObjectValues", JS: true, Args: 1},
	"Object.entries":     {Helper: "ObjectEntries", JS: true, Args: 1},
	"Object.fromEntries": {Helper: "ObjectFromEntries", JS: true, Args: 1},
	"Object.assign":      {Helper: "ObjectAssign", JS: true, Args: -1},
	"Object.hasOwn":      {Helper: "ObjectHasOwn", JS: true, Args: 2},
	// §2.5
	"Math.round": {Helper: "MathRound", Args: 1, Facts: ssrIRFacts{Number: true}},
	"Math.floor": {Helper: "MathFloor", Args: 1, Facts: ssrIRFacts{Number: true}},
	"Math.ceil":  {Helper: "MathCeil", Args: 1, Facts: ssrIRFacts{Number: true}},
	"Math.trunc": {Helper: "MathTrunc", Args: 1, Facts: ssrIRFacts{Number: true}},
	"Math.abs":   {Helper: "MathAbs", Args: 1, Facts: ssrIRFacts{Number: true}},
	"Math.sign":  {Helper: "MathSign", Args: 1, Facts: ssrIRFacts{Number: true}},
	"Math.sqrt":  {Helper: "MathSqrt", Args: 1, Facts: ssrIRFacts{Number: true}},
	"Math.cbrt":  {Helper: "MathCbrt", Args: 1, Facts: ssrIRFacts{Number: true}},
	"Math.min":   {Helper: "MathMin", Args: -1, Facts: ssrIRFacts{Number: true}},
	"Math.max":   {Helper: "MathMax", Args: -1, Facts: ssrIRFacts{Number: true}},
	"Math.hypot": {Helper: "MathHypot", Args: -1, Facts: ssrIRFacts{Number: true}},
	"Math.pow":   {Helper: "Pow", Args: 2, Facts: ssrIRFacts{Number: true}},
	"Math.atan2": {Helper: "MathAtan2", Args: 2, Facts: ssrIRFacts{Number: true}},
	"Math.imul":  {Helper: "MathImul", Args: 2, Facts: ssrIRFacts{Number: true}},
	// §2.7
	"JSON.stringify": {Helper: "JSONStringify", JS: true, Args: 3},
	"JSON.parse":     {Helper: "JSONParse", JS: true, Args: 1},
}

// ssrBuiltinConstants are the admitted constant properties.
var ssrBuiltinConstants = map[string]float64{
	"Math.PI": math.Pi, "Math.E": math.E, "Math.LN2": math.Ln2, "Math.LN10": math.Ln10,
	"Math.LOG2E": math.Log2E, "Math.LOG10E": math.Log10E, "Math.SQRT2": math.Sqrt2, "Math.SQRT1_2": math.Sqrt2 / 2,
	"Number.MAX_SAFE_INTEGER": 9007199254740991, "Number.MIN_SAFE_INTEGER": -9007199254740991,
	"Number.EPSILON": 2.220446049250313e-16, "Number.MAX_VALUE": math.MaxFloat64, "Number.MIN_VALUE": 5e-324,
	"Number.POSITIVE_INFINITY": math.Inf(1), "Number.NEGATIVE_INFINITY": math.Inf(-1), "Number.NaN": math.NaN(),
}

// ssrUndecidedBuiltins name the built-ins whose admission waits on a
// product decision (matrix §5).
var ssrUndecidedBuiltins = map[string]string{}

func init() {
	// One-argument Math functions: the transcendental ones use Go math,
	// within one unit in the last place of V8 (decision of §5).
	for _, name := range []string{"log", "log2", "log10", "log1p", "exp", "expm1", "sin", "cos", "tan", "asin", "acos", "atan", "sinh", "cosh", "tanh", "asinh", "acosh", "atanh", "fround", "clz32"} {
		ssrBuiltinGlobals["Math."+name] = ssrBuiltinGlobal{Helper: "Math" + strings.ToUpper(name[:1]) + name[1:], Args: 1, Facts: ssrIRFacts{Number: true}}
	}
}

// shadowedGlobal reports whether a global name is bound by the code being
// translated.
func (b *ssrIRBuilder) shadowedGlobal(name string) bool {
	if b.shadowed(name) {
		return true
	}
	ctx := b.ctx
	for _, names := range []map[string]JsExpr{ctx.StateInitializers, ctx.DerivedInitializers, ctx.PlainInitializers, ctx.DeepInitializers} {
		if _, ok := names[name]; ok {
			return true
		}
	}
	if _, ok := ctx.Functions[name]; ok {
		return true
	}
	if _, ok := ctx.ModuleRefs[name]; ok {
		return true
	}
	if _, ok := ctx.HostSymbols[name]; ok {
		return true
	}
	return false
}

// globalName returns "Math.floor" for Math.floor and "parseInt" for
// parseInt when the root is an unshadowed global.
func (b *ssrIRBuilder) globalName(callee JsExpr) string {
	switch c := callee.(type) {
	case *JsIdent:
		if !b.shadowedGlobal(c.Name) {
			return c.Name
		}
	case *JsMemberExpr:
		object, ok := c.Obj.(*JsIdent)
		property, named := c.Prop.(*JsIdent)
		if ok && named && !c.Computed && !c.Optional && ssrGlobalNamespaces[object.Name] && !b.shadowedGlobal(object.Name) {
			return object.Name + "." + property.Name
		}
	}
	return ""
}

// spreadArgs lowers the arguments of a call with a spread argument to one
// []any, passed with ... to a variadic Go function; spread is nil when no
// argument is spread.
func (b *ssrIRBuilder) spreadArgs(call *JsCallExpr, pos int) (goast.Expr, error) {
	for _, argument := range call.Args {
		if _, spread := argument.(*JsSpreadExpr); spread {
			array, err := b.build(&JsArrayExpr{Elems: call.Args}, pos)
			if err != nil {
				return nil, err
			}
			return lowerSSRIR(array), nil
		}
	}
	return nil, nil
}

func (b *ssrIRBuilder) buildArgs(call *JsCallExpr, pos int) ([]SSRIRExpr, error) {
	args := make([]SSRIRExpr, 0, len(call.Args))
	for _, argument := range call.Args {
		if _, spread := argument.(*JsSpreadExpr); spread {
			return nil, ssrExprUnsupported(pos, "spread arguments are not supported yet")
		}
		value, err := b.build(argument, pos)
		if err != nil {
			return nil, err
		}
		args = append(args, value)
	}
	return args, nil
}

// builtinCall lowers a call to a global function, a namespace function, or
// an admitted method. handled is false when the call is none of them.
func (b *ssrIRBuilder) builtinCall(call *JsCallExpr, pos int) (SSRIRExpr, bool, error) {
	if !b.js {
		return nil, false, nil
	}
	if name := b.globalName(call.Callee); name != "" {
		if method, ok := strings.CutPrefix(name, "console."); ok {
			return b.consoleCall(method, call, pos)
		}
		if what, undecided := ssrUndecidedBuiltins[name]; undecided {
			return nil, true, ssrExprUnsupported(pos, "%s is not available during server rendering: %s await a product decision (docs/SSR_TRANSLATION_MATRIX.md §5); implement the calling function as a host", name, what)
		}
		global, ok := ssrBuiltinGlobals[name]
		if !ok {
			return nil, false, nil
		}
		spread, err := b.spreadArgs(call, pos)
		if err != nil {
			return nil, true, err
		}
		if spread != nil {
			if global.Args >= 0 {
				return nil, true, ssrExprUnsupported(pos, "spread arguments to %s are not supported yet", name)
			}
			fun := goSelector("vela", global.Helper)
			var fixed []goast.Expr
			if global.JS {
				fun = goSelector("ssrJS", global.Helper)
				fixed = []goast.Expr{ssrGoString(b.site)}
				b.usedJS, b.throws = true, true
			}
			if name == "Object.assign" {
				return nil, true, ssrExprUnsupported(pos, "spread arguments to Object.assign are not supported yet")
			}
			return &ssrIRGo{ssrIRBase: b.base(pos, global.Facts), Expr: &goast.CallExpr{Fun: fun, Args: append(fixed, spread), Ellipsis: 1}}, true, nil
		}
		args, err := b.buildArgs(call, pos)
		if err != nil {
			return nil, true, err
		}
		lowered := make([]goast.Expr, 0, len(args)+1)
		for _, arg := range args {
			lowered = append(lowered, lowerSSRIR(arg))
		}
		if global.Args >= 0 {
			for len(lowered) < global.Args {
				lowered = append(lowered, ssrUndefined())
			}
			lowered = lowered[:global.Args]
		}
		if name == "String" && len(call.Args) == 0 {
			lowered = []goast.Expr{ssrGoString("")}
		}
		if name == "Number" && len(call.Args) == 0 {
			lowered = []goast.Expr{&goast.BasicLit{Kind: token.FLOAT, Value: "0.0"}}
		}
		fun := goSelector("vela", global.Helper)
		if global.JS {
			fun = goSelector("ssrJS", global.Helper)
			lowered = append([]goast.Expr{ssrGoString(b.site)}, lowered...)
			b.usedJS, b.throws = true, true
		}
		if name == "Object.assign" {
			if len(call.Args) == 0 {
				return nil, true, ssrExprUnsupported(pos, "Object.assign needs a target")
			}
			if _, literal := call.Args[0].(*JsObjectExpr); !literal {
				return nil, true, ssrImpure(pos, "Object.assign changes its first argument; during server rendering it must be an object literal, as in Object.assign({}, a, b) (T-6.6b)")
			}
		}
		return &ssrIRGo{ssrIRBase: b.base(pos, global.Facts), Expr: &goast.CallExpr{Fun: fun, Args: lowered}}, true, nil
	}
	member, ok := call.Callee.(*JsMemberExpr)
	if !ok || member.Computed {
		return nil, false, nil
	}
	property, ok := member.Prop.(*JsIdent)
	if !ok {
		return nil, false, nil
	}
	name := property.Name
	facts, admitted := ssrBuiltinMethods[name]
	if mutating, isMutating := ssrMutatingMethods[name]; isMutating {
		if mutating || !ssrFreshExpr(member.Obj) {
			if b.funcCompiler != nil && b.funcCompiler.deferImpure {
				// A callback that changes state fails only if the render calls it.
				b.usedJS, b.throws = true, true
				message := fmt.Sprintf("%s changes an array the function did not create (T-6.6a)", name)
				return &ssrIRGo{ssrIRBase: b.base(pos, ssrIRFacts{}), Expr: &goast.CallExpr{Fun: goSelector("ssrJS", "Impure"), Args: []goast.Expr{ssrGoString(message), ssrGoString(b.site)}}}, true, nil
			}
			return nil, true, ssrImpure(pos, "%s changes its array; during server rendering it is admitted only as a statement on an array the function created (T-6.6b)", name)
		}
		admitted, facts = true, ssrIRFacts{}
	}
	if !admitted {
		return nil, false, nil
	}
	receiver, err := b.build(member.Obj, pos)
	if err != nil {
		return nil, true, err
	}
	lowered := []goast.Expr{ssrGoString(b.site), lowerSSRIR(receiver), ssrGoString(name)}
	spread, err := b.spreadArgs(call, pos)
	if err != nil {
		return nil, true, err
	}
	ellipsis := token.NoPos
	if spread != nil {
		lowered, ellipsis = append(lowered, spread), 1
	} else {
		args, err := b.buildArgs(call, pos)
		if err != nil {
			return nil, true, err
		}
		for _, arg := range args {
			lowered = append(lowered, lowerSSRIR(arg))
		}
	}
	o := receiver.irBase().Facts
	switch {
	case o.String && (name == "slice" || name == "concat" || name == "at"):
		if name != "at" {
			facts = ssrIRFacts{String: true}
		}
	case o.Array && (name == "slice" || name == "concat"):
		facts = ssrIRFacts{Array: true}
	}
	b.usedJS, b.throws = true, true
	// Method returns any; a result type the facts promise is converted
	// (after a throw the value is not used).
	var expr goast.Expr = &goast.CallExpr{Fun: goSelector("ssrJS", "Method"), Args: lowered, Ellipsis: ellipsis}
	facts.Array = false
	switch {
	case facts.String:
		expr = &goast.CallExpr{Fun: goSelector("vela", "String"), Args: []goast.Expr{expr}}
	case facts.Number:
		expr = &goast.CallExpr{Fun: goSelector("vela", "ToNumber"), Args: []goast.Expr{expr}}
	case facts.Bool:
		expr = &goast.CallExpr{Fun: goSelector("vela", "Truthy"), Args: []goast.Expr{expr}}
	}
	return &ssrIRGo{ssrIRBase: b.base(pos, facts), Expr: expr}, true, nil
}

// builtinConstant lowers Math.PI and the other admitted constants.
func (b *ssrIRBuilder) builtinConstant(node *JsMemberExpr, pos int) (SSRIRExpr, bool) {
	name := b.globalName(node)
	value, ok := ssrBuiltinConstants[name]
	if !ok {
		return nil, false
	}
	var expr goast.Expr = &goast.CallExpr{Fun: goast.NewIdent("float64"), Args: []goast.Expr{&goast.BasicLit{Kind: token.FLOAT, Value: strconv.FormatFloat(value, 'g', -1, 64)}}}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		// NaN and the infinities have no Go literal; ToNumber spells them.
		expr = &goast.CallExpr{Fun: goSelector("vela", "ToNumber"), Args: []goast.Expr{ssrGoString(ssrJSNumberText(value))}}
	}
	return &ssrIRGo{ssrIRBase: b.base(pos, ssrIRFacts{Number: true}), Expr: expr}, true
}

func ssrJSNumberText(value float64) string {
	switch {
	case math.IsNaN(value):
		return "NaN"
	case math.IsInf(value, 1):
		return "Infinity"
	case math.IsInf(value, -1):
		return "-Infinity"
	}
	return strconv.FormatFloat(value, 'g', -1, 64)
}

// ssrFreshExpr reports an expression that creates a new array or object:
// a literal, or a call to a built-in that returns a new one.
func ssrFreshExpr(expr JsExpr) bool {
	switch e := expr.(type) {
	case *JsArrayExpr, *JsObjectExpr:
		return true
	case *JsCallExpr:
		member, ok := e.Callee.(*JsMemberExpr)
		if !ok || member.Computed {
			return false
		}
		property, ok := member.Prop.(*JsIdent)
		if !ok {
			return false
		}
		if object, ok := member.Obj.(*JsIdent); ok {
			switch object.Name + "." + property.Name {
			case "Object.keys", "Object.values", "Object.entries", "Object.fromEntries", "Array.from", "Array.of", "JSON.parse":
				return true
			}
		}
		return ssrFreshMethods[property.Name]
	}
	return false
}

// ── Freshness of locals (T-6.6b) ─────────────────────────────────────────

// ssrFreshness records, by name, the locals of a translated function that
// hold an array or object the function created, and the arrays among them
// that are never copied into another variable or value, so that methods
// changing their length can reassign them.
type ssrFreshness struct {
	fresh     map[string]bool
	unaliased map[string]bool
}

// ssrAnalyzeFreshness analyses a function body; names are tracked
// conservatively across scopes.
func ssrAnalyzeFreshness(body *JsBlock) *ssrFreshness {
	f := &ssrFreshness{fresh: map[string]bool{}, unaliased: map[string]bool{}}
	stale := map[string]bool{}
	aliased := map[string]bool{}
	readOnly := map[string]bool{"Math": true, "JSON": true, "Number": true, "String": true, "Boolean": true, "Array": true, "Object": true}
	WalkNode(body, func(n JsNode) bool {
		switch node := n.(type) {
		case *JsVarDecl:
			for _, declarator := range node.Decls {
				name := identName(declarator.Id)
				if name == "" {
					continue
				}
				if declarator.Init != nil && ssrFreshExpr(declarator.Init) {
					f.fresh[name] = true
				} else {
					stale[name] = true
				}
				if alias := identName(declarator.Init); alias != "" {
					aliased[alias] = true
				}
			}
		case *JsAssignExpr:
			if name := identName(node.L); name != "" && (node.Op != "=" || !ssrFreshExpr(node.R)) {
				stale[name] = true
			}
			if alias := identName(node.R); alias != "" {
				aliased[alias] = true
			}
		case *JsArrayExpr:
			for _, element := range node.Elems {
				if alias := identName(element); alias != "" {
					aliased[alias] = true
				}
			}
		case *JsObjectExpr:
			for _, property := range node.Props {
				if property.Spread {
					continue
				}
				value := property.Value
				if value == nil {
					value = property.Key
				}
				if alias := identName(value); alias != "" {
					aliased[alias] = true
				}
			}
		case *JsCallExpr:
			builtin := false
			if member, ok := node.Callee.(*JsMemberExpr); ok {
				if object, ok := member.Obj.(*JsIdent); ok && readOnly[object.Name] {
					builtin = true
				}
				if property, ok := member.Prop.(*JsIdent); ok && !member.Computed {
					if _, method := ssrBuiltinMethods[property.Name]; method && property.Name != "concat" {
						builtin = true
					}
				}
			}
			if !builtin {
				for _, argument := range node.Args {
					if alias := identName(argument); alias != "" {
						aliased[alias] = true
					}
				}
			} else if member, ok := node.Callee.(*JsMemberExpr); ok {
				// push(x) and unshift(x) store x in the array.
				if property, ok := member.Prop.(*JsIdent); ok && (property.Name == "push" || property.Name == "unshift" || property.Name == "splice" || property.Name == "fill") {
					for _, argument := range node.Args {
						if alias := identName(argument); alias != "" {
							aliased[alias] = true
						}
					}
				}
			}
		}
		return true
	})
	for name := range f.fresh {
		if stale[name] {
			delete(f.fresh, name)
			continue
		}
		if !aliased[name] {
			f.unaliased[name] = true
		}
	}
	return f
}

// ssrMutationStatement lowers xs.push(…) and the other methods that change
// an array the function created, as a statement or the initializer or
// right-hand side of target (empty: the result is dropped).
func (c *ssrFuncCompiler) mutationStatement(call *JsCallExpr, target string, declare bool) ([]goast.Stmt, bool, error) {
	member, ok := call.Callee.(*JsMemberExpr)
	if !ok || member.Computed {
		return nil, false, nil
	}
	property, ok := member.Prop.(*JsIdent)
	if !ok {
		return nil, false, nil
	}
	changesLength, mutating := ssrMutatingMethods[property.Name]
	receiver := identName(member.Obj)
	if !mutating || receiver == "" {
		return nil, false, nil
	}
	goName, local := c.lookup(receiver)
	if !local || !c.freshness.fresh[receiver] || changesLength && !c.freshness.unaliased[receiver] {
		reason := "an array the function created"
		if changesLength {
			reason = "an array the function created and does not copy into another variable or value"
		}
		problem := fmt.Sprintf("%s.%s changes %q; during server rendering it is admitted only on %s (T-6.6b)", receiver, property.Name, receiver, reason)
		stmts, err := c.impure(problem)
		return stmts, true, err
	}
	b := c.builder()
	args, err := b.buildArgs(call, 0)
	c.absorb(b)
	if err != nil {
		return nil, true, err
	}
	lowered := []goast.Expr{ssrGoString(c.site), goast.NewIdent(goName), ssrGoString(property.Name)}
	for _, arg := range args {
		lowered = append(lowered, lowerSSRIR(arg))
	}
	var out []goast.Stmt
	if target != "" && declare {
		out = append(out, ssrVarAny(target, nil), ssrUse(target))
	}
	array, result := c.declare("$array"), c.declare("$result")
	out = append(out,
		&goast.AssignStmt{Lhs: []goast.Expr{goast.NewIdent(array), goast.NewIdent(result)}, Tok: token.DEFINE, Rhs: []goast.Expr{&goast.CallExpr{Fun: goSelector("ssrJS", "MutateArray"), Args: lowered}}},
		&goast.AssignStmt{Lhs: []goast.Expr{goast.NewIdent(goName)}, Tok: token.ASSIGN, Rhs: []goast.Expr{goast.NewIdent(array)}},
		ssrUse(result),
	)
	if target != "" {
		out = append(out, &goast.AssignStmt{Lhs: []goast.Expr{goast.NewIdent(target)}, Tok: token.ASSIGN, Rhs: []goast.Expr{goast.NewIdent(result)}})
	}
	return append(out, c.unwind()), true, nil
}

// ssrCatalogueNames lists every admitted built-in, for the matrix drift
// test.
func ssrCatalogueNames() []string {
	var names []string
	for name := range ssrBuiltinGlobals {
		names = append(names, name)
	}
	for name := range ssrBuiltinConstants {
		names = append(names, name)
	}
	for name := range ssrBuiltinMethods {
		names = append(names, "."+name)
	}
	for name := range ssrMutatingMethods {
		names = append(names, "."+name)
	}
	sort.Strings(names)
	return names
}

// mutationTarget reports a call of a method that changes an array, and
// its receiver name.
func (c *ssrFuncCompiler) mutationTarget(call *JsCallExpr) (string, bool) {
	member, ok := call.Callee.(*JsMemberExpr)
	if !ok || member.Computed {
		return "", false
	}
	property, ok := member.Prop.(*JsIdent)
	if !ok {
		return "", false
	}
	if _, mutating := ssrMutatingMethods[property.Name]; !mutating {
		return "", false
	}
	receiver := identName(member.Obj)
	return receiver, receiver != ""
}

// ssrGlobalFunctionValues are the global functions admitted as values.
var ssrGlobalFunctionValues = map[string]bool{"String": true, "Number": true, "Boolean": true, "parseInt": true, "parseFloat": true, "isNaN": true, "isFinite": true, "encodeURIComponent": true, "encodeURI": true, "decodeURIComponent": true, "decodeURI": true}

// ssrConsoleMethods are the console methods admitted in server code; the
// calls go to the observer of vela.WithConsoleObserver (decision of §5).
var ssrConsoleMethods = map[string]bool{"log": true, "info": true, "warn": true, "error": true, "debug": true}

func (b *ssrIRBuilder) consoleCall(method string, call *JsCallExpr, pos int) (SSRIRExpr, bool, error) {
	if !ssrConsoleMethods[method] {
		return nil, true, ssrExprUnsupported(pos, "console.%s is not available during server rendering; use log, info, warn, error, or debug", method)
	}
	lowered := []goast.Expr{b.contextExpr(), ssrGoString(method)}
	spread, err := b.spreadArgs(call, pos)
	if err != nil {
		return nil, true, err
	}
	ellipsis := token.NoPos
	if spread != nil {
		lowered, ellipsis = append(lowered, spread), 1
	} else {
		args, err := b.buildArgs(call, pos)
		if err != nil {
			return nil, true, err
		}
		for _, arg := range args {
			lowered = append(lowered, lowerSSRIR(arg))
		}
	}
	b.usedJS = true
	return &ssrIRGo{ssrIRBase: b.base(pos, ssrIRFacts{}), Expr: &goast.CallExpr{Fun: goSelector("ssrJS", "Console"), Args: lowered, Ellipsis: ellipsis}}, true, nil
}
