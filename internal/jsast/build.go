package jsast

import "strconv"

// ─────────────────────────────────────────────
//  AST construction helpers
// ─────────────────────────────────────────────
//
// Hand-building AST nodes is verbose, so these helpers cover the shapes
// the code generators reach for most often. Every compiler-authored construct is made
// through typed nodes; user-authored <script> enters separately through the
// cached source-ordered ScriptPlan. Everything constructed here is marked
// OriginGenerated.

// GenNode marks a freshly built node as compiler-generated.
func GenNode() BaseNode { return BaseNode{Origin: OriginGenerated} }

// GenIdent builds an identifier.
func GenIdent(name string) *JsIdent {
	return &JsIdent{BaseExpr: BaseExpr{BaseNode: GenNode()}, Name: name}
}

// GenStrLit builds a decoded compiler-generated string literal. The printer
// owns JavaScript escaping, so callers always pass the semantic string value.
func GenStrLit(value string) *JsLiteral {
	return &JsLiteral{
		BaseExpr: BaseExpr{BaseNode: GenNode()},
		Kind:     "string",
		Value:    value,
		Decoded:  true,
	}
}

func GenMember(obj JsExpr, prop string) *JsMemberExpr {
	return &JsMemberExpr{BaseExpr: BaseExpr{BaseNode: GenNode()}, Obj: obj, Prop: GenIdent(prop)}
}

func GenCall(callee JsExpr, args ...JsExpr) *JsCallExpr {
	return &JsCallExpr{BaseExpr: BaseExpr{BaseNode: GenNode()}, Callee: callee, Args: args}
}

func GenExprStmt(expr JsExpr) *JsExprStmt {
	return &JsExprStmt{BaseStmt: BaseStmt{BaseNode: GenNode()}, X: expr}
}

func GenAssignStmt(left, right JsExpr) *JsExprStmt {
	return GenExprStmt(&JsAssignExpr{BaseExpr: BaseExpr{BaseNode: GenNode()}, Op: "=", L: left, R: right})
}

func GenVar(kind, name string, init JsExpr) *JsVarDecl {
	return &JsVarDecl{BaseStmt: BaseStmt{BaseNode: GenNode()}, Kind: kind, Decls: []*JsVarDeclarator{{Id: GenIdent(name), Init: init}}}
}

func GenConst(name string, init JsExpr) *JsVarDecl { return GenVar("const", name, init) }

func GenLet(name string, init JsExpr) *JsVarDecl { return GenVar("let", name, init) }

func GenInvoke(name string, args ...JsExpr) *JsExprStmt {
	return GenExprStmt(GenCall(GenIdent(name), args...))
}

func GenBinary(op string, left, right JsExpr) *JsBinaryExpr {
	return &JsBinaryExpr{BaseExpr: BaseExpr{BaseNode: GenNode()}, Op: op, L: left, R: right}
}

func GenIf(test JsExpr, then JsStmt) *JsIfStmt {
	return &JsIfStmt{BaseStmt: BaseStmt{BaseNode: GenNode()}, Test: test, Then: then}
}

func GenNull() *JsLiteral {
	return &JsLiteral{BaseExpr: BaseExpr{BaseNode: GenNode()}, Raw: "null", Kind: "null"}
}

func GenNumLit(value int) *JsLiteral {
	return &JsLiteral{BaseExpr: BaseExpr{BaseNode: GenNode()}, Raw: strconv.Itoa(value), Kind: "number"}
}

func GenBoolLit(value bool) *JsLiteral {
	if value {
		return &JsLiteral{BaseExpr: BaseExpr{BaseNode: GenNode()}, Raw: "true", Kind: "boolean"}
	}
	return &JsLiteral{BaseExpr: BaseExpr{BaseNode: GenNode()}, Raw: "false", Kind: "boolean"}
}

func GenComputedMember(obj, prop JsExpr) *JsMemberExpr {
	return &JsMemberExpr{BaseExpr: BaseExpr{BaseNode: GenNode()}, Obj: obj, Prop: prop, Computed: true}
}

func GenLogical(op string, left, right JsExpr) *JsLogicalExpr {
	return &JsLogicalExpr{BaseExpr: BaseExpr{BaseNode: GenNode()}, Op: op, L: left, R: right}
}

func GenUnary(op string, expr JsExpr) *JsUnaryExpr {
	return &JsUnaryExpr{BaseExpr: BaseExpr{BaseNode: GenNode()}, Op: op, Prefix: true, X: expr}
}

func GenNew(callee JsExpr, args ...JsExpr) *JsNewExpr {
	return &JsNewExpr{BaseExpr: BaseExpr{BaseNode: GenNode()}, Callee: callee, Args: args}
}

func GenArray(elems ...JsExpr) *JsArrayExpr {
	return &JsArrayExpr{BaseExpr: BaseExpr{BaseNode: GenNode()}, Elems: elems}
}

func GenBlock(body ...JsStmt) *JsBlock {
	return &JsBlock{BaseStmt: BaseStmt{BaseNode: GenNode()}, Body: body}
}

func GenReturn(arg JsExpr) *JsReturnStmt {
	return &JsReturnStmt{BaseStmt: BaseStmt{BaseNode: GenNode()}, Arg: arg}
}

func GenForOf(name string, iterable JsExpr, body JsStmt) *JsForInStmt {
	return &JsForInStmt{
		BaseStmt: BaseStmt{BaseNode: GenNode()},
		Left:     GenConst(name, nil),
		Right:    iterable,
		Body:     body,
		IsOf:     true,
	}
}

func GenBool(expr JsExpr) JsExpr {
	return &JsUnaryExpr{
		BaseExpr: BaseExpr{BaseNode: GenNode()}, Op: "!", Prefix: true,
		X: &JsUnaryExpr{BaseExpr: BaseExpr{BaseNode: GenNode()}, Op: "!", Prefix: true, X: expr},
	}
}

func GenString(expr JsExpr) JsExpr { return GenCall(GenIdent("String"), expr) }
