package jsast

// ──────────────────────────────────────────────
//  Node interfaces
// ──────────────────────────────────────────────

// JsNode is the root interface for all AST nodes.
type JsNode interface {
	jsNode()
	NodePos() int
	NodeEnd() int
	origin() NodeOrigin
}

// JsStmt is any statement node.
type JsStmt interface {
	JsNode
	jsStmt()
}

// JsExpr is any expression node.
type JsExpr interface {
	JsNode
	jsExpr()
}

// ──────────────────────────────────────────────
//  Base structs
// ──────────────────────────────────────────────

// NodeOrigin marks where an AST node came from.  In a unified
// module tree — where user <script> code and compiler-generated
// wiring coexist — this lets analysis passes and diagnostics tell
// the two apart: a problem in OriginUser code is reported against
// the developer's source, while a problem in OriginGenerated code
// is a compiler bug.
type NodeOrigin uint8

const (
	OriginUser      NodeOrigin = iota // parsed from a .vela <script> or template expr
	OriginGenerated                   // constructed by the code generator
)

type BaseNode struct {
	Pos, End int
	Origin   NodeOrigin

	// SourceOrigin is an optional absolute byte offset in the original SFC.
	// It is deliberately independent of Pos/End, which describe parser input
	// and are not meaningful for compiler-created nodes.
	SourceOrigin    int
	HasSourceOrigin bool
}

func (b *BaseNode) jsNode()            {}
func (b *BaseNode) NodePos() int       { return b.Pos }
func (b *BaseNode) NodeEnd() int       { return b.End }
func (b *BaseNode) origin() NodeOrigin { return b.Origin }

// shiftPos moves a node's source span by delta bytes.  Used to
// relocate a sub-tree parsed from a detached substring back onto
// the coordinates of the original source (see structureTemplate).
func (b *BaseNode) shiftPos(delta int) {
	b.Pos += delta
	b.End += delta
}

// posShiftable is implemented by every node via the embedded
// *BaseNode — it exposes shiftPos for ShiftTree.
type posShiftable interface {
	shiftPos(int)
}

type BaseStmt struct{ BaseNode }

func (s *BaseStmt) jsNode()      {}
func (s *BaseStmt) jsStmt()      {}
func (s *BaseStmt) NodePos() int { return s.Pos }
func (s *BaseStmt) NodeEnd() int { return s.End }

type BaseExpr struct{ BaseNode }

func (e *BaseExpr) jsNode()      {}
func (e *BaseExpr) jsExpr()      {}
func (e *BaseExpr) NodePos() int { return e.Pos }
func (e *BaseExpr) NodeEnd() int { return e.End }

// ──────────────────────────────────────────────
//  Program
// ──────────────────────────────────────────────

type JsProgram struct {
	BaseNode
	Body []JsStmt
}

// ──────────────────────────────────────────────
//  Statements
// ──────────────────────────────────────────────

// JsImportDecl: import { Foo, Bar as B } from 'mod'
//
//	import * as Ns from 'mod'
//	import Default from 'mod'
//	import Default, * as Ns from 'mod'
//	import data from './d.json' with { type: 'json' }
//	import 'side-effect'
type JsImportDecl struct {
	BaseStmt
	DefaultName string
	Namespace   string // * as X  →  X
	Specifiers  []JsImportSpec
	Source      string // unquoted module path
	Attributes  []JsImportAttr
}

type JsImportSpec struct {
	Imported string // original exported name (string names are unquoted)
	Local    string // local binding
}

// JsImportAttr is one `key: 'value'` pair of an import attributes clause
// (`with { type: 'json' }`, or the legacy `assert { … }` spelling).
type JsImportAttr struct {
	Key   string
	Value string // unquoted
}

// JsExportDecl covers every export form:
//
//	export const a = 1               Declaration = the declaration
//	export default <decl|expr>       Default = true, Declaration = decl, or a
//	                                 JsExprStmt wrapping the expression
//	export { a, b as c }             Specifiers, Declaration == nil
//	export { a } from 'm'            Specifiers, HasSource, Source
//	export * from 'm'                All, HasSource, Source
//	export * as ns from 'm'          Namespace, HasSource, Source
//
// Declaration is nil for the list / re-export forms.
type JsExportDecl struct {
	BaseStmt
	Default     bool
	Declaration JsStmt

	Specifiers []JsExportSpec
	All        bool   // export * from …
	Namespace  string // export * as Namespace from …
	HasSource  bool   // `from '…'` present
	Source     string // unquoted module path
	Attributes []JsImportAttr
}

type JsExportSpec struct {
	Local    string // binding in this module (or the imported name when HasSource)
	Exported string // name visible to importers (string names are unquoted)
}

// JsVarDecl: let / const / var
type JsVarDecl struct {
	BaseStmt
	Kind  string // "let" | "const" | "var"
	Decls []*JsVarDeclarator
}

type JsVarDeclarator struct {
	BaseNode
	Id   JsExpr // Identifier or destructuring pattern
	Init JsExpr // may be nil
}

type JsFuncDecl struct {
	BaseStmt
	Name      string
	Params    []JsExpr
	Body      *JsBlock
	Async     bool
	Generator bool
}

type JsClassDecl struct {
	BaseStmt
	Name       string
	SuperClass JsExpr
	Members    []*JsClassMember
}

// JsClassExpr: a class in expression position (`const A = class B {}`,
// `f(class {})`).  Name is empty for anonymous classes.
type JsClassExpr struct {
	BaseExpr
	Name       string
	SuperClass JsExpr
	Members    []*JsClassMember
}

// JsClassMember is one element of a class body.
//
//	method / get / set   Value is a *JsFuncExpr (Async / Generator set on it)
//	field                Value is the initializer, nil when there is none
//	static-block         `static { … }`: Body is set, Key and Value are nil
//
// Private members (`#x`) use a *JsIdent whose Name includes the leading '#'.
type JsClassMember struct {
	BaseNode
	Key      JsExpr
	Value    JsExpr
	Kind     string // "method" | "get" | "set" | "field" | "static-block"
	Static   bool
	Computed bool
	Body     *JsBlock // static blocks only
}

type JsBlock struct {
	BaseStmt
	Body []JsStmt
}

type JsExprStmt struct {
	BaseStmt
	X JsExpr
}

type JsIfStmt struct {
	BaseStmt
	Test JsExpr
	Then JsStmt
	Else JsStmt // may be nil
}

type JsForStmt struct {
	BaseStmt
	Init   JsStmt // JsVarDecl or JsExprStmt, may be nil
	Test   JsExpr // may be nil
	Update JsExpr // may be nil
	Body   JsStmt
}

type JsForInStmt struct {
	BaseStmt
	Left  JsStmt
	Right JsExpr
	Body  JsStmt
	IsOf  bool
	Await bool // for await (… of …); implies IsOf
}

type JsWhileStmt struct {
	BaseStmt
	Test JsExpr
	Body JsStmt
}

type JsDoWhileStmt struct {
	BaseStmt
	Body JsStmt
	Test JsExpr
}

type JsReturnStmt struct {
	BaseStmt
	Arg JsExpr // may be nil
}

type JsThrowStmt struct {
	BaseStmt
	Arg JsExpr
}

type JsTryStmt struct {
	BaseStmt
	Block      *JsBlock
	CatchParam JsExpr   // may be nil (optional catch binding)
	Handler    *JsBlock // may be nil
	Finalizer  *JsBlock // may be nil
}

type JsSwitchStmt struct {
	BaseStmt
	Disc  JsExpr
	Cases []*JsSwitchCase
}

type JsSwitchCase struct {
	BaseNode
	Test JsExpr // nil → default
	Body []JsStmt
}

type JsBreakStmt struct {
	BaseStmt
	Label string
}

type JsContinueStmt struct {
	BaseStmt
	Label string
}

type JsLabeledStmt struct {
	BaseStmt
	Label string
	Body  JsStmt
}

type JsEmptyStmt struct{ BaseStmt }

type JsDebuggerStmt struct{ BaseStmt }

// ──────────────────────────────────────────────
//  Expressions
// ──────────────────────────────────────────────

// JsIdent: any bare identifier reference.
//
// A few syntactic forms reuse JsIdent instead of introducing node types:
//
//   - private names: Name keeps the leading '#' ("#count"); they appear as the
//     key of class members, as the property of a member expression
//     (`this.#count`) and as the left operand of `#count in obj`.
//   - meta properties: `new.target` and `import.meta` are JsMemberExpr with
//     Obj = JsIdent{"new"|"import"} and Prop = JsIdent{"target"|"meta"}.
//   - dynamic import: `import(x)` is a JsCallExpr whose Callee is
//     JsIdent{"import"}.
//
// Analysis passes must not treat these names as variable references.
type JsIdent struct {
	BaseExpr
	Name string
}

// JsTaggedTemplate: tag`hello ${name}`.  Quasi is the template literal that
// is passed to Tag; member/call expressions may be tags (String.raw`x`).
type JsTaggedTemplate struct {
	BaseExpr
	Tag   JsExpr
	Quasi *JsTemplateLit
}

// JsLiteral: number, string, boolean, null, regex.
type JsLiteral struct {
	BaseExpr
	Raw     string
	Kind    string // "number" | "bigint" | "string" | "boolean" | "null" | "regex" | "unknown" (placeholder for a token the parser could not use; always accompanied by a diagnostic)
	Value   string // decoded generated string value when Decoded is true
	Decoded bool   // printer owns escaping for generated string literals
}

// JsTemplateLit: a template literal, structured into alternating
// literal string chunks (Quasis) and interpolated expressions (Exprs).
//
// For `hello ${name}!`:
//
//	Quasis = ["hello ", "!"]
//	Exprs  = [JsIdent{name}]
//
// Invariant: len(Quasis) == len(Exprs) + 1.  A template with no
// interpolations has one quasi and no exprs.  An empty template “
// has Quasis == [""], Exprs == nil.
//
// Quasi strings are stored WITHOUT escaping resolved — they are the
// cooked-but-still-source-form text between the backticks and the
// ${...} holes.  The printer re-emits them verbatim between
// backticks; round-tripping is therefore exact.
//
// Storing the literal structured (rather than as opaque source
// text) lets analysis passes descend into the interpolations and
// lets codegen construct template literals node-by-node instead of
// by string concatenation.
type JsTemplateLit struct {
	BaseExpr
	Quasis []string
	Exprs  []JsExpr
}

type JsArrayExpr struct {
	BaseExpr
	Elems []JsExpr // nil entry = hole
}

type JsObjectExpr struct {
	BaseExpr
	Props []*JsProp
}

type JsProp struct {
	BaseNode
	Key       JsExpr
	Value     JsExpr
	Computed  bool
	Shorthand bool
	Spread    bool
	// Method is true for object-method shorthand — `{ m() {} }` —
	// as opposed to `{ m: function() {} }`.  Both parse to a
	// JsFuncExpr value; this flag distinguishes them so the printer
	// can round-trip the shorthand form.  Always false for get/set
	// (those are marked via Kind) and for non-function values.
	Method bool
	Kind   string // "init" | "get" | "set"
}

type JsFuncExpr struct {
	BaseExpr
	Name      string
	Params    []JsExpr
	Body      *JsBlock
	Async     bool
	Generator bool
}

// JsArrowFunc: (params) => body  or  param => body.
// Body is either *JsBlock or JsExpr (concise arrow).
type JsArrowFunc struct {
	BaseExpr
	Params []JsExpr
	Body   JsNode // *JsBlock or JsExpr
	Async  bool
}

type JsUnaryExpr struct {
	BaseExpr
	Op     string
	Prefix bool
	X      JsExpr
}

type JsBinaryExpr struct {
	BaseExpr
	Op   string
	L, R JsExpr
}

type JsLogicalExpr struct {
	BaseExpr
	Op   string
	L, R JsExpr
}

type JsAssignExpr struct {
	BaseExpr
	Op   string
	L, R JsExpr
}

type JsMemberExpr struct {
	BaseExpr
	Obj      JsExpr
	Prop     JsExpr
	Computed bool
	Optional bool
}

type JsCallExpr struct {
	BaseExpr
	Callee   JsExpr
	Args     []JsExpr
	Optional bool
}

type JsNewExpr struct {
	BaseExpr
	Callee JsExpr
	Args   []JsExpr
}

type JsConditionalExpr struct {
	BaseExpr
	Test, Then, Else JsExpr
}

type JsSeqExpr struct {
	BaseExpr
	Exprs []JsExpr
}

type JsSpreadExpr struct {
	BaseExpr
	X JsExpr
}

type JsAwaitExpr struct {
	BaseExpr
	X JsExpr
}

type JsYieldExpr struct {
	BaseExpr
	X        JsExpr // may be nil
	Delegate bool
}

type JsThisExpr struct{ BaseExpr }
type JsSuperExpr struct{ BaseExpr }
