package main

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
// terminator and emits warnings via ds.
func DetectUnreachable(prog *JsProgram, ds *DiagSet) {
	if prog == nil {
		return
	}
	analyzeStmts(prog.Body, ds)
}

// analyzeStmts walks a list of statements in order.  Returns the
// outcome of the last analysed statement so the caller can propagate
// the verdict up (used by blocks inside if/else).
func analyzeStmts(stmts []JsStmt, ds *DiagSet) flowOutcome {
	terminated := false
	var firstDead JsStmt
	last := flowContinue

	for _, s := range stmts {
		if terminated {
			if firstDead == nil {
				firstDead = s
				ds.WarnWith(s.nodePos(), "VELA-FLOW-UNREACHABLE", "Remove the statement or make the preceding control flow fall through.", "unreachable code")
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
	return last
}

// analyzeStmt classifies a single statement's outcome and recurses
// into any nested code.
func analyzeStmt(s JsStmt, ds *DiagSet) flowOutcome {
	if s == nil {
		return flowContinue
	}
	switch n := s.(type) {

	case *JsReturnStmt:
		if n.Arg != nil {
			scanNestedFunctions(n, ds)
		}
		return flowReturn

	case *JsThrowStmt:
		scanNestedFunctions(n, ds)
		return flowReturn

	case *JsBreakStmt:
		return flowBranch

	case *JsContinueStmt:
		return flowBranch

	case *JsBlock:
		return analyzeStmts(n.Body, ds)

	case *JsIfStmt:
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

	case *JsForStmt:
		analyzeStmt(n.Body, ds)
		return flowContinue

	case *JsForInStmt:
		analyzeStmt(n.Body, ds)
		return flowContinue

	case *JsWhileStmt:
		analyzeStmt(n.Body, ds)
		return flowContinue

	case *JsDoWhileStmt:
		analyzeStmt(n.Body, ds)
		return flowContinue

	case *JsTryStmt:
		var blockOut, handlerOut flowOutcome
		if n.Block != nil {
			blockOut = analyzeStmts(n.Block.Body, ds)
		}
		if n.Handler != nil {
			handlerOut = analyzeStmts(n.Handler.Body, ds)
		}
		if n.Finalizer != nil {
			// A finally that terminates always wins — even an
			// otherwise-finished try/catch can't escape it.
			if finOut := analyzeStmts(n.Finalizer.Body, ds); finOut.terminates() {
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

	case *JsSwitchStmt:
		// Switch flow is hard (fallthrough between cases, default
		// optional).  Just analyse each case body in isolation; don't
		// try to claim the switch as a whole terminates.
		for _, c := range n.Cases {
			analyzeStmts(c.Body, ds)
		}
		return flowContinue

	case *JsFuncDecl:
		// A function declaration introduces a binding but doesn't
		// execute any code at this point; its body has its own flow.
		if n.Body != nil {
			analyzeStmts(n.Body.Body, ds)
		}
		return flowContinue

	case *JsClassDecl:
		// Method bodies have their own flow.
		for _, m := range n.Members {
			scanNestedFunctions(m.Value, ds)
		}
		return flowContinue

	case *JsLabeledStmt:
		return analyzeStmt(n.Body, ds)

	case *JsExprStmt:
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
func scanNestedFunctions(node JsNode, ds *DiagSet) {
	if node == nil {
		return
	}
	WalkNode(node, func(n JsNode) bool {
		switch fn := n.(type) {
		case *JsFuncDecl:
			if fn.Body != nil {
				analyzeStmts(fn.Body.Body, ds)
			}
			return false // don't descend; analyzeStmts handled it
		case *JsFuncExpr:
			if fn.Body != nil {
				analyzeStmts(fn.Body.Body, ds)
			}
			return false
		case *JsArrowFunc:
			if block, ok := fn.Body.(*JsBlock); ok && block != nil {
				analyzeStmts(block.Body, ds)
			}
			return false
		}
		return true
	})
}
