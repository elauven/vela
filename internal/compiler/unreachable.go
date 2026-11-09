package compiler

import (
	"github.com/elauven/vela/internal/diag"
	"github.com/elauven/vela/internal/jsast"
)

// unreachable.go — flag statements that the JS runtime can never reach.
//
// Operates at the statement level over a parsed JsProgram.  For each
// list of statements, we walk in order tracking whether control flow
// is "still alive."  A statement that definitely terminates (return,
// throw, break, continue, or an if/else whose both arms terminate)
// kills the rest of the list; the first dead statement is flagged and
// the remainder is skipped — except we still recurse into any nested
// function bodies, because their flow is independent.
//
// Conservative.  We don't try to recognise things like
//
//   while (true) { … never break … }   then unreachable code after
//
// or
//
//   switch (k) { case 1: return; case 2: return; }   then unreachable
//
// because both require non-trivial reasoning (constant evaluation for
// the loop, exhaustiveness analysis for switch).  Easy wins only.

// flowOutcome describes how a statement completes.  Loops/try always
// flowContinue because we can't prove they don't exit normally.
type flowOutcome uint8

const (
	flowContinue flowOutcome = iota // execution falls through to next stmt
	flowReturn                      // return or throw — exits the function
	flowBranch                      // break or continue — jumps in enclosing loop/switch
)

func (f flowOutcome) terminates() bool { return f != flowContinue }

// DetectUnreachable scans a program for statements after a definite
// terminator and emits warnings via ds.  It then removes those
// statements from prog: the warning says they never run, and the
// generated-AST validator rejects unreachable statements (task 068).
// Hoisted declarations stay so that bindings keep their meaning: a
// function declaration is kept whole, a `var` keeps its names without
// initializers.  Everything else after the terminator is dropped.
func DetectUnreachable(prog *jsast.JsProgram, ds *diag.DiagSet) {
	if prog == nil {
		return
	}
	analyzeStmts(&prog.Body, ds)
}

// pruneDead returns what remains of the dead statements after a
// terminator: function declarations unchanged and `var` declarations
// stripped of their initializers.
func pruneDead(dead []jsast.JsStmt) []jsast.JsStmt {
	var kept []jsast.JsStmt
	for _, s := range dead {
		switch n := s.(type) {
		case *jsast.JsFuncDecl:
			kept = append(kept, n)
		case *jsast.JsVarDecl:
			if n.Kind != "var" {
				continue
			}
			if decl := hoistedVar(n); decl != nil {
				kept = append(kept, decl)
			}
		}
	}
	return kept
}

// hoistedVar returns `var a, b;` for the plain identifiers a dead `var`
// declares, or nil when there are none.  A destructuring pattern
// declares its names too; they are collected the same way.
func hoistedVar(n *jsast.JsVarDecl) *jsast.JsVarDecl {
	var decls []*jsast.JsVarDeclarator
	for _, d := range n.Decls {
		for _, id := range patternIdents(d.Id, nil) {
			decls = append(decls, &jsast.JsVarDeclarator{BaseNode: d.BaseNode, Id: id})
		}
	}
	if len(decls) == 0 {
		return nil
	}
	return &jsast.JsVarDecl{BaseStmt: n.BaseStmt, Kind: "var", Decls: decls}
}

// patternIdents appends the identifiers a binding pattern declares.
func patternIdents(e jsast.JsExpr, out []*jsast.JsIdent) []*jsast.JsIdent {
	switch x := e.(type) {
	case *jsast.JsIdent:
		if x != nil {
			out = append(out, x)
		}
	case *jsast.JsArrayExpr:
		for _, el := range x.Elems {
			if el != nil {
				out = patternIdents(el, out)
			}
		}
	case *jsast.JsObjectExpr:
		for _, pr := range x.Props {
			if pr.Value != nil {
				out = patternIdents(pr.Value, out)
			} else if pr.Key != nil {
				out = patternIdents(pr.Key, out)
			}
		}
	case *jsast.JsAssignExpr:
		out = patternIdents(x.L, out)
	case *jsast.JsSpreadExpr:
		out = patternIdents(x.X, out)
	}
	return out
}

// analyzeStmts walks a list of statements in order.  Returns the
// outcome of the last analysed statement so the caller can propagate
// the verdict up (used by blocks inside if/else).
//
// Statements after the terminator are pruned from *list in place (see
// DetectUnreachable).
func analyzeStmts(list *[]jsast.JsStmt, ds *diag.DiagSet) flowOutcome {
	terminated := false
	var firstDead jsast.JsStmt
	last := flowContinue
	stmts := *list
	deadFrom := -1

	for i, s := range stmts {
		if terminated {
			if deadFrom < 0 {
				deadFrom = i
			}
			if firstDead == nil {
				firstDead = s
				ds.Report(diag.SevWarning, s.NodePos(), s.NodeEnd(), "VELA-FLOW-UNREACHABLE", "Remove the statement or make the preceding control flow fall through.", "unreachable code")
			}
			// Even after the marker we still recurse into nested
			// function bodies — their flow is independent and a
			// warning inside an unreachable function is still useful.
			scanNestedFunctions(s, ds)
			continue
		}
		last = analyzeStmt(s, ds)
		if last.terminates() {
			terminated = true
		}
	}
	if deadFrom >= 0 {
		*list = append(stmts[:deadFrom:deadFrom], pruneDead(stmts[deadFrom:])...)
	}
	return last
}

// analyzeStmt classifies a single statement's outcome and recurses
// into any nested code.
func analyzeStmt(s jsast.JsStmt, ds *diag.DiagSet) flowOutcome {
	if s == nil {
		return flowContinue
	}
	switch n := s.(type) {

	case *jsast.JsReturnStmt:
		if n.Arg != nil {
			scanNestedFunctions(n, ds)
		}
		return flowReturn

	case *jsast.JsThrowStmt:
		scanNestedFunctions(n, ds)
		return flowReturn

	case *jsast.JsBreakStmt:
		return flowBranch

	case *jsast.JsContinueStmt:
		return flowBranch

	case *jsast.JsBlock:
		return analyzeStmts(&n.Body, ds)

	case *jsast.JsIfStmt:
		thenOut := analyzeStmt(n.Then, ds)
		if n.Else == nil {
			// No else → fallthrough is always possible.
			return flowContinue
		}
		elseOut := analyzeStmt(n.Else, ds)
		if thenOut.terminates() && elseOut.terminates() {
			// Both arms exit — the if-statement as a whole terminates.
			// Prefer 'return' over 'branch' when mixed: a function
			// containing return+break still definitely exits the fn.
			if thenOut == flowReturn || elseOut == flowReturn {
				return flowReturn
			}
			return flowBranch
		}
		return flowContinue

	case *jsast.JsForStmt:
		analyzeStmt(n.Body, ds)
		return flowContinue

	case *jsast.JsForInStmt:
		analyzeStmt(n.Body, ds)
		return flowContinue

	case *jsast.JsWhileStmt:
		analyzeStmt(n.Body, ds)
		return flowContinue

	case *jsast.JsDoWhileStmt:
		analyzeStmt(n.Body, ds)
		return flowContinue

	case *jsast.JsTryStmt:
		var blockOut, handlerOut flowOutcome
		if n.Block != nil {
			blockOut = analyzeStmts(&n.Block.Body, ds)
		}
		if n.Handler != nil {
			handlerOut = analyzeStmts(&n.Handler.Body, ds)
		}
		if n.Finalizer != nil {
			// A finally that terminates always wins — even an
			// otherwise-finished try/catch can't escape it.
			if finOut := analyzeStmts(&n.Finalizer.Body, ds); finOut.terminates() {
				return finOut
			}
		}
		// Without a catch we can't claim the block's outcome — any
		// statement could throw and skip the rest.  With a catch, if
		// both block and handler terminate, the try as a whole does.
		if n.Handler != nil && blockOut.terminates() && handlerOut.terminates() {
			if blockOut == flowReturn || handlerOut == flowReturn {
				return flowReturn
			}
			return flowBranch
		}
		return flowContinue

	case *jsast.JsSwitchStmt:
		// Switch flow is hard (fallthrough between cases, default
		// optional).  Just analyse each case body in isolation; don't
		// try to claim the switch as a whole terminates.
		for _, c := range n.Cases {
			analyzeStmts(&c.Body, ds)
		}
		return flowContinue

	case *jsast.JsFuncDecl:
		// A function declaration introduces a binding but doesn't
		// execute any code at this point; its body has its own flow.
		if n.Body != nil {
			analyzeStmts(&n.Body.Body, ds)
		}
		return flowContinue

	case *jsast.JsClassDecl:
		// Method bodies have their own flow.
		for _, m := range n.Members {
			scanNestedFunctions(m.Value, ds)
		}
		return flowContinue

	case *jsast.JsLabeledStmt:
		return analyzeStmt(n.Body, ds)

	case *jsast.JsExprStmt:
		scanNestedFunctions(n.X, ds)
		return flowContinue
	}
	return flowContinue
}

// scanNestedFunctions descends through an arbitrary node and runs
// statement-level flow analysis on every function/arrow body it finds.
// Used to make sure we don't miss unreachable code that lives inside,
// say, the right-hand side of an assignment or a callback argument.
//
// Stops descending once a function is hit (its body has already been
// scheduled for analysis) so we don't double-analyse nested fns.
func scanNestedFunctions(node jsast.JsNode, ds *diag.DiagSet) {
	if node == nil {
		return
	}
	jsast.WalkNode(node, func(n jsast.JsNode) bool {
		switch fn := n.(type) {
		case *jsast.JsFuncDecl:
			if fn.Body != nil {
				analyzeStmts(&fn.Body.Body, ds)
			}
			return false // don't descend; analyzeStmts handled it
		case *jsast.JsFuncExpr:
			if fn.Body != nil {
				analyzeStmts(&fn.Body.Body, ds)
			}
			return false
		case *jsast.JsArrowFunc:
			if block, ok := fn.Body.(*jsast.JsBlock); ok && block != nil {
				analyzeStmts(&block.Body, ds)
			}
			return false
		}
		return true
	})
}
