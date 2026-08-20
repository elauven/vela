package main

// ──────────────────────────────────────────────
//  Node interfaces
// ──────────────────────────────────────────────

// JsNode is the root interface for all AST nodes.
type JsNode interface {
	jsNode()
	nodePos() int
	nodeEnd() int
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

type baseNode struct {
	Pos, End int
	Origin   NodeOrigin

	// SourceOrigin is an optional absolute byte offset in the original SFC.
	// It is deliberately independent of Pos/End, which describe parser input
	// and are not meaningful for compiler-created nodes.
	SourceOrigin    int
	HasSourceOrigin bool
}

func (b *baseNode) jsNode()            {}
func (b *baseNode) nodePos() int       { return b.Pos }
func (b *baseNode) nodeEnd() int       { return b.End }
func (b *baseNode) origin() NodeOrigin { return b.Origin }

// shiftPos moves a node's source span by delta bytes.  Used to
// relocate a sub-tree parsed from a detached substring back onto
// the coordinates of the original source (see structureTemplate).
func (b *baseNode) shiftPos(delta int) {
	b.Pos += delta
	b.End += delta
}

// posShiftable is implemented by every node via the embedded
// *baseNode — it exposes shiftPos for shiftTree.
type posShiftable interface {
	shiftPos(int)
}

type baseStmt struct{ baseNode }

func (s *baseStmt) jsNode()      {}
func (s *baseStmt) jsStmt()      {}
func (s *baseStmt) nodePos() int { return s.Pos }
func (s *baseStmt) nodeEnd() int { return s.End }

type baseExpr struct{ baseNode }

func (e *baseExpr) jsNode()      {}
func (e *baseExpr) jsExpr()      {}
func (e *baseExpr) nodePos() int { return e.Pos }
func (e *baseExpr) nodeEnd() int { return e.End }

// ──────────────────────────────────────────────
//  Program
// ──────────────────────────────────────────────

type JsProgram struct {
	baseNode
	Body []JsStmt
}

// ──────────────────────────────────────────────
//  Statements
// ──────────────────────────────────────────────

// JsImportDecl: import { Foo, Bar as B } from 'mod'
//
//	import * as Ns from 'mod'
//	import Default from 'mod'
type JsImportDecl struct {
	baseStmt
	DefaultName string
	Namespace   string // * as X  →  X
	Specifiers  []JsImportSpec
	Source      string // unquoted module path
}

type JsImportSpec struct {
	Imported string // original exported name
	Local    string // local binding
}

type JsExportDecl struct {
	baseStmt
	Default     bool
	Declaration JsStmt
}

// JsVarDecl: let / const / var
type JsVarDecl struct {
	baseStmt
	Kind  string // "let" | "const" | "var"
	Decls []*JsVarDeclarator
}

type JsVarDeclarator struct {
	baseNode
	Id   JsExpr // Identifier or destructuring pattern
	Init JsExpr // may be nil
}

type JsFuncDecl struct {
	baseStmt
	Name      string
	Params    []JsExpr
	Body      *JsBlock
	Async     bool
	Generator bool
}

type JsClassDecl struct {
	baseStmt
	Name       string
	SuperClass JsExpr
	Members    []*JsClassMember
}

type JsClassMember struct {
	baseNode
	Key      JsExpr
	Value    JsExpr
	Kind     string // "method" | "get" | "set" | "field"
	Static   bool
	Computed bool
}

type JsBlock struct {
	baseStmt
	Body []JsStmt
}

type JsExprStmt struct {
	baseStmt
	X JsExpr
}

type JsIfStmt struct {
	baseStmt
	Test JsExpr
	Then JsStmt
	Else JsStmt // may be nil
}

type JsForStmt struct {
	baseStmt
	Init   JsStmt // JsVarDecl or JsExprStmt, may be nil
	Test   JsExpr // may be nil
	Update JsExpr // may be nil
	Body   JsStmt
}

type JsForInStmt struct {
	baseStmt
	Left  JsStmt
	Right JsExpr
	Body  JsStmt
	IsOf  bool
}

type JsWhileStmt struct {
	baseStmt
	Test JsExpr
	Body JsStmt
}

type JsDoWhileStmt struct {
	baseStmt
	Body JsStmt
	Test JsExpr
}

type JsReturnStmt struct {
	baseStmt
	Arg JsExpr // may be nil
}

type JsThrowStmt struct {
	baseStmt
	Arg JsExpr
}

type JsTryStmt struct {
	baseStmt
	Block      *JsBlock
	CatchParam JsExpr   // may be nil (optional catch binding)
	Handler    *JsBlock // may be nil
	Finalizer  *JsBlock // may be nil
}

type JsSwitchStmt struct {
	baseStmt
	Disc  JsExpr
	Cases []*JsSwitchCase
}

type JsSwitchCase struct {
	baseNode
	Test JsExpr // nil → default
	Body []JsStmt
}

type JsBreakStmt struct {
	baseStmt
	Label string
}

type JsContinueStmt struct {
	baseStmt
	Label string
}

type JsLabeledStmt struct {
	baseStmt
	Label string
	Body  JsStmt
}

type JsEmptyStmt struct{ baseStmt }

type JsDebuggerStmt struct{ baseStmt }

// ──────────────────────────────────────────────
//  Expressions
// ──────────────────────────────────────────────

// JsIdent: any bare identifier reference.
type JsIdent struct {
	baseExpr
	Name string
}

// JsLiteral: number, string, boolean, null, regex.
type JsLiteral struct {
	baseExpr
	Raw  string
	Kind string // "number" | "string" | "boolean" | "null" | "regex"
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
	baseExpr
	Quasis []string
	Exprs  []JsExpr
}

type JsArrayExpr struct {
	baseExpr
	Elems []JsExpr // nil entry = hole
}

type JsObjectExpr struct {
	baseExpr
	Props []*JsProp
}

type JsProp struct {
	baseNode
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
	baseExpr
	Name      string
	Params    []JsExpr
	Body      *JsBlock
	Async     bool
	Generator bool
}

// JsArrowFunc: (params) => body  or  param => body.
// Body is either *JsBlock or JsExpr (concise arrow).
type JsArrowFunc struct {
	baseExpr
	Params []JsExpr
	Body   JsNode // *JsBlock or JsExpr
	Async  bool
}

type JsUnaryExpr struct {
	baseExpr
	Op     string
	Prefix bool
	X      JsExpr
}

type JsBinaryExpr struct {
	baseExpr
	Op   string
	L, R JsExpr
}

type JsLogicalExpr struct {
	baseExpr
	Op   string
	L, R JsExpr
}

type JsAssignExpr struct {
	baseExpr
	Op   string
	L, R JsExpr
}

type JsMemberExpr struct {
	baseExpr
	Obj      JsExpr
	Prop     JsExpr
	Computed bool
	Optional bool
}

type JsCallExpr struct {
	baseExpr
	Callee   JsExpr
	Args     []JsExpr
	Optional bool
}

type JsNewExpr struct {
	baseExpr
	Callee JsExpr
	Args   []JsExpr
}

type JsConditionalExpr struct {
	baseExpr
	Test, Then, Else JsExpr
}

type JsSeqExpr struct {
	baseExpr
	Exprs []JsExpr
}

type JsSpreadExpr struct {
	baseExpr
	X JsExpr
}

type JsAwaitExpr struct {
	baseExpr
	X JsExpr
}

type JsYieldExpr struct {
	baseExpr
	X        JsExpr // may be nil
	Delegate bool
}

type JsThisExpr struct{ baseExpr }
type JsSuperExpr struct{ baseExpr }
