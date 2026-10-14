package main

// scope.go — lexical scope and binding analysis for the JS AST.
//
// Produces a tree of scopes, resolves every identifier reference to a
// binding (or flags it undeclared), and detects:
//
//   • use-of-undeclared variables           (warning)
//   • write-to-const / write-to-import      (error)
//   • temporal dead zone violations         (error, static — same fn only)
//   • shadowing of an outer binding         (hint, off by default — see below)
//   • duplicate let/const declarations      (error)
//
// Why this exists: diagnostics.go's old validator built a `knownLocals`
// map by scraping a.Imports + a.Functions + a.Props.  That works for the
// top level but misses every binding declared inside a block, function,
// loop, destructuring pattern, or catch clause — so it could only emit
// warnings about the most obvious typos.  A proper scope graph closes
// that gap and is also the substrate any future optimizer (DCE,
// constant folding, etc.) will want anyway.

import "strings"

// ─────────────────────────────────────────────
//  Bindings
// ─────────────────────────────────────────────

// BindingKind classifies how a name was introduced.  Kind drives both
// hoisting behaviour (Var/Function hoist out of blocks; Let/Const/Class
// don't) and TDZ semantics (Let/Const/Class are TDZ-protected).
type BindingKind uint8

const (
	BindVar      BindingKind = iota // var      — hoisted to fn/program, no TDZ
	BindLet                         // let      — block scope, TDZ
	BindConst                       // const    — block scope, TDZ, immutable
	BindFunction                    // function — hoisted, no TDZ
	BindClass                       // class    — block scope, TDZ
	BindParam                       // formal parameter
	BindCatch                       // catch (e) binding
	BindImport                      // import binding — module-scope, immutable
	BindBuiltin                     // pre-declared global (console, $state, …)
)

func (k BindingKind) String() string {
	switch k {
	case BindVar:
		return "var"
	case BindLet:
		return "let"
	case BindConst:
		return "const"
	case BindFunction:
		return "function"
	case BindClass:
		return "class"
	case BindParam:
		return "parameter"
	case BindCatch:
		return "catch binding"
	case BindImport:
		return "import"
	case BindBuiltin:
		return "global"
	}
	return "?"
}

// hasTDZ reports whether the binding can be in temporal dead zone.
func (k BindingKind) hasTDZ() bool {
	return k == BindLet || k == BindConst || k == BindClass
}

// hoists reports whether a declaration of this kind is lifted to the
// nearest enclosing function or program scope.
func (k BindingKind) hoists() bool {
	return k == BindVar || k == BindFunction
}

// Binding is a single named declaration plus the references that
// resolve to it.  One Binding is shared by every JsIdent in its scope
// chain that refers to the same name.
type Binding struct {
	Name   string
	Kind   BindingKind
	Pos    int // declaration position (0 for builtins)
	End    int // declaration end
	Scope  *Scope
	Refs   []*JsIdent // every read reference resolved to this binding
	Writes []*JsIdent // every write reference (subset of Refs)
}

// ─────────────────────────────────────────────
//  Scopes
// ─────────────────────────────────────────────

type ScopeKind uint8

const (
	ScopeProgram    ScopeKind = iota
	ScopeFunction             // any callable's body — closes the hoist chain
	ScopeBlock                // {} — boundary for let/const/class only
	ScopeCatch                // catch (e) { … }
	ScopeForBinding           // for / for-in / for-of header
)

func (k ScopeKind) String() string {
	switch k {
	case ScopeProgram:
		return "program"
	case ScopeFunction:
		return "function"
	case ScopeBlock:
		return "block"
	case ScopeCatch:
		return "catch"
	case ScopeForBinding:
		return "for"
	}
	return "?"
}

// Scope is one node in the lexical scope tree.
type Scope struct {
	Parent   *Scope
	Children []*Scope
	Kind     ScopeKind
	Node     JsNode // owning AST node — nil for the synthetic builtin scope
	Bindings map[string]*Binding
}

func newScope(parent *Scope, kind ScopeKind, node JsNode) *Scope {
	s := &Scope{
		Parent:   parent,
		Kind:     kind,
		Node:     node,
		Bindings: map[string]*Binding{},
	}
	if parent != nil {
		parent.Children = append(parent.Children, s)
	}
	return s
}

// hoistTarget returns the scope where a var or function declaration is
// hoisted to — the nearest enclosing function or program scope.  Block,
// catch, and for scopes are transparent to hoisting.
func (s *Scope) hoistTarget() *Scope {
	for s != nil {
		if s.Kind == ScopeFunction || s.Kind == ScopeProgram {
			return s
		}
		s = s.Parent
	}
	return nil
}

// lookup walks the scope chain searching for `name`.
func (s *Scope) lookup(name string) *Binding {
	for s != nil {
		if b, ok := s.Bindings[name]; ok {
			return b
		}
		s = s.Parent
	}
	return nil
}

// ─────────────────────────────────────────────
//  Built-in globals
//
//  Pre-declared at the root scope so references resolve instead of
//  triggering "undeclared" warnings.  Browser + Node + ES + the Vela
//  reactive primitives.  Extend via BuildScopesWith.
// ─────────────────────────────────────────────

var defaultGlobals = []string{
	// ES intrinsics
	"undefined", "null", "NaN", "Infinity", "globalThis",
	"Object", "Array", "String", "Number", "Boolean", "BigInt", "Symbol",
	"Function", "RegExp", "Date", "Math", "JSON",
	"Map", "Set", "WeakMap", "WeakSet", "Proxy", "Reflect",
	"Promise", "Error", "TypeError", "RangeError", "SyntaxError", "ReferenceError",
	"EvalError", "URIError", "AggregateError",
	"parseInt", "parseFloat", "isNaN", "isFinite", "encodeURI", "decodeURI",
	"encodeURIComponent", "decodeURIComponent", "eval",
	"Int8Array", "Uint8Array", "Uint8ClampedArray", "Int16Array", "Uint16Array",
	"Int32Array", "Uint32Array", "Float32Array", "Float64Array",
	"BigInt64Array", "BigUint64Array", "ArrayBuffer", "DataView",

	// Browser/DOM essentials
	"window", "document", "navigator", "location", "history", "screen",
	"console", "localStorage", "sessionStorage", "indexedDB",
	"setTimeout", "setInterval", "clearTimeout", "clearInterval",
	"requestAnimationFrame", "cancelAnimationFrame", "queueMicrotask",
	"fetch", "URL", "URLSearchParams", "FormData", "Headers",
	"Request", "Response", "Blob", "File", "FileReader",
	"Event", "EventTarget", "CustomEvent", "MouseEvent", "KeyboardEvent",
	"HTMLElement", "Element", "Node", "Text", "DocumentFragment",
	"alert", "confirm", "prompt",
	"XMLHttpRequest", "WebSocket", "EventSource", "AbortController", "AbortSignal",
	"BroadcastChannel", "MessageChannel", "Worker", "structuredClone", "crypto",
	"performance", "atob", "btoa", "TextEncoder", "TextDecoder", "Intl",
	"IntersectionObserver", "ResizeObserver", "MutationObserver", "matchMedia",
	"getComputedStyle", "DOMParser", "Image", "HTMLInputElement", "InputEvent",
	"DragEvent", "PointerEvent", "FocusEvent", "SubmitEvent",

	// Node (so SSR / build scripts don't trip)
	"process", "Buffer", "require", "module", "exports",
	"__dirname", "__filename", "global",

	// Conventional in-function specials.  arguments isn't actually
	// bound in arrow functions but warning users about that needs
	// per-function context — punt for now.
	"arguments", "this",

	// Vela compiler primitives.  These appear in user .vela scripts as
	// $state(0), $derived(...), $effect(...), $props(), $bindable().
	"$state", "$derived", "$effect", "$props", "$bindable", "$inspect",

	// $$dispatch is wired into every component's scope by the code
	// generator — a (name, detail) => void that fires a bubbling
	// CustomEvent at the component's mount target.  User scripts
	// call it directly, so it must resolve like a builtin.
	"$$dispatch",
}

// ─────────────────────────────────────────────
//  Builder
// ─────────────────────────────────────────────

type scopeBuilder struct {
	ds                      *DiagSet
	cur                     *Scope // top of the scope stack
	root                    *Scope // program scope (for hoisting / globals)
	target                  TargetProfile
	excludedGlobals         map[string]bool
	unavailableCapabilities map[string]bool
	strict                  bool
}

// ScopeOptions controls proof-oriented global diagnostics. Zero values retain
// the historical permissive environment.
type ScopeOptions struct {
	ExtraGlobals            []string
	TargetProfile           TargetProfile
	UnavailableCapabilities []string
	Strict                  bool
}

// BuildScopes runs scope/binding analysis over a parsed program and
// emits diagnostics via ds.  Returns the program scope so callers can
// inspect bindings (e.g. for an unused-var pass).
//
// Safe to call with prog == nil — returns nil.
func BuildScopes(prog *JsProgram, ds *DiagSet) *Scope {
	return BuildScopesWithOptions(prog, ds, ScopeOptions{})
}

// BuildScopesWith is like BuildScopes but lets the caller supply extra
// pre-declared globals (e.g. injected by a host environment).  These
// merge with defaultGlobals.
func BuildScopesWith(prog *JsProgram, ds *DiagSet, extraGlobals []string) *Scope {
	return BuildScopesWithOptions(prog, ds, ScopeOptions{ExtraGlobals: extraGlobals})
}

// BuildScopesWithOptions extends BuildScopesWith with an explicit host target
// and unavailable direct capabilities. All known globals stay declared so the
// permissive default remains compatible; resolveRef reports selected absences
// only when it can prove the reference is that builtin.
func BuildScopesWithOptions(prog *JsProgram, ds *DiagSet, opts ScopeOptions) *Scope {
	if prog == nil {
		return nil
	}

	// Synthetic global scope holds builtins.  The real program scope
	// sits beneath it so user code can shadow globals freely without
	// triggering a duplicate-declaration error.
	globalScope := newScope(nil, ScopeProgram, nil)
	for _, name := range defaultGlobals {
		globalScope.Bindings[name] = &Binding{
			Name: name, Kind: BindBuiltin, Scope: globalScope,
		}
	}
	for _, name := range opts.ExtraGlobals {
		if _, dup := globalScope.Bindings[name]; !dup {
			globalScope.Bindings[name] = &Binding{
				Name: name, Kind: BindBuiltin, Scope: globalScope,
			}
		}
	}

	root := newScope(globalScope, ScopeProgram, prog)

	unavailable := map[string]bool{}
	for _, name := range opts.UnavailableCapabilities {
		if name != "" {
			unavailable[name] = true
		}
	}
	b := &scopeBuilder{
		ds: ds, cur: root, root: root,
		target:                  opts.TargetProfile.normalized(),
		excludedGlobals:         targetExcludedGlobals(opts.TargetProfile),
		unavailableCapabilities: unavailable,
		strict:                  opts.Strict,
	}

	// Pass 1 (hoisting): pre-declare every var and function statement
	// reachable through transparent scopes (block, if, try, switch …)
	// so that references appearing before the source-order decl still
	// resolve.  This mirrors JS engine behaviour.
	b.hoist(prog.Body)
	b.preDeclareLexical(prog.Body)

	// Pass 2 (walk): visit statements in source order, creating
	// non-hoisted bindings as we go and resolving every reference.
	for _, stmt := range prog.Body {
		b.walkStmt(stmt)
	}

	return root
}

// preDeclareLexical scans a single statement list (the body of one
// scope) for let/const/class declarations and pre-creates their
// bindings *in the current scope*.  Unlike hoist (which crosses
// transparent statement forms and climbs to the function/program
// scope), this stays local: a `let` inside a nested block belongs to
// that block, not to the outer one, so we don't recurse into nested
// blocks.  We DO peek into label statements since labels are
// transparent to scoping.
//
// Why this exists: a reference like
//
//	function f() { console.log(x); let x = 1; }
//
// must report TDZ, not "undefined".  TDZ detection in resolveRef
// works by comparing the reference's source position to the
// binding's; that comparison is meaningless if the binding hasn't
// been created yet when the reference is walked.  So we pre-create
// every lexical binding at scope entry.  resolveRef then sees the
// binding, notices id.Pos < bind.Pos, and emits TDZ.
//
// Duplicate-declaration detection is still done here — emitting an
// error during pre-decl is cleaner than waiting for the walk pass to
// hit the same name again.
func (b *scopeBuilder) preDeclareLexical(stmts []JsStmt) {
	for _, s := range stmts {
		b.preDeclareLexicalStmt(s)
	}
}

func (b *scopeBuilder) preDeclareLexicalStmt(s JsStmt) {
	switch n := s.(type) {
	case *JsVarDecl:
		if n.Kind == "var" {
			return // var is function-scoped, handled by hoist
		}
		kind := BindLet
		if n.Kind == "const" {
			kind = BindConst
		}
		for _, d := range n.Decls {
			eachPatternIdent(d.Id, func(id *JsIdent) {
				b.predeclare(id.Name, kind, id.Pos, id.End)
			})
		}
	case *JsClassDecl:
		if n.Name != "" {
			b.predeclare(n.Name, BindClass, n.Pos, n.End)
		}
	case *JsLabeledStmt:
		// Labels are transparent — a labeled `let x = 1` still
		// introduces x into the enclosing scope.
		b.preDeclareLexicalStmt(n.Body)
	case *JsExportDecl:
		// `export let x = …` / `export class X …` introduces x
		// into module scope exactly as the bare declaration would
		// — unwrap so the inner let/const/class is pre-declared.
		// Without this an `export let` prop referenced by a
		// closure earlier in source order (e.g. inside an
		// $effect) resolves to "not defined".
		if n.Declaration != nil {
			b.preDeclareLexicalStmt(n.Declaration)
		}
	}
	// Crucially: no recursion into blocks, if/else, loops, try, etc.
	// Each of those creates its own scope and will pre-declare its
	// own lexical names when entered in pass 2.
}

// predeclare adds a lexical binding directly to b.cur, emitting a
// redeclaration error if the name is already present.  Mirrors the
// declare() logic but bypasses the hoist-target lookup.
func (b *scopeBuilder) predeclare(name string, kind BindingKind, pos, end int) {
	if name == "" {
		return
	}
	if existing, ok := b.cur.Bindings[name]; ok {
		// Anything involving let/const/class is a SyntaxError at runtime.
		b.ds.ErrorWith(pos, "VELA-SCOPE-DUPLICATE", "Rename or remove one of the declarations.",
			"redeclaration of %q (previously declared as %s)",
			name, existing.Kind)
		return
	}
	b.cur.Bindings[name] = &Binding{
		Name: name, Kind: kind, Pos: pos, End: end, Scope: b.cur,
	}
}

// ─────────────────────────────────────────────
//  Pass 1 — hoisting
// ─────────────────────────────────────────────

// hoist scans a list of statements (within whatever scope is current)
// for var declarations and function declarations and pre-creates
// bindings for them in the current scope's hoist target.  It descends
// through every statement form that is transparent to hoisting (blocks,
// if/else, loops, switch cases, try/catch/finally, labels) but stops at
// function boundaries — those have their own hoisting handled when we
// recurse into them in pass 2.
func (b *scopeBuilder) hoist(stmts []JsStmt) {
	for _, s := range stmts {
		b.hoistStmt(s)
	}
}

func (b *scopeBuilder) hoistStmt(s JsStmt) {
	if s == nil {
		return
	}
	switch n := s.(type) {
	case *JsVarDecl:
		if n.Kind != "var" {
			return // let/const/class are not hoisted
		}
		target := b.cur.hoistTarget()
		for _, d := range n.Decls {
			eachPatternIdent(d.Id, func(id *JsIdent) {
				if _, exists := target.Bindings[id.Name]; exists {
					return // legal var-on-var redeclaration
				}
				bind := &Binding{
					Name: id.Name, Kind: BindVar, Pos: id.Pos, End: id.End,
					Scope: target,
				}
				target.Bindings[id.Name] = bind
			})
		}
	case *JsFuncDecl:
		if n.Name == "" {
			return
		}
		target := b.cur.hoistTarget()
		if _, exists := target.Bindings[n.Name]; !exists {
			target.Bindings[n.Name] = &Binding{
				Name: n.Name, Kind: BindFunction, Pos: n.Pos, End: n.End,
				Scope: target,
			}
		}
	case *JsBlock:
		b.hoist(n.Body)
	case *JsIfStmt:
		b.hoistStmt(n.Then)
		b.hoistStmt(n.Else)
	case *JsForStmt:
		b.hoistStmt(n.Init)
		b.hoistStmt(n.Body)
	case *JsForInStmt:
		b.hoistStmt(n.Left)
		b.hoistStmt(n.Body)
	case *JsWhileStmt:
		b.hoistStmt(n.Body)
	case *JsDoWhileStmt:
		b.hoistStmt(n.Body)
	case *JsTryStmt:
		if n.Block != nil {
			b.hoist(n.Block.Body)
		}
		if n.Handler != nil {
			b.hoist(n.Handler.Body)
		}
		if n.Finalizer != nil {
			b.hoist(n.Finalizer.Body)
		}
	case *JsSwitchStmt:
		for _, c := range n.Cases {
			b.hoist(c.Body)
		}
	case *JsLabeledStmt:
		b.hoistStmt(n.Body)
	}
	// Function/class declarations inside expressions don't count for
	// hoisting in any modern (strict / module) interpretation — leave
	// them to pass 2.
}

// ─────────────────────────────────────────────
//  Pass 2 — walk
// ─────────────────────────────────────────────

func (b *scopeBuilder) enterScope(kind ScopeKind, node JsNode) {
	b.cur = newScope(b.cur, kind, node)
}

func (b *scopeBuilder) leaveScope() {
	if b.cur.Parent != nil {
		b.cur = b.cur.Parent
	}
}

// declare adds a binding for a non-hoisted declaration to the current
// scope.  Catches duplicate let/const, warns on shadow (commented out
// by default — see note in resolveRef).
func (b *scopeBuilder) declare(name string, kind BindingKind, pos, end int) *Binding {
	if name == "" {
		return nil
	}
	target := b.cur
	if kind.hoists() {
		target = target.hoistTarget()
	}

	if existing, ok := target.Bindings[name]; ok {
		// var-on-var is allowed.  Anything involving let/const/class is
		// a SyntaxError at runtime, so we emit Error severity.
		if kind == BindVar && existing.Kind == BindVar {
			return existing
		}
		if kind == BindVar && existing.Kind == BindFunction {
			return existing // legal
		}
		b.ds.ErrorWith(pos, "VELA-SCOPE-DUPLICATE", "Rename or remove one of the declarations.",
			"redeclaration of %q (previously declared as %s)",
			name, existing.Kind)
		return existing
	}

	bind := &Binding{
		Name: name, Kind: kind, Pos: pos, End: end, Scope: target,
	}
	target.Bindings[name] = bind

	// Shadowing hint.  Off-by-default for now — emitting on every
	// shadow tends to be noisy (loop indexes, fn params reusing common
	// names, etc.).  Wire in via a flag when you want it; the logic is:
	//
	//   if outer := target.Parent.lookup(name); outer != nil &&
	//       outer.Kind != BindBuiltin {
	//       b.ds.Hint(pos, "%q shadows %s declared at offset %d",
	//           name, outer.Kind, outer.Pos)
	//   }
	return bind
}

func (b *scopeBuilder) walkStmt(stmt JsStmt) {
	if stmt == nil {
		return
	}
	switch s := stmt.(type) {

	case *JsBlock:
		b.enterScope(ScopeBlock, s)
		b.preDeclareLexical(s.Body)
		for _, x := range s.Body {
			b.walkStmt(x)
		}
		b.leaveScope()

	case *JsVarDecl:
		for _, d := range s.Decls {
			// Walk the pattern's default-value expressions and computed
			// keys as references.  For let/const the binding itself was
			// pre-declared at scope entry, so we only need the read-side
			// of the pattern here; for var we still need to declare
			// (declare() is idempotent against the hoist pass).
			if s.Kind == "var" {
				if d.Init != nil {
					b.walkExpr(d.Init)
				}
				b.declarePattern(d.Id, BindVar)
				continue
			}
			b.walkPatternRHS(d.Id)
			if d.Init != nil {
				b.walkExpr(d.Init)
			}
		}

	case *JsFuncDecl:
		// Already hoisted at the enclosing function/program scope.
		// Recurse into params + body in a fresh function scope.
		b.enterScope(ScopeFunction, s)
		b.declareParams(s.Params)
		if s.Body != nil {
			// Hoist var/function decls and pre-declare lexical bindings.
			b.hoist(s.Body.Body)
			b.preDeclareLexical(s.Body.Body)
			for _, x := range s.Body.Body {
				b.walkStmt(x)
			}
		}
		b.leaveScope()

	case *JsClassDecl:
		// Already pre-declared at the enclosing scope.
		if s.SuperClass != nil {
			b.walkExpr(s.SuperClass)
		}
		b.walkClassMembers(s.Members)

	case *JsImportDecl:
		if s.DefaultName != "" {
			b.declare(s.DefaultName, BindImport, s.Pos, s.End)
		}
		if s.Namespace != "" {
			b.declare(s.Namespace, BindImport, s.Pos, s.End)
		}
		// JsImportSpec is by-value with no Pos of its own; attribute
		// to the parent decl's span.
		for _, sp := range s.Specifiers {
			b.declare(sp.Local, BindImport, s.Pos, s.End)
		}

	case *JsExportDecl:
		if s.Declaration != nil {
			b.walkStmt(s.Declaration)
		}

	case *JsExprStmt:
		b.walkExpr(s.X)

	case *JsIfStmt:
		b.walkExpr(s.Test)
		b.walkStmt(s.Then)
		if s.Else != nil {
			b.walkStmt(s.Else)
		}

	case *JsForStmt:
		// Per-iteration scope: `for (let i …)` is a fresh `i` each iter.
		// We collapse that to one ScopeForBinding for analysis purposes.
		b.enterScope(ScopeForBinding, s)
		if s.Init != nil {
			b.preDeclareLexicalStmt(s.Init)
			b.walkStmt(s.Init)
		}
		if s.Test != nil {
			b.walkExpr(s.Test)
		}
		if s.Update != nil {
			b.walkExpr(s.Update)
		}
		b.walkStmt(s.Body)
		b.leaveScope()

	case *JsForInStmt:
		b.enterScope(ScopeForBinding, s)
		if s.Left != nil {
			b.preDeclareLexicalStmt(s.Left)
			b.walkStmt(s.Left)
		}
		b.walkExpr(s.Right)
		b.walkStmt(s.Body)
		b.leaveScope()

	case *JsWhileStmt:
		b.walkExpr(s.Test)
		b.walkStmt(s.Body)

	case *JsDoWhileStmt:
		b.walkStmt(s.Body)
		b.walkExpr(s.Test)

	case *JsReturnStmt:
		if s.Arg != nil {
			b.walkExpr(s.Arg)
		}

	case *JsThrowStmt:
		b.walkExpr(s.Arg)

	case *JsTryStmt:
		if s.Block != nil {
			b.walkStmt(s.Block)
		}
		if s.Handler != nil {
			b.enterScope(ScopeCatch, s)
			if s.CatchParam != nil {
				b.declarePattern(s.CatchParam, BindCatch)
			}
			b.preDeclareLexical(s.Handler.Body)
			for _, x := range s.Handler.Body {
				b.walkStmt(x)
			}
			b.leaveScope()
		}
		if s.Finalizer != nil {
			b.walkStmt(s.Finalizer)
		}

	case *JsSwitchStmt:
		b.walkExpr(s.Disc)
		b.enterScope(ScopeBlock, s)
		// All case bodies share a single block scope.  Pre-declare
		// lexical bindings from every case before walking any of them
		// so a `let` in case A is in TDZ for forward references from
		// case B (which fall-through can reach).
		for _, c := range s.Cases {
			b.preDeclareLexical(c.Body)
		}
		for _, c := range s.Cases {
			if c.Test != nil {
				b.walkExpr(c.Test)
			}
			for _, x := range c.Body {
				b.walkStmt(x)
			}
		}
		b.leaveScope()

	case *JsLabeledStmt:
		b.walkStmt(s.Body)
	}
}

// declarePattern walks a binding pattern (identifier, array, object, or
// nested) and creates a binding for each name.  Patterns can carry
// default-value expressions and computed keys; those evaluate as
// references and are walked accordingly.
func (b *scopeBuilder) declarePattern(node JsExpr, kind BindingKind) {
	if node == nil {
		return
	}
	switch p := node.(type) {

	case *JsIdent:
		b.declare(p.Name, kind, p.Pos, p.End)

	case *JsArrayExpr:
		for _, elem := range p.Elems {
			if elem == nil {
				continue // hole
			}
			b.declarePatternElem(elem, kind)
		}

	case *JsObjectExpr:
		for _, prop := range p.Props {
			if prop.Spread {
				// { ...rest }
				b.declarePatternElem(prop.Value, kind)
				continue
			}
			if prop.Computed && prop.Key != nil {
				b.walkExpr(prop.Key)
			}
			// Shorthand: { a } / { a = 1 }  — Value mirrors Key.
			// Long form: { a: b } / { a: b = 1 }.
			target := prop.Value
			if target == nil {
				target = prop.Key
			}
			b.declarePatternElem(target, kind)
		}

	case *JsAssignExpr:
		// Top-level default in parameter context: `function f(a = 1)`.
		// Pattern context lands here too via array/object dispatch.
		if p.R != nil {
			b.walkExpr(p.R)
		}
		b.declarePattern(p.L, kind)

	case *JsSpreadExpr:
		b.declarePattern(p.X, kind)
	}
}

// declarePatternElem unwraps default-value and rest wrappers around a
// pattern element before delegating to declarePattern.
func (b *scopeBuilder) declarePatternElem(node JsExpr, kind BindingKind) {
	target := node
	if a, ok := target.(*JsAssignExpr); ok {
		if a.R != nil {
			b.walkExpr(a.R)
		}
		target = a.L
	}
	if s, ok := target.(*JsSpreadExpr); ok {
		target = s.X
	}
	b.declarePattern(target, kind)
}

func (b *scopeBuilder) declareParams(params []JsExpr) {
	for _, p := range params {
		b.declarePattern(p, BindParam)
	}
}

// walkPatternRHS walks the *read-side* of a binding pattern: any
// default-value expressions (the `1` in `let { a = 1 } = obj`), any
// computed keys (`let { [k]: v } = obj`).  Binding identifiers
// themselves are NOT touched — for let/const they've been pre-declared
// at scope entry, and re-declaring would emit spurious redeclaration
// errors.
//
// Mirrors declarePattern's structure but with declares replaced by
// no-ops on the identifier leaves.
func (b *scopeBuilder) walkPatternRHS(node JsExpr) {
	if node == nil {
		return
	}
	switch p := node.(type) {
	case *JsIdent:
		// Binding leaf — already pre-declared, nothing to read.
	case *JsArrayExpr:
		for _, el := range p.Elems {
			if el == nil {
				continue
			}
			b.walkPatternRHSElem(el)
		}
	case *JsObjectExpr:
		for _, prop := range p.Props {
			if prop.Spread {
				b.walkPatternRHSElem(prop.Value)
				continue
			}
			if prop.Computed && prop.Key != nil {
				b.walkExpr(prop.Key)
			}
			target := prop.Value
			if target == nil {
				target = prop.Key
			}
			b.walkPatternRHSElem(target)
		}
	case *JsAssignExpr:
		if p.R != nil {
			b.walkExpr(p.R)
		}
		b.walkPatternRHS(p.L)
	case *JsSpreadExpr:
		b.walkPatternRHS(p.X)
	}
}

func (b *scopeBuilder) walkPatternRHSElem(node JsExpr) {
	target := node
	if a, ok := target.(*JsAssignExpr); ok {
		if a.R != nil {
			b.walkExpr(a.R)
		}
		target = a.L
	}
	if s, ok := target.(*JsSpreadExpr); ok {
		target = s.X
	}
	b.walkPatternRHS(target)
}

// ─────────────────────────────────────────────
//  Pass 2 — expression walk + reference resolution
// ─────────────────────────────────────────────

func (b *scopeBuilder) walkExpr(node JsExpr) {
	if node == nil {
		return
	}
	switch e := node.(type) {

	case *JsIdent:
		// A private name (`#x in o`) is not a variable reference.
		if !strings.HasPrefix(e.Name, "#") {
			b.resolveRef(e, false)
		}

	case *JsLiteral, *JsThisExpr, *JsSuperExpr:
		// no references

	case *JsTemplateLit:
		// Interpolation expressions can reference variables.
		for _, x := range e.Exprs {
			b.walkExpr(x)
		}

	case *JsArrayExpr:
		for _, el := range e.Elems {
			b.walkExpr(el)
		}

	case *JsObjectExpr:
		for _, p := range e.Props {
			if p.Spread {
				b.walkExpr(p.Value)
				continue
			}
			if p.Computed && p.Key != nil {
				b.walkExpr(p.Key)
			}
			if p.Shorthand {
				// { a } — the value is the reference; the key is a
				// separate name node.
				if id, ok := p.Value.(*JsIdent); ok {
					b.resolveRef(id, false)
					continue
				}
			}
			b.walkExpr(p.Value)
		}

	case *JsClassExpr:
		if e.SuperClass != nil {
			b.walkExpr(e.SuperClass)
		}
		// The name of a class expression is visible inside its body only.
		b.enterScope(ScopeBlock, e)
		if e.Name != "" {
			b.declare(e.Name, BindClass, e.Pos, e.End)
		}
		b.walkClassMembers(e.Members)
		b.leaveScope()

	case *JsTaggedTemplate:
		b.walkExpr(e.Tag)
		b.walkExpr(e.Quasi)

	case *JsFuncExpr:
		b.enterScope(ScopeFunction, e)
		// Named function expression: the name is visible *inside* the
		// body only.  Bind it in this scope, not the enclosing one.
		if e.Name != "" {
			b.declare(e.Name, BindConst, e.Pos, e.End)
		}
		b.declareParams(e.Params)
		if e.Body != nil {
			b.hoist(e.Body.Body)
			b.preDeclareLexical(e.Body.Body)
			for _, x := range e.Body.Body {
				b.walkStmt(x)
			}
		}
		b.leaveScope()

	case *JsArrowFunc:
		b.enterScope(ScopeFunction, e)
		b.declareParams(e.Params)
		switch body := e.Body.(type) {
		case *JsBlock:
			if body != nil {
				b.hoist(body.Body)
				b.preDeclareLexical(body.Body)
				for _, x := range body.Body {
					b.walkStmt(x)
				}
			}
		case JsExpr:
			b.walkExpr(body)
		}
		b.leaveScope()

	case *JsUnaryExpr:
		// ++x / --x / x++ / x-- on a bare Ident is a write *and* read.
		if (e.Op == "++" || e.Op == "--") && e.X != nil {
			if id, ok := e.X.(*JsIdent); ok {
				b.resolveRef(id, true)
				return
			}
		}
		b.walkExpr(e.X)

	case *JsBinaryExpr:
		b.walkExpr(e.L)
		b.walkExpr(e.R)

	case *JsLogicalExpr:
		b.walkExpr(e.L)
		b.walkExpr(e.R)

	case *JsAssignExpr:
		// LHS: identifier → write reference.  Member expression →
		// the *object* is read, but the property name itself isn't a
		// variable reference.  Pattern (destructuring assignment) →
		// each ident is a write.
		switch lhs := e.L.(type) {
		case *JsIdent:
			b.resolveRef(lhs, true)
		case *JsMemberExpr:
			b.walkExpr(lhs)
		case *JsArrayExpr, *JsObjectExpr:
			b.walkAssignPattern(lhs)
		default:
			b.walkExpr(lhs)
		}
		b.walkExpr(e.R)

	case *JsMemberExpr:
		// `new.target` and `import.meta` are meta properties.
		if !isMetaProperty(e) {
			b.walkExpr(e.Obj)
		}
		if e.Computed {
			b.walkExpr(e.Prop)
		}
		// Non-computed: e.Prop is a JsIdent representing the property
		// *name*, not a variable reference.  Do not resolveRef it.

	case *JsCallExpr:
		// Dynamic `import(…)` is syntax, not a call of a variable.
		if callee, ok := e.Callee.(*JsIdent); !ok || callee.Name != "import" {
			b.walkExpr(e.Callee)
		}
		for _, a := range e.Args {
			b.walkExpr(a)
		}

	case *JsNewExpr:
		b.walkExpr(e.Callee)
		for _, a := range e.Args {
			b.walkExpr(a)
		}

	case *JsConditionalExpr:
		b.walkExpr(e.Test)
		b.walkExpr(e.Then)
		b.walkExpr(e.Else)

	case *JsSeqExpr:
		for _, x := range e.Exprs {
			b.walkExpr(x)
		}

	case *JsSpreadExpr:
		b.walkExpr(e.X)

	case *JsAwaitExpr:
		b.walkExpr(e.X)

	case *JsYieldExpr:
		if e.X != nil {
			b.walkExpr(e.X)
		}
	}
}

// walkClassMembers walks a class body. Each method has its own function
// scope; field initializers and static blocks run in implicit functions too.
func (b *scopeBuilder) walkClassMembers(members []*JsClassMember) {
	for _, m := range members {
		if m.Computed && m.Key != nil {
			b.walkExpr(m.Key)
		}
		if m.Value != nil {
			b.walkExpr(m.Value)
		}
		if m.Body != nil {
			b.enterScope(ScopeFunction, m.Body)
			b.hoist(m.Body.Body)
			b.preDeclareLexical(m.Body.Body)
			for _, x := range m.Body.Body {
				b.walkStmt(x)
			}
			b.leaveScope()
		}
	}
}

// isMetaProperty reports `new.target` and `import.meta`.
func isMetaProperty(e *JsMemberExpr) bool {
	obj, ok := e.Obj.(*JsIdent)
	if !ok || e.Computed {
		return false
	}
	prop, ok := e.Prop.(*JsIdent)
	return ok && (obj.Name == "new" && prop.Name == "target" || obj.Name == "import" && prop.Name == "meta")
}

// walkAssignPattern handles destructuring assignment LHSes like
// `[a, b] = arr` or `({x, y} = obj)` — each named element is a write
// reference, not a declaration.
func (b *scopeBuilder) walkAssignPattern(node JsExpr) {
	if node == nil {
		return
	}
	switch p := node.(type) {
	case *JsIdent:
		b.resolveRef(p, true)
	case *JsArrayExpr:
		for _, el := range p.Elems {
			b.walkAssignPatternElem(el)
		}
	case *JsObjectExpr:
		for _, prop := range p.Props {
			if prop.Spread {
				b.walkAssignPattern(prop.Value)
				continue
			}
			if prop.Computed && prop.Key != nil {
				b.walkExpr(prop.Key)
			}
			target := prop.Value
			if target == nil {
				target = prop.Key
			}
			b.walkAssignPatternElem(target)
		}
	case *JsMemberExpr:
		// `[obj.x] = arr` — obj is read, property is name.
		b.walkExpr(p)
	default:
		b.walkExpr(p)
	}
}

func (b *scopeBuilder) walkAssignPatternElem(node JsExpr) {
	if node == nil {
		return
	}
	target := node
	if a, ok := target.(*JsAssignExpr); ok {
		if a.R != nil {
			b.walkExpr(a.R)
		}
		target = a.L
	}
	if s, ok := target.(*JsSpreadExpr); ok {
		target = s.X
	}
	b.walkAssignPattern(target)
}

// ─────────────────────────────────────────────
//  Reference resolution + diagnostics
// ─────────────────────────────────────────────

// resolveRef looks up an identifier reference and emits the appropriate
// diagnostic if anything's wrong.  isWrite is true for assignment
// targets and update expressions; that path also flags writes to
// const/import bindings.
func (b *scopeBuilder) resolveRef(id *JsIdent, isWrite bool) {
	if id == nil || id.Name == "" {
		return
	}
	// Ignore the rare names users pull out of patterns that aren't
	// really references (the parser shouldn't produce them, but be
	// defensive).
	if id.Name == "undefined" {
		return
	}

	bind := b.cur.lookup(id.Name)
	if bind == nil {
		if isWrite {
			b.ds.WarnWith(id.Pos, "VELA-SCOPE-IMPLICIT-GLOBAL", "Declare the binding with let, const, or var.",
				"assignment to undeclared variable %q (this creates an implicit global; use let/const)",
				id.Name)
		} else if b.strict {
			b.ds.ErrorWith(id.Pos, "VELA-SCOPE-UNDECLARED", "Declare the name or import it before use.", "%q is not defined", id.Name)
		} else {
			b.ds.WarnWith(id.Pos, "VELA-SCOPE-UNDECLARED", "Declare the name or import it before use.", "%q is not defined", id.Name)
		}
		return
	}

	bind.Refs = append(bind.Refs, id)
	if isWrite {
		bind.Writes = append(bind.Writes, id)
	}
	if bind.Kind == BindBuiltin {
		if b.excludedGlobals[id.Name] {
			b.ds.ErrorWith(id.Pos, "VELA-PROFILE-GLOBAL", "Use a target-compatible API or guard this code behind a host boundary.", "%q is not available in the %s target profile", id.Name, b.target)
			return
		}
		if b.unavailableCapabilities[id.Name] {
			b.ds.ErrorWith(id.Pos, "VELA-CAPABILITY-UNAVAILABLE", "Enable the capability in the host or use a supported alternative.", "%q is configured as unavailable for this compilation", id.Name)
			return
		}
	}

	// Write checks.
	if isWrite {
		switch bind.Kind {
		case BindConst:
			b.ds.ErrorWith(id.Pos, "VELA-SCOPE-IMMUTABLE-WRITE", "Assign to a mutable binding instead.", "cannot assign to const %q", id.Name)
		case BindImport:
			b.ds.ErrorWith(id.Pos, "VELA-SCOPE-IMMUTABLE-WRITE", "Assign to a local mutable binding instead.", "cannot assign to import binding %q", id.Name)
		}
	}

	// TDZ.  Static rule: a reference is definitely in TDZ iff the
	// binding is TDZ-protected, the reference precedes the binding in
	// source order, and we can reach the binding's scope from the
	// current scope without crossing a function boundary.  Crossing a
	// function means the reference might run after the decl at
	// runtime — too speculative to flag.
	if bind.Kind.hasTDZ() && id.Pos < bind.Pos && b.noFunctionBetween(bind.Scope) {
		if isWrite {
			b.ds.ErrorWith(id.Pos, "VELA-SCOPE-TDZ", "Move the access after the declaration.",
				"cannot assign to %q before initialization (temporal dead zone)", id.Name)
		} else {
			b.ds.ErrorWith(id.Pos, "VELA-SCOPE-TDZ", "Move the access after the declaration.",
				"cannot access %q before initialization (temporal dead zone)", id.Name)
		}
	}
}

// noFunctionBetween reports whether target is reachable from b.cur by
// walking parents through only block/catch/for scopes — i.e. the
// reference and the binding live in the same function context.
func (b *scopeBuilder) noFunctionBetween(target *Scope) bool {
	s := b.cur
	for s != nil {
		if s == target {
			return true
		}
		if s.Kind == ScopeFunction {
			return false
		}
		s = s.Parent
	}
	return false
}

// ─────────────────────────────────────────────
//  Pattern helpers
// ─────────────────────────────────────────────

// eachPatternIdent calls fn for every bare-identifier name found in a
// binding pattern.  Used by hoisting to extract var names without
// running the full declarePattern (which would resolve defaults as
// references — wrong during the hoist pre-pass).
func eachPatternIdent(node JsExpr, fn func(*JsIdent)) {
	if node == nil {
		return
	}
	switch p := node.(type) {
	case *JsIdent:
		fn(p)
	case *JsArrayExpr:
		for _, el := range p.Elems {
			eachPatternIdent(unwrapPatternElem(el), fn)
		}
	case *JsObjectExpr:
		for _, prop := range p.Props {
			if prop.Spread {
				eachPatternIdent(unwrapPatternElem(prop.Value), fn)
				continue
			}
			target := prop.Value
			if target == nil {
				target = prop.Key
			}
			eachPatternIdent(unwrapPatternElem(target), fn)
		}
	case *JsAssignExpr:
		eachPatternIdent(p.L, fn)
	case *JsSpreadExpr:
		eachPatternIdent(p.X, fn)
	}
}

func unwrapPatternElem(node JsExpr) JsExpr {
	if a, ok := node.(*JsAssignExpr); ok {
		return a.L
	}
	if s, ok := node.(*JsSpreadExpr); ok {
		return s.X
	}
	return node
}

// ─────────────────────────────────────────────
//  Unused bindings — separate pass over the built scope tree
// ─────────────────────────────────────────────

// ReportUnused emits a hint for each user-declared binding that has no
// references.  Builtins and imports are excluded — unused imports are
// usually a deliberate side-effect import, and builtins are noise.
// Parameter and catch-binding noise is also skipped (those routinely
// go unused for ergonomic reasons).
func ReportUnused(root *Scope, ds *DiagSet) {
	if root == nil {
		return
	}
	var visit func(*Scope)
	visit = func(s *Scope) {
		for _, bind := range s.Bindings {
			if shouldReportUnused(bind) {
				ds.HintWith(bind.Pos, "VELA-SCOPE-UNUSED", "Remove the binding or use it.",
					"%q is declared but never used", bind.Name)
			}
		}
		for _, child := range s.Children {
			visit(child)
		}
	}
	visit(root)
}

func shouldReportUnused(b *Binding) bool {
	if len(b.Refs) > 0 {
		return false
	}
	switch b.Kind {
	case BindBuiltin, BindImport, BindParam, BindCatch:
		return false
	}
	// Identifiers prefixed with _ are conventionally "intentionally
	// unused" — skip them.
	if strings.HasPrefix(b.Name, "_") {
		return false
	}
	return true
}
