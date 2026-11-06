package jsast

import (
	"fmt"
	"strings"
	"testing"
)

// The TypeScript corpus (task 080). Every valid case is a TypeScript source
// and the JavaScript it erases to: parsing the TypeScript must report
// nothing, keep the tree invariants, give the same tree as the JavaScript,
// and print to JavaScript that parses (with TypeScript off) to that tree.
// The same sources are checked against TypeScript's own transpileModule in
// ts_oracle_test.go.

type tsCase struct {
	group, name string
	ts, js      string
}

var tsValidCases = []tsCase{
	// Annotations
	{"Annotations", "let", "let x: number = 1", "let x = 1"},
	{"Annotations", "let without init", "let x: string", "let x"},
	{"Annotations", "const destructuring", "const { a, b }: { a: number; b: string } = o", "const { a, b } = o"},
	{"Annotations", "array pattern", "const [a, b]: [number, string] = t", "const [a, b] = t"},
	{"Annotations", "definite", "let x!: number", "let x"},
	{"Annotations", "several declarators", "let a: number = 1, b: string = 'b'", "let a = 1, b = 'b'"},
	{"Annotations", "function params and return", "function f(a: number, b?: string): void {}", "function f(a, b) {}"},
	{"Annotations", "default and type", "function f(a: number = 1) {}", "function f(a = 1) {}"},
	{"Annotations", "rest param", "function f(...rest: number[]): number { return 0 }", "function f(...rest) { return 0 }"},
	{"Annotations", "this param", "function f(this: Window, a: number) {}", "function f(a) {}"},
	{"Annotations", "this param alone", "function f(this: Window) {}", "function f() {}"},
	{"Annotations", "destructured param", "function f({ a, b }: Opts, [c]: number[]) {}", "function f({ a, b }, [c]) {}"},
	{"Annotations", "catch", "try {} catch (e: unknown) {}", "try {} catch (e) {}"},
	{"Annotations", "function expression", "const f = function (a: number): number { return a }", "const f = function (a) { return a }"},
	{"Annotations", "async function", "async function f(): Promise<void> {}", "async function f() {}"},
	{"Annotations", "generator", "function* g(): Generator<number, void, unknown> { yield 1 }", "function* g() { yield 1 }"},
	{"Annotations", "for of", "for (const x of xs as number[]) {}", "for (const x of xs) {}"},
	{"Annotations", "for loop", "for (let i: number = 0; i < n; i++) {}", "for (let i = 0; i < n; i++) {}"},

	// Arrows
	{"Arrows", "typed params", "const f = (a: number, b: string) => a", "const f = (a, b) => a"},
	{"Arrows", "return type", "const f = (a: number): number => a", "const f = (a) => a"},
	{"Arrows", "empty params return type", "const f = (): void => {}", "const f = () => {}"},
	{"Arrows", "async return type", "const f = async (a: number): Promise<number> => a", "const f = async (a) => a"},
	{"Arrows", "generic", "const f = <T,>(x: T): T => x", "const f = (x) => x"},
	{"Arrows", "generic constraint", "const f = <T extends object>(x: T) => x", "const f = (x) => x"},
	{"Arrows", "async generic", "const f = async <T>(x: T) => x", "const f = async (x) => x"},
	{"Arrows", "optional param", "const f = (a?: number) => a", "const f = (a) => a"},
	{"Arrows", "predicate", "const isS = (x: unknown): x is string => typeof x === 'string'", "const isS = (x) => typeof x === 'string'"},
	{"Arrows", "function type return", "const f = (a: number): ((b: number) => number) => (b) => a + b", "const f = (a) => (b) => a + b"},
	{"Arrows", "in conditional then", "const r = c ? (x: number): number => x : null", "const r = c ? (x) => x : null"},
	{"Arrows", "conditional not arrow", "const r = a ? (b) : c => d", "const r = a ? (b) : c => d"},
	{"Arrows", "conditional not arrow call", "const r = a ? (b) : (c)", "const r = a ? b : c"},
	{"Arrows", "parenthesised not arrow", "const r = (a)", "const r = a"},
	{"Arrows", "object return type", "const f = (): { a: number } => ({ a: 1 })", "const f = () => ({ a: 1 })"},
	{"Arrows", "body as object", "const f = () => ({} as Foo)", "const f = () => ({})"},
	{"Arrows", "default param", "const f = (a: number = 1, { b }: O = {}) => a", "const f = (a = 1, { b } = {}) => a"},

	// Expressions
	{"Expressions", "as", "const a = b as number", "const a = b"},
	{"Expressions", "as const", "const a = [1, 2] as const", "const a = [1, 2]"},
	{"Expressions", "as as", "const a = b as unknown as string", "const a = b"},
	{"Expressions", "as binary", "const a = (b + c) as number", "const a = b + c"},
	{"Expressions", "as precedence", "const a = b + c as number", "const a = b + c"},
	{"Expressions", "as in call", "f(x as any, y)", "f(x, y)"},
	{"Expressions", "as member", "(x as any).y", "x.y"},
	{"Expressions", "as assign target", "(x as any) = 1", "x = 1"},
	{"Expressions", "satisfies", "const c = { a: 1 } satisfies Config", "const c = { a: 1 }"},
	{"Expressions", "non-null", "const a = b!", "const a = b"},
	{"Expressions", "non-null member", "a!.b!.c", "a.b.c"},
	{"Expressions", "non-null call", "a!()", "a()"},
	{"Expressions", "non-null index", "a![0]", "a[0]"},
	{"Expressions", "non-null assign", "a! = 1", "a = 1"},
	{"Expressions", "non-null in chain", "a?.b!.c", "a?.b.c"},
	{"Expressions", "non-null division", "const r = a! / b", "const r = a / b"},
	{"Expressions", "as division", "const r = a as number / 2", "const r = a / 2"},
	{"Expressions", "as const division", "const r = a as const / 2", "const r = a / 2"},
	{"Expressions", "not equal stays", "const r = a !== b", "const r = a !== b"},
	{"Expressions", "call type args", "f<number>(1)", "f(1)"},
	{"Expressions", "call several type args", "f<A, B<C>>(x)", "f(x)"},
	{"Expressions", "type args shift", "f<Array<Array<T>>>(x)", "f(x)"},
	{"Expressions", "method type args", "a.b<T>(c)", "a.b(c)"},
	{"Expressions", "optional call type args", "a?.<T>(c)", "a?.(c)"},
	{"Expressions", "new type args", "const m = new Map<string, number>()", "const m = new Map()"},
	{"Expressions", "new type args no parens", "const m = new Foo<string>;", "const m = new Foo;"},
	{"Expressions", "tagged template type args", "tag<T>`x`", "tag`x`"},
	{"Expressions", "instantiation", "const g = f<string>;", "const g = f;"},
	{"Expressions", "comparisons stay", "const r = a < b && c > d", "const r = a < b && c > d"},
	{"Expressions", "comparison chain", "const r = a < b > c", "const r = a < b > c"},
	{"Expressions", "comparison plus", "const r = a < b > +c", "const r = a < b > +c"},
	{"Expressions", "shift stays", "const r = a << b >> c", "const r = a << b >> c"},
	{"Expressions", "template hole", "const s = `${a as string}!`", "const s = `${a}!`"},
	{"Expressions", "object method types", "const o = { m<T>(a: T): T { return a } }", "const o = { m(a) { return a } }"},
	{"Expressions", "object getter type", "const o = { get x(): number { return 1 } }", "const o = { get x() { return 1 } }"},
	{"Expressions", "class expression", "const C = class<T> implements I { x: T }", "const C = class { x }"},
	{"Expressions", "satisfies division", "const r = a satisfies number / 2", "const r = a / 2"},
	{"Expressions", "contextual type ident", "const type = 1; let interface2 = type", "const type = 1; let interface2 = type"},
	{"Expressions", "type as label", "type: for (;;) break type", "type: for (;;) break type"},
	{"Expressions", "declare as identifier", "declare(x)", "declare(x)"},
	{"Expressions", "abstract as identifier", "let abstract = 1; abstract = 2", "let abstract = 1; abstract = 2"},

	// Type language
	{"Types", "union", "let a: string | number | null", "let a"},
	{"Types", "leading union", "type A =\n  | 'a'\n  | 'b'", ""},
	{"Types", "intersection", "let a: A & B & { c: 1 }", "let a"},
	{"Types", "arrays", "let a: string[][]", "let a"},
	{"Types", "readonly array", "let a: readonly string[]", "let a"},
	{"Types", "tuple", "let a: [number, string?, ...boolean[]]", "let a"},
	{"Types", "named tuple", "let a: [x: number, y?: string, ...rest: number[]]", "let a"},
	{"Types", "function type", "let f: (a: number, ...b: string[]) => void", "let f"},
	{"Types", "generic function type", "let f: <T>(x: T) => T", "let f"},
	{"Types", "constructor type", "let c: new (x: number) => Foo", "let c"},
	{"Types", "abstract constructor", "let c: abstract new () => Foo", "let c"},
	{"Types", "object type", "let o: { a: number; b?: string; readonly c: boolean, [k: string]: unknown }", "let o"},
	{"Types", "object methods", "let o: { m(): void; n?<T>(x: T): T; (x: number): string; new (x: number): O; get g(): number; set s(v: number) }", "let o"},
	{"Types", "computed member", "let o: { [Symbol.iterator](): Iterator<number> }", "let o"},
	{"Types", "mapped", "type M<T> = { readonly [K in keyof T]?: T[K] }", ""},
	{"Types", "mapped modifiers", "type M<T> = { -readonly [K in keyof T]-?: T[K] }", ""},
	{"Types", "mapped as", "type M<T> = { [K in keyof T as `get${Capitalize<K & string>}`]: () => T[K] }", ""},
	{"Types", "conditional", "type C<T> = T extends string ? 'a' : T extends number ? 'b' : never", ""},
	{"Types", "infer", "type R<T> = T extends (...args: any[]) => infer U ? U : never", ""},
	{"Types", "function type in extends", "type Eq<T, U> = (<G>() => G extends T ? 1 : 2) extends <G>() => G extends U ? 1 : 2 ? true : false", ""},
	{"Types", "infer extends", "type F<T> = T extends [infer H extends string, ...infer R] ? H : never", ""},
	{"Types", "indexed", "type V = T['a']['b'][number]", ""},
	{"Types", "keyof typeof", "type K = keyof typeof obj", ""},
	{"Types", "typeof member", "let a: typeof b.c", "let a"},
	{"Types", "unique symbol", "declare const s: unique symbol", ""},
	{"Types", "literals", "let a: 'x' | 1 | -1 | true | 10n | null | undefined | void", "let a"},
	{"Types", "template literal", "type T = `a${string}b${number}`", ""},
	{"Types", "import type", "let a: import('./m').A<string>", "let a"},
	{"Types", "typeof import", "let a: typeof import('./m')", "let a"},
	{"Types", "qualified", "let a: A.B.C<D>", "let a"},
	{"Types", "parenthesized", "let a: (A | B)[]", "let a"},
	{"Types", "function in union", "let a: (() => void) | null", "let a"},
	{"Types", "this type", "interface I { m(): this }", ""},
	{"Types", "asserts", "function f(x: unknown): asserts x is string {}", "function f(x) {}"},
	{"Types", "asserts this", "class C { m(): asserts this {} }", "class C { m() {} }"},
	{"Types", "this predicate", "class C { m(): this is D { return true } }", "class C { m() { return true } }"},
	{"Types", "nested generic gt eq", "let a: Array<number>= []", "let a = []"},
	{"Types", "object literal key types", "let a: { 'q': 1; 2: 3 }", "let a"},
	{"Types", "const type param", "function f<const T extends readonly unknown[]>(x: T) {}", "function f(x) {}"},
	{"Types", "variance", "interface I<in out T> {}", ""},
	{"Types", "default type param", "type A<T = string> = T", ""},
	{"Types", "intrinsic keyword", "type U = intrinsic", ""},

	// Declarations
	{"Declarations", "interface", "interface A extends B, C<D> { a: number }", ""},
	{"Declarations", "interface with semicolon", "interface A {}\nlet x = 1", "let x = 1"},
	{"Declarations", "type alias", "type A<T> = { a: T }", ""},
	{"Declarations", "type then code", "type A = 1\nconst a = 1", "const a = 1"},
	{"Declarations", "declare const", "declare const x: number", ""},
	{"Declarations", "declare function", "declare function f(a: number): void", ""},
	{"Declarations", "declare class", "declare class C { m(): void; x: number }", ""},
	{"Declarations", "declare module", "declare module 'x' { export const a: number }", ""},
	{"Declarations", "declare module shorthand", "declare module 'x';", ""},
	{"Declarations", "declare global", "declare global { interface Window { a: number } }", ""},
	{"Declarations", "declare namespace", "declare namespace N { const x: number; function f(): void }", ""},
	{"Declarations", "declare enum", "declare enum E { A, B }", ""},
	{"Declarations", "declare const enum", "declare const enum E { A }", ""},
	{"Declarations", "type-only namespace", "namespace N { export type X = 1; export interface I {} }", ""},
	{"Declarations", "nested type namespace", "namespace A.B { export type X = 1 }", ""},
	{"Declarations", "overloads", "function f(a: string): void\nfunction f(a: number): void\nfunction f(a: any) {}", "function f(a) {}"},
	{"Declarations", "local type", "function f() { type L = 1; interface I {} return 1 }", "function f() { return 1 }"},
	{"Declarations", "block type", "if (a) { type T = 1 }", "if (a) {}"},

	// Classes
	{"Classes", "fields", "class C { x: number; y?: string; z!: boolean; w = 1 }", "class C { x; y; z; w = 1 }"},
	{"Classes", "declare field", "class C { declare x: number; y = 1 }", "class C { y = 1 }"},
	{"Classes", "modifiers", "class C { private x = 1; protected static readonly y = 2; public override m() {} }", "class C { x = 1; static y = 2; m() {} }"},
	{"Classes", "static modifier order", "class C { static private x = 1 }", "class C { static x = 1 }"},
	{"Classes", "abstract", "abstract class A { abstract m(): void; abstract x: number; n() {} }", "class A { n() {} }"},
	{"Classes", "export abstract", "export abstract class A {}", "export class A {}"},
	{"Classes", "export default abstract", "export default abstract class A {}", "export default class A {}"},
	{"Classes", "generics", "class C<T, U extends T = T> extends B<T> implements I<U>, J { m<V>(v: V): T { return v as any } }", "class C extends B { m(v) { return v } }"},
	{"Classes", "method overloads", "class C { m(a: string): void; m(a: number): void; m(a: any) {} }", "class C { m(a) {} }"},
	{"Classes", "index signature", "class C { [key: string]: unknown; static [k: number]: string; x = 1 }", "class C { x = 1 }"},
	{"Classes", "optional method", "class C { m?(): void; n?() {} }", "class C { n() {} }"},
	{"Classes", "accessors", "class C { get x(): number { return 1 } set x(v: number) {} }", "class C { get x() { return 1 } set x(v) {} }"},
	{"Classes", "private constructor", "class C { private constructor(a: number) {} }", "class C { constructor(a) {} }"},
	{"Classes", "private names", "class C { #x: number = 1; #m(): void {} }", "class C { #x = 1; #m() {} }"},
	{"Classes", "modifier names", "class C { readonly = 1; public() {} declare; abstract: number }", "class C { readonly = 1; public() {} declare; abstract }"},
	{"Classes", "static block", "class C { static { let x: number = 1 } }", "class C { static { let x = 1 } }"},
	{"Classes", "extends call", "class C extends mixin(A)<T> {}", "class C extends mixin(A) {}"},
	{"Classes", "async method", "class C { async m<T>(x: T): Promise<T> { return x } }", "class C { async m(x) { return x } }"},

	// Modules
	{"Modules", "import type", "import type { A } from './a'\nconst x = 1", "const x = 1"},
	{"Modules", "import type default", "import type A from './a'", ""},
	{"Modules", "import type namespace", "import type * as N from './a'", ""},
	{"Modules", "inline type", "import { type A, b } from './a'", "import { b } from './a'"},
	{"Modules", "only inline types", "import { type A } from './a'", "import './a'"},
	{"Modules", "type named type", "import { type } from './a'", "import { type } from './a'"},
	{"Modules", "type as x", "import { type as x } from './a'", "import { type as x } from './a'"},
	{"Modules", "type as as x", "import { type as as x } from './a'", "import './a'"},
	{"Modules", "type as as", "import { type as as } from './a'", "import { type as as } from './a'"},
	{"Modules", "type as", "import { type as } from './a'", "import './a'"},
	{"Modules", "default named type", "import type from './a'", "import type from './a'"},
	{"Modules", "type from from", "import type from from './a'", ""},
	{"Modules", "export type list", "export type { A, B as C }", ""},
	{"Modules", "export type from", "export type { A } from './a'", ""},
	{"Modules", "export type star", "export type * from './a'", ""},
	{"Modules", "export type star as", "export type * as N from './a'", ""},
	{"Modules", "export inline type", "export { type A, b }", "export { b }"},
	{"Modules", "export only inline type", "export { type A }", "export {}"},
	{"Modules", "export interface", "export interface A {}", ""},
	{"Modules", "export type alias", "export type A = 1", ""},
	{"Modules", "export declare", "export declare const a: number", ""},
	{"Modules", "export default interface", "export default interface A {}", ""},
	{"Modules", "export overload", "export function f(a: string): void\nexport function f(a: any) {}", "export function f(a) {}"},
	{"Modules", "export default overload", "export default function f(a: string): void\nexport default function f(a: any) {}", "export default function f(a) {}"},
	{"Modules", "export namespace types", "export namespace N { export type A = 1 }", ""},
	{"Modules", "export as namespace", "export as namespace Lib", ""},
	{"Modules", "export let typed", "export let title: string = 'Untitled'", "export let title = 'Untitled'"},
	{"Modules", "export const enum declare", "export declare const enum E { A }", ""},
}

var tsInvalidCases = []struct {
	name, ts string
	code     DiagCode // "" for a syntax error
}{
	{"enum", "enum E { A, B = 2 }", DiagTSNonErasable},
	{"const enum", "const enum E { A }", DiagTSNonErasable},
	{"export enum", "export enum E { A }", DiagTSNonErasable},
	{"value namespace", "namespace N { export const x = 1 }", DiagTSNonErasable},
	{"parameter property", "class C { constructor(private x: number) {} }", DiagTSNonErasable},
	{"readonly parameter property", "class C { constructor(readonly x: number) {} }", DiagTSNonErasable},
	{"import equals require", "import fs = require('fs')", DiagTSNonErasable},
	{"import equals alias", "import X = N.Y", DiagTSNonErasable},
	{"export equals", "export = x", DiagTSNonErasable},
	{"export import", "export import X = N.Y", DiagTSNonErasable},
	{"angle assertion", "const a = <string>b", DiagTSNonErasable},
	{"missing type", "let x: = 1", ""},
	{"unclosed type args", "let x: Array<number = 1", ""},
	{"empty type args", "f<>()", ""},
	{"decorator", "@dec class C {}", ""},
	{"type and default", "import type A, { B } from './a'", ""},
	{"abstract with body", "abstract class A { abstract m() {} }", ""},
	{"ambient body", "declare function f() {}", ""},
	{"signature initializer", "let f: (a = 1) => void", ""},
	{"quoted module without declare", "module 'x' {}", ""},
}

// tsParse parses src as TypeScript with the corpus watchdog.
func tsParse(src string) ctParsed {
	done := make(chan ctParsed, 1)
	go func() {
		var r ctParsed
		defer func() {
			if e := recover(); e != nil {
				r.panic = e
			}
			done <- r
		}()
		r.prog, r.diags = ParseWithOptions(src, ParseOptions{TypeScript: true})
	}()
	return <-done
}

func TestTSCorpusValid(t *testing.T) {
	for _, c := range tsValidCases {
		t.Run(c.group+"/"+c.name, func(t *testing.T) {
			got := tsParse(c.ts)
			if got.panic != nil {
				t.Fatalf("panic: %v", got.panic)
			}
			for _, d := range got.diags {
				t.Errorf("diagnostic @%d: %s (%s)", d.Pos, d.Message, d.Code)
			}
			for _, bad := range ctInvariants(c.ts, got.prog) {
				t.Errorf("invariant: %s", bad)
			}
			for _, u := range ctUnknownNodes(got.prog) {
				t.Errorf("placeholder node %q", u)
			}
			want := ctParse(c.js)
			if len(want.diags) > 0 {
				t.Fatalf("the expected JavaScript does not parse: %v", want.diags)
			}
			if g, w := ctDump(got.prog), ctDump(want.prog); g != w {
				t.Errorf("tree mismatch\n  ts:   %q\n  got:  %s\n  want: %s", c.ts, g, w)
			}
			for _, mode := range []PrintMode{PrintPretty, PrintMinified} {
				printed := PrintJS(got.prog, mode)
				again := ctParse(printed)
				if len(again.diags) > 0 {
					t.Errorf("printed JavaScript does not parse: %q %v", printed, again.diags)
					continue
				}
				if g, w := ctDump(again.prog), ctDump(want.prog); g != w {
					t.Errorf("printed JavaScript changes the tree: %q", printed)
				}
			}
		})
	}
}

func TestTSCorpusInvalid(t *testing.T) {
	for _, c := range tsInvalidCases {
		t.Run(c.name, func(t *testing.T) {
			got := tsParse(c.ts)
			if got.panic != nil {
				t.Fatalf("panic: %v", got.panic)
			}
			if len(got.diags) == 0 {
				t.Fatalf("no diagnostic for %q", c.ts)
			}
			if c.code != "" {
				found := false
				for _, d := range got.diags {
					found = found || d.Code == c.code
				}
				if !found {
					t.Errorf("want a %s diagnostic, got %v", c.code, got.diags)
				}
			}
			// The tree stays printable: recovery leaves no placeholders
			// behind for erased constructs.
			for _, bad := range ctInvariants(c.ts, got.prog) {
				t.Errorf("invariant: %s", bad)
			}
		})
	}
}

// The JavaScript corpus parses to the same trees with TypeScript on, except
// where TypeScript itself reads the source differently.
func TestTSAcceptsJavaScriptCorpus(t *testing.T) {
	differs := map[string]bool{
		// `a < b > (c)` is a call with a type argument in TypeScript.
		"f < x > (y)": true,
	}
	n := 0
	for _, src := range printerRoundTripSources() {
		js := ctParse(src)
		if js.panic != nil || js.hang || len(js.diags) > 0 || differs[src] {
			continue
		}
		ts := tsParse(src)
		if ts.panic != nil {
			t.Errorf("panic on %q: %v", src, ts.panic)
			continue
		}
		if len(ts.diags) > 0 {
			t.Errorf("TypeScript rejects valid JavaScript %q: %v", src, ts.diags)
			continue
		}
		if g, w := ctDump(ts.prog), ctDump(js.prog); g != w {
			t.Errorf("TypeScript reads %q differently\n  ts: %s\n  js: %s", src, g, w)
		}
		n++
	}
	if n < 100 {
		t.Fatalf("only %d JavaScript sources checked", n)
	}
}

func TestTSInfoRecordsDeclarationsAndImports(t *testing.T) {
	prog, diags := ParseWithOptions(strings.Join([]string{
		"import type { A } from './a'",
		"import { type B, c } from './b'",
		"export interface I<T> extends A { x: T; y: B }",
		"type L = typeof c",
		"export declare const d: number",
		"function f(x: Q): x is R { return true }",
		"export type { L as M }",
	}, "\n"), ParseOptions{TypeScript: true})
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	ts := prog.TS
	if ts == nil {
		t.Fatal("no TS info")
	}
	var imports []string
	for _, im := range ts.Imports {
		imports = append(imports, fmt.Sprintf("%s=%s:%s/%v", im.Local, im.Imported, im.Source, im.Statement))
	}
	if got := strings.Join(imports, " "); got != "A=A:./a/true B=B:./b/false" {
		t.Errorf("imports: %s", got)
	}
	var decls []string
	for _, d := range ts.Decls {
		decls = append(decls, fmt.Sprintf("%d:%s/%v", d.Kind, d.Name, d.Exported))
	}
	if got := strings.Join(decls, " "); got != "0:I/true 1:L/false 4:d/true 8:/true" {
		t.Errorf("decls: %s", got)
	}
	var refs []string
	for _, r := range ts.Refs {
		refs = append(refs, fmt.Sprintf("%s/%v", r.Name, r.Query))
	}
	// T is the interface's type parameter: not a reference.
	if got := strings.Join(refs, " "); got != "A/false B/false c/true Q/false R/false" {
		t.Errorf("refs: %s", got)
	}
	// The kept function carries its signature.
	fn := prog.Body[len(prog.Body)-1].(*JsFuncDecl)
	if fn.TS == nil || fn.TS.Params[0].Type.Name != "Q" || fn.TS.Return.Kind != TSPredicate {
		t.Errorf("signature not recorded: %+v", fn.TS)
	}
}

func TestTSAnnotationsOnNodes(t *testing.T) {
	prog, diags := ParseWithOptions("export let title: string = 'x'\nlet v: Array<number>\nclass C { x?: number }", ParseOptions{TypeScript: true})
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	decl := prog.Body[0].(*JsExportDecl).Declaration.(*JsVarDecl).Decls[0]
	if decl.Type == nil || decl.Type.Kind != TSKeyword || decl.Type.Name != "string" {
		t.Errorf("export let type: %+v", decl.Type)
	}
	if got := "export let title: string = 'x'"[decl.Type.Pos:decl.Type.End]; got != "string" {
		t.Errorf("type span %q", got)
	}
	arr := prog.Body[1].(*JsVarDecl).Decls[0].Type
	if arr.Kind != TSRefType || arr.Name != "Array" || len(arr.Args) != 1 || arr.Args[0].Name != "number" {
		t.Errorf("Array<number>: %+v", arr)
	}
	member := prog.Body[2].(*JsClassDecl).Members[0]
	if member.TS == nil || !member.TS.Optional || member.TS.Type.Name != "number" {
		t.Errorf("class member: %+v", member.TS)
	}
}

func TestDetectTypeScript(t *testing.T) {
	src := "let a = 1\nexport let title: string = 'x'"
	if _, diags := ParseJSWithDiagnostics(src); len(diags) == 0 {
		t.Fatal("JavaScript accepted a type annotation")
	}
	pos, ok := DetectTypeScript(src)
	if !ok || src[pos:pos+6] != "string" {
		t.Errorf("DetectTypeScript = %d, %v", pos, ok)
	}
	if _, ok := DetectTypeScript("let a = ;"); ok {
		t.Error("broken source detected as TypeScript")
	}
	if _, ok := DetectTypeScript("let a = 1"); ok {
		t.Error("plain JavaScript detected as TypeScript")
	}
}

func TestTSDoesNotChangeJavaScriptMode(t *testing.T) {
	for _, src := range []string{"let x: number = 1", "f<T>(x)", "a!", "interface A {}", "x as T"} {
		_, diags := ParseJSWithDiagnostics(src)
		_, tsDiags := ParseWithOptions(src, ParseOptions{TypeScript: true})
		if src != "f<T>(x)" && src != "interface A {}" && len(diags) == 0 {
			t.Errorf("JavaScript mode accepted %q", src)
		}
		if len(tsDiags) > 0 {
			t.Errorf("TypeScript mode rejected %q: %v", src, tsDiags)
		}
	}
}
