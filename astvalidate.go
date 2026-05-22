package main

import "reflect"

// astvalidate.go — validation passes over the generated JS AST.
//
// This is the payoff of the AST migration.  The string-concatenation
// generator could emit syntactically-invalid JavaScript — `if ()`
// with an empty condition, a `${#key x}` block leaking into a
// template literal, a directive name escaping into setAttribute —
// and nothing caught it until `node --check` at best, or a runtime
// error in the browser at worst.
//
// With a real output AST, an entire class of those bugs becomes
// detectable structurally, before a single character is printed:
//
//   - empty / malformed control structures — an JsIfStmt with no
//     test, a call with no callee, a binary expr missing a side
//   - references to identifiers that were never declared anywhere
//     in the unified module tree (generated wiring + user script)
//   - statements after an unconditional return (unreachable output)
//
// ValidateProgram runs these checks and returns a list of
// diagnostics.  A generator bug surfaces here as a clear,
// located message instead of as malformed text downstream.

// ValidationIssue is a single problem found in the generated AST.
type ValidationIssue struct {
	Kind string // "empty-node", "undeclared-ref", "unreachable"
	Msg  string
}

// ValidateProgram runs every validation pass over a generated
// program and returns all issues found.  An empty slice means the
// generated tree is structurally sound.
func ValidateProgram(prog *JsProgram) []ValidationIssue {
	var issues []ValidationIssue
	issues = append(issues, checkEmptyNodes(prog)...)
	issues = append(issues, checkUndeclaredRefs(prog)...)
	issues = append(issues, checkUnreachable(prog)...)
	return issues
}

// ─────────────────────────────────────────────
//  Pass 1 — empty / malformed nodes
// ─────────────────────────────────────────────

// checkEmptyNodes walks the tree for structurally-incomplete nodes:
// control structures missing a required child, calls without a
// callee, operators missing an operand.  These are exactly the
// shapes the string generator could emit as `if ()`, `()` etc.
func checkEmptyNodes(prog *JsProgram) []ValidationIssue {
	var issues []ValidationIssue
	report := func(what string) {
		issues = append(issues, ValidationIssue{
			Kind: "empty-node",
			Msg:  "generated " + what,
		})
	}
	for _, s := range prog.Body {
		WalkNode(s, func(n JsNode) bool {
			switch x := n.(type) {
			case *JsIfStmt:
				if isNilExpr(x.Test) {
					report("if statement with no condition")
				}
			case *JsWhileStmt:
				if isNilExpr(x.Test) {
					report("while loop with no condition")
				}
			case *JsConditionalExpr:
				if isNilExpr(x.Test) || isNilExpr(x.Then) || isNilExpr(x.Else) {
					report("ternary with a missing branch")
				}
			case *JsCallExpr:
				if isNilExpr(x.Callee) {
					report("call with no callee")
				}
			case *JsBinaryExpr:
				if isNilExpr(x.L) || isNilExpr(x.R) {
					report("binary expression " + x.Op + " missing an operand")
				}
			case *JsLogicalExpr:
				if isNilExpr(x.L) || isNilExpr(x.R) {
					report("logical expression " + x.Op + " missing an operand")
				}
			case *JsAssignExpr:
				if isNilExpr(x.L) || isNilExpr(x.R) {
					report("assignment missing a side")
				}
			case *JsMemberExpr:
				if isNilExpr(x.Obj) || isNilExpr(x.Prop) {
					report("member access missing object or property")
				}
			case *JsReturnStmt:
				// A return with no argument is legal — no check.
			}
			return true
		})
	}
	return issues
}

// isNilExpr reports whether an expression slot is empty — either a
// genuine nil interface or a typed-nil pointer.
func isNilExpr(e JsExpr) bool {
	if e == nil {
		return true
	}
	return isNilNodeV(e)
}

// isNilNodeV reports whether a JsNode interface value wraps a nil
// pointer.  A typed nil (e.g. (*JsIdent)(nil) stored in a JsExpr)
// is non-nil as an interface but still an empty slot — exactly the
// shape a codegen bug produces when it forgets to fill a child.
//
// Reflection keeps this correct as new node types are added; the
// AST has ~30 pointer-typed node structs and an exhaustive type
// switch would silently miss any future addition.
func isNilNodeV(n JsNode) bool {
	v := reflect.ValueOf(n)
	return v.Kind() == reflect.Ptr && v.IsNil()
}

// ─────────────────────────────────────────────
//  Pass 2 — undeclared references
// ─────────────────────────────────────────────

// checkUndeclaredRefs collects every identifier the module
// declares (imports, top-level + nested declarations, function
// params, the component function's own params) and every
// identifier the module reads, and reports reads with no matching
// declaration.
//
// Runtime globals and DOM/JS built-ins are whitelisted.  This pass
// is deliberately conservative: it only fires when a name is
// genuinely undeclared anywhere in the unified tree, which for
// generated code means a codegen bug (a wiring variable that was
// referenced but never emitted).
func checkUndeclaredRefs(prog *JsProgram) []ValidationIssue {
	declared := map[string]bool{}
	for k := range jsGlobals {
		declared[k] = true
	}

	// Collect declarations across the whole tree.
	for _, s := range prog.Body {
		collectDeclared(s, declared)
	}

	// Collect reads, report any not declared.
	var issues []ValidationIssue
	seen := map[string]bool{}
	for _, s := range prog.Body {
		WalkNode(s, func(n JsNode) bool {
			id, ok := n.(*JsIdent)
			if !ok || id == nil {
				return true
			}
			name := id.Name
			if declared[name] || seen[name] {
				return true
			}
			// Property keys and member property names are not
			// references — but WalkNode already only yields the
			// Prop of a non-computed member as an ident, so guard
			// the common safe names by skipping anything that is a
			// plausible property.  Conservative: only flag names
			// with the generated $$ prefix, where an undeclared
			// reference is unambiguously a codegen bug.
			if !hasDollarDollar(name) {
				return true
			}
			seen[name] = true
			issues = append(issues, ValidationIssue{
				Kind: "undeclared-ref",
				Msg:  "generated reference to undeclared variable " + name,
			})
			return true
		})
	}
	return issues
}

// collectDeclared records every name introduced by a declaration
// node anywhere in the subtree.
func collectDeclared(root JsNode, out map[string]bool) {
	WalkNode(root, func(n JsNode) bool {
		switch x := n.(type) {
		case *JsVarDecl:
			for _, d := range x.Decls {
				collectPatternNames(d.Id, out)
			}
		case *JsFuncDecl:
			if x.Name != "" {
				out[x.Name] = true
			}
			for _, p := range x.Params {
				collectPatternNames(p, out)
			}
		case *JsFuncExpr:
			if x.Name != "" {
				out[x.Name] = true
			}
			for _, p := range x.Params {
				collectPatternNames(p, out)
			}
		case *JsArrowFunc:
			for _, p := range x.Params {
				collectPatternNames(p, out)
			}
		case *JsClassDecl:
			if x.Name != "" {
				out[x.Name] = true
			}
		case *JsTryStmt:
			if x.CatchParam != nil {
				collectPatternNames(x.CatchParam, out)
			}
		case *JsImportDecl:
			if x.DefaultName != "" {
				out[x.DefaultName] = true
			}
			if x.Namespace != "" {
				out[x.Namespace] = true
			}
			for _, sp := range x.Specifiers {
				if sp.Local != "" {
					out[sp.Local] = true
				} else {
					out[sp.Imported] = true
				}
			}
		}
		return true
	})
}

// collectPatternNames extracts every binding name from a binding
// pattern — a plain identifier, or the identifiers inside an array
// / object destructuring pattern.
func collectPatternNames(e JsExpr, out map[string]bool) {
	if e == nil || isNilNodeV(e) {
		return
	}
	switch x := e.(type) {
	case *JsIdent:
		out[x.Name] = true
	case *JsArrayExpr:
		for _, el := range x.Elems {
			if el != nil {
				collectPatternNames(el, out)
			}
		}
	case *JsObjectExpr:
		for _, pr := range x.Props {
			if pr.Value != nil {
				collectPatternNames(pr.Value, out)
			} else if pr.Key != nil {
				collectPatternNames(pr.Key, out)
			}
		}
	case *JsAssignExpr:
		// default in a pattern: `{ a = 1 }` / `[x = 0]`
		collectPatternNames(x.L, out)
	case *JsSpreadExpr:
		collectPatternNames(x.X, out)
	}
}

// hasDollarDollar reports whether a name carries the $$ prefix the
// compiler uses for all generated wiring identifiers.
func hasDollarDollar(s string) bool {
	return len(s) >= 2 && s[0] == '$' && s[1] == '$'
}

// jsGlobals is the whitelist of identifiers that are legitimately
// available without a local declaration — JS built-ins, DOM
// globals, and the component-function parameters / runtime symbols
// the compiler relies on.
var jsGlobals = map[string]bool{
	// component-function params + runtime
	"$$target": true, "$$props": true, "$$slots": true,
	"$$v": true, "$$i": true, "$$e": true, "$event": true,
	"$$defer": true, "$$blk": true, "$$c": true, "$$arr": true,
	"$$active": true, "$$keynew": true, "$$key": true,
	"$$newKeys": true, "$$newBlocks": true, "$$p": true,
	"$$sf": true, "$$sn": true, "$$tmp": true, "$$t": true,
	"$$h": true, "$$fb": true, "$$keyset": true,
	// JS built-ins / globals
	"undefined": true, "null": true, "true": true, "false": true,
	"NaN": true, "Infinity": true, "globalThis": true,
	"console": true, "document": true, "window": true,
	"Object": true, "Array": true, "String": true, "Number": true,
	"Boolean": true, "Symbol": true, "Math": true, "JSON": true,
	"Promise": true, "Map": true, "Set": true, "WeakMap": true,
	"WeakSet": true, "Proxy": true, "Reflect": true, "Date": true,
	"RegExp": true, "Error": true, "TypeError": true,
	"CustomEvent": true, "Event": true, "Node": true,
	"setTimeout": true, "clearTimeout": true,
	"setInterval": true, "clearInterval": true,
	"requestAnimationFrame": true, "fetch": true,
	"parseInt": true, "parseFloat": true, "isNaN": true,
	"encodeURIComponent": true, "decodeURIComponent": true,
	"this": true, "super": true, "arguments": true,
}

// ─────────────────────────────────────────────
//  Pass 3 — unreachable statements
// ─────────────────────────────────────────────

// checkUnreachable reports statements that follow an unconditional
// return / throw / break / continue within the same block — code
// the generator emitted that can never run.
func checkUnreachable(prog *JsProgram) []ValidationIssue {
	var issues []ValidationIssue
	for _, s := range prog.Body {
		WalkNode(s, func(n JsNode) bool {
			blk, ok := n.(*JsBlock)
			if !ok {
				return true
			}
			for i, st := range blk.Body {
				if isTerminator(st) && i < len(blk.Body)-1 {
					issues = append(issues, ValidationIssue{
						Kind: "unreachable",
						Msg:  "generated unreachable statement after " +
							terminatorName(st),
					})
					break
				}
			}
			return true
		})
	}
	return issues
}

// isTerminator reports whether a statement unconditionally diverts
// control flow, making any following sibling unreachable.
func isTerminator(s JsStmt) bool {
	switch s.(type) {
	case *JsReturnStmt, *JsThrowStmt, *JsBreakStmt, *JsContinueStmt:
		return true
	}
	return false
}

func terminatorName(s JsStmt) string {
	switch s.(type) {
	case *JsReturnStmt:
		return "return"
	case *JsThrowStmt:
		return "throw"
	case *JsBreakStmt:
		return "break"
	case *JsContinueStmt:
		return "continue"
	}
	return "control-flow exit"
}
