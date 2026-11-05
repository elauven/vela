package jsast

// TypeScript support (task 080, docs/TYPESCRIPT.md TS-2, TS-3).
//
// The parser erases types while it parses: with ParseOptions.TypeScript the
// returned JsProgram is an ordinary JavaScript tree, so every pass of the
// compiler (analysis, client codegen, the SSR translation, the printer) works
// unchanged on TypeScript sources. What the types said is kept beside the
// tree, for the passes that want it:
//
//   - annotations that belong to a JavaScript node hang off that node:
//     JsVarDeclarator.Type, the TS signature of functions and arrows, the TS
//     header of classes and class members, JsTryStmt.CatchType;
//   - declarations that have no JavaScript at all (interfaces, type aliases,
//     `declare …`, overload signatures, `export type`) and type-only imports
//     are listed in JsProgram.TS, with the names referenced in type
//     positions.
//
// None of these structures contains a JsNode, so WalkNode, the tree
// invariants and the clone helpers never see them, and a cloned tree may
// share them: they are never mutated after parsing.
//
// Expression-level syntax (`as`, `satisfies`, non-null `!`, type arguments
// of calls) leaves nothing behind: the expression it wraps is the node.

// ParseOptions selects the source language.
type ParseOptions struct {
	// TypeScript accepts the erasable TypeScript syntax of TS-2.1 and
	// reports the constructs of TS-2.4 with DiagTSNonErasable.
	TypeScript bool
}

// DiagCode classifies a parse diagnostic. The empty code is a syntax error.
type DiagCode string

const (
	// DiagTSNonErasable: TypeScript syntax that generates code (`enum`,
	// value namespaces, parameter properties, `import x = …`, `export =`,
	// `<T>x`), rejected by TS-2.4.
	DiagTSNonErasable DiagCode = "ts-non-erasable"
)

// TSInfo is what a TypeScript program declared beyond its JavaScript.
type TSInfo struct {
	// Decls are the erased top-level declarations, in source order.
	Decls []*TSDecl
	// Imports are the bindings created by type-only imports: `import type`
	// declarations and `type` specifiers.
	Imports []TSTypeImport
	// Refs are the identifiers referenced in type positions anywhere in the
	// program: the first name of every type reference (`A` of `A.B<C>`) and
	// of every `typeof` query, except the names of type parameters in scope.
	Refs []TSRef
	// Erased are the source ranges that hold TypeScript syntax, in the
	// order they were parsed (nested ranges are possible). BlankTypeScript
	// turns them into JavaScript of the same length.
	Erased []TSErased
}

// TSErasedKind is the kind of a TSErased range.
type TSErasedKind uint8

const (
	// TSErasedSyntax is replaced by spaces: annotations, type arguments and
	// parameters, `as T`, `!`, modifiers, `type` specifiers.
	TSErasedSyntax TSErasedKind = iota
	// TSErasedStatement is a statement or class member with no JavaScript:
	// replaced by spaces, with a `;` first where the lines around it would
	// otherwise join (TS-2.3).
	TSErasedStatement
	// TSErasedMove moves the byte at Pos to the offset End: the `)` of an
	// arrow whose return type spans lines, the `(` of a generic arrow whose
	// type parameters do (TS-2.3: `=>` and the operand of `return` stay on
	// their line).
	TSErasedMove
)

// TSErased is one range of TypeScript syntax.
type TSErased struct {
	Pos, End int
	Kind     TSErasedKind
}

// TSTypeImport is one binding of a type-only import.
type TSTypeImport struct {
	Pos, End  int
	Local     string
	Imported  string // "default" for a default import, "*" for a namespace import
	Source    string
	Statement bool // from `import type …`, not from a `type` specifier
}

// TSRef is a name referenced in a type position.
type TSRef struct {
	Pos, End int
	Name     string
	// Query is set for `typeof x`, which refers to a value.
	Query bool
}

// TSDeclKind is the kind of an erased declaration.
type TSDeclKind uint8

const (
	TSDeclInterface TSDeclKind = iota
	TSDeclTypeAlias
	TSDeclNamespace // `namespace N {}` with types only, and `declare namespace`
	TSDeclModule    // `declare module 'm' {}`, `declare global {}`
	TSDeclVar       // `declare var|let|const`
	TSDeclFunction  // `declare function`, and overload signatures
	TSDeclClass     // `declare class`
	TSDeclEnum      // `declare enum`
	TSDeclExport    // `export type { … }`, `export type * from …`, `export as namespace N`
)

// TSDecl is a declaration that has no JavaScript.
type TSDecl struct {
	Pos, End int
	Kind     TSDeclKind
	// Name is the declared name; for TSDeclModule the module name (or
	// "global"); empty for TSDeclExport.
	Name     string
	Exported bool
	Default  bool
	Declare  bool

	TypeParams []*TSTypeParam
	// Type is the aliased type (type alias), the body (interface: an object
	// type), or the declared type (`declare const x: T`).
	Type    *TSType
	Extends []*TSType      // interface extends
	Sig     *TSSignature   // functions
	Names   []TSExportSpec // TSDeclExport of a list
	Source  string         // TSDeclExport with `from`
}

// TSExportSpec is one name of `export type { a as b }`.
type TSExportSpec struct{ Local, Exported string }

// TSTypeKind is the kind of a TSType.
type TSTypeKind uint8

const (
	TSKeyword      TSTypeKind = iota // Name: any, unknown, never, void, undefined, null, boolean, number, string, bigint, symbol, object, intrinsic, const (`as const`)
	TSThis                           // this
	TSRefType                        // Name (dotted), Args: type arguments
	TSQuery                          // typeof Name, Args: type arguments
	TSImportType                     // import('Name').Label<Args>
	TSLiteral                        // Name: the literal as written ('a', 1, -1, true, 1n)
	TSTemplate                       // Name: the template as written, Args: the types of its holes
	TSArray                          // Args[0][]
	TSTuple                          // [Args…]
	TSNamedMember                    // tuple element `Name?: Args[0]`, Rest for `...Name: T`
	TSOptionalType                   // tuple element `Args[0]?`
	TSRestType                       // tuple element `...Args[0]`
	TSUnion                          // Args[0] | Args[1] | …
	TSIntersection                   // Args[0] & Args[1] & …
	TSFunction                       // Sig: (params) => return
	TSConstructor                    // new (params) => return; Abstract
	TSObject                         // { Members }
	TSMapped                         // { [Name in Args[0] as Args[1]]: Args[2] }; Readonly, OptionalMod
	TSIndexed                        // Args[0][Args[1]]
	TSOperator                       // Name: keyof, unique, readonly; Args[0]
	TSConditional                    // Args[0] extends Args[1] ? Args[2] : Args[3]
	TSInfer                          // infer Name [extends Args[0]]
	TSPredicate                      // [asserts] Name [is Args[0]]; Name may be "this"
	TSParen                          // (Args[0])
)

// TSType is a parsed type. Fields other than Pos, End, Kind and Name are
// used as the kind's comment above says.
type TSType struct {
	Pos, End int
	Kind     TSTypeKind
	Name     string
	Label    string // TSImportType: the qualifier after the import
	Args     []*TSType
	Members  []*TSMember
	Sig      *TSSignature

	Optional bool // TSNamedMember
	Rest     bool // TSNamedMember
	Abstract bool // TSConstructor
	Asserts  bool // TSPredicate

	// TSMapped modifiers: Readonly is "", "readonly", "+readonly" or
	// "-readonly"; OptionalMod is "", "?", "+?" or "-?".
	Readonly    string
	OptionalMod string
}

// TSMemberKind is the kind of a member of an object type or interface.
type TSMemberKind uint8

const (
	TSPropertySig TSMemberKind = iota
	TSMethodSig
	TSCallSig
	TSConstructSig
	TSIndexSig
	TSGetSig
	TSSetSig
)

// TSMember is a member of an object type or an interface.
type TSMember struct {
	Pos, End int
	Kind     TSMemberKind
	// Name is the property name; for a computed name ([Symbol.iterator])
	// the source text of the expression, with Computed set.
	Name     string
	Computed bool
	Optional bool
	Readonly bool
	// Type is the property type or the value type of an index signature.
	Type *TSType
	// Sig is the signature of methods, call, construct and accessor
	// signatures, and holds the key parameter of an index signature.
	Sig *TSSignature
}

// TSSignature is the TypeScript part of a function, method, arrow or
// function type.
type TSSignature struct {
	TypeParams []*TSTypeParam
	// This is the type of an explicit `this` parameter, which is not part of
	// the JavaScript parameter list.
	This *TSType
	// Params is aligned with the JavaScript parameters of a function (one
	// entry per parameter, Type nil when not annotated). For a function type
	// or a member signature it is the whole parameter list.
	Params []*TSParam
	Return *TSType // nil when not annotated
}

// TSParam is one parameter of a signature.
type TSParam struct {
	Pos, End int
	Name     string // "" for a destructuring pattern
	Type     *TSType
	Optional bool
	Rest     bool
}

// TSTypeParam is a type parameter `const in out T extends C = D`.
type TSTypeParam struct {
	Pos, End   int
	Name       string
	Constraint *TSType
	Default    *TSType
	Const      bool
	In, Out    bool
}

// TSClass is the TypeScript part of a class declaration or expression.
type TSClass struct {
	TypeParams    []*TSTypeParam
	SuperTypeArgs []*TSType
	Implements    []*TSType
	Abstract      bool
}

// TSClassMember is the TypeScript part of a class member.
type TSClassMember struct {
	Type     *TSType // field type
	Access   string  // "", "public", "private", "protected"
	Optional bool
	Definite bool
	Readonly bool
	Override bool
}
