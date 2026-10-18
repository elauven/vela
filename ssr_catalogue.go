package main

import (
	"fmt"
	"strings"
)

// The built-in rows of docs/SSR_TRANSLATION_MATRIX.md §2 (V-11.1a). The
// matrix section is generated from this table (TestSSRMatrixBuiltins,
// VELA_UPDATE_MATRIX=1); Names ties a row to the admitted catalogue in
// ssr_builtins.go, and Oracles to the groups of
// testdata/ssr_expression_oracle.json.

type ssrCatalogueRow struct {
	Builtin, Helper, Rule, Divergence, Status string
	Names                                     []string
	Oracles                                   []string
}

type ssrCatalogueSection struct {
	Title string
	Intro string
	Rows  []ssrCatalogueRow
}

var ssrCatalogue = []ssrCatalogueSection{
	{Title: "2.1 Conversions and global functions", Rows: []ssrCatalogueRow{
		{Builtin: "`String(x)`", Helper: "`vela.ToStringValue`", Rule: "T-6.4a", Divergence: "`String()` is `\"\"`; arrays join; `-0` is `\"0\"`.", Status: "implemented", Names: []string{"String"}, Oracles: []string{"conv-call"}},
		{Builtin: "`Number(x)`", Helper: "`vela.ToNumber`", Rule: "§6.2", Divergence: "`Number(\"\")` is `0`, `Number(\" 12 \")` is `12`, `Number(\"0x1f\")` is `31`, `Number(\"1_0\")` is `NaN` ✓; `strconv.ParseFloat` differs on all four.", Status: "implemented", Names: []string{"Number"}, Oracles: []string{"conv-call"}},
		{Builtin: "`Boolean(x)`", Helper: "`vela.ToBoolean`", Rule: "§6.2", Status: "implemented", Names: []string{"Boolean"}, Oracles: []string{"conv-call"}},
		{Builtin: "`parseInt(s, radix)`", Helper: "`vela.ParseInt`", Rule: "—", Divergence: "Parses a prefix: `parseInt(\"12px\")` is `12` ✓, while `strconv.ParseInt` fails; `0x` prefix with radix 16 or absent; radix outside 2–36 is `NaN`.", Status: "implemented", Names: []string{"parseInt", "Number.parseInt"}, Oracles: []string{"parseInt"}},
		{Builtin: "`parseFloat(s)`", Helper: "`vela.ParseFloat`", Rule: "—", Divergence: "Prefix parsing; `Infinity` literal.", Status: "implemented", Names: []string{"parseFloat", "Number.parseFloat"}, Oracles: []string{"parseFloat"}},
		{Builtin: "`isNaN`, `isFinite`", Helper: "`vela.IsNaN`, `vela.IsFinite`", Rule: "—", Divergence: "Coerce with `ToNumber` first.", Status: "implemented", Names: []string{"isNaN", "isFinite"}, Oracles: []string{"isnan"}},
		{Builtin: "`Number.isNaN`, `Number.isFinite`, `Number.isInteger`, `Number.isSafeInteger`", Helper: "`vela.Number…`", Rule: "—", Divergence: "No coercion.", Status: "implemented", Names: []string{"Number.isNaN", "Number.isFinite", "Number.isInteger", "Number.isSafeInteger"}, Oracles: []string{"isnan"}},
		{Builtin: "`encodeURIComponent`, `encodeURI`", Helper: "`ssrJS.EncodeURIComponent`, `ssrJS.EncodeURI`", Rule: "T-6.5a", Divergence: "JS keeps `!*'()` unescaped and encodes space as `%20` ✓; `url.QueryEscape` escapes them and writes `+`, `url.PathEscape` escapes them too. A lone surrogate throws `URIError`.", Status: "implemented", Names: []string{"encodeURIComponent", "encodeURI"}, Oracles: []string{"uri"}},
		{Builtin: "`decodeURIComponent`, `decodeURI`", Helper: "`ssrJS.DecodeURI…`", Rule: "T-6.5a", Divergence: "Malformed input throws `URIError`; `decodeURI` keeps escapes of reserved characters.", Status: "implemented", Names: []string{"decodeURIComponent", "decodeURI"}, Oracles: []string{"uri"}},
		{Builtin: "global functions as values (`items.filter(Boolean)`)", Helper: "`ssrJS.GlobalFunc`", Rule: "§7.1", Status: "implemented", Oracles: []string{"global-values"}},
		{Builtin: "`NaN`, `Infinity`", Helper: "`vela.ToNumber(\"NaN\")`", Rule: "§6.2", Status: "implemented", Oracles: []string{"math-const"}},
		{Builtin: "`Array.isArray`", Helper: "`vela.IsArray`", Rule: "—", Status: "implemented", Names: []string{"Array.isArray"}, Oracles: []string{"isarray"}},
		{Builtin: "`console.log`, `info`, `warn`, `error`, `debug`", Helper: "`ssrJS.Console`", Rule: "T-6.6a", Divergence: "Calls go to the observer of `vela.WithConsoleObserver(ctx, f)`; without one they do nothing (decision of §5).", Status: "implemented", Oracles: []string{"console"}},
	}},
	{Title: "2.2 String methods", Intro: "Method calls dispatch on the receiver at run time (`ssrJS.Method`), so the methods shared by strings and arrays (`at`, `includes`, `indexOf`, `lastIndexOf`, `slice`, `concat`) need no type proof.", Rows: []ssrCatalogueRow{
		{Builtin: "`length`", Helper: "`vela.Length`", Rule: "T-6.3a", Divergence: "UTF-16: `\"😀\".length` is `2` ✓.", Status: "implemented", Oracles: []string{"strings-length"}},
		{Builtin: "`at`, `charAt`, `charCodeAt`, `codePointAt`", Helper: "`ssrJS.Method`", Rule: "T-6.3a", Divergence: "UTF-16 indices; negative index for `at`.", Status: "implemented", Names: []string{".at", ".charAt", ".charCodeAt", ".codePointAt"}, Oracles: []string{"str-index"}},
		{Builtin: "`includes`, `startsWith`, `endsWith`, `indexOf`, `lastIndexOf`", Helper: "`ssrJS.Method`", Rule: "T-6.3a", Divergence: "Positions in UTF-16 units; `position` argument clamping.", Status: "implemented", Names: []string{".includes", ".startsWith", ".endsWith", ".indexOf", ".lastIndexOf"}, Oracles: []string{"str-search"}},
		{Builtin: "`slice`, `substring`", Helper: "`ssrJS.Method`", Rule: "T-6.3a", Divergence: "`substring` swaps reversed arguments; `slice` accepts negatives; cutting a pair yields a lone surrogate (T-6.3b), kept in WTF-8 so that joining the halves gives the pair back.", Status: "implemented", Names: []string{".slice", ".substring"}, Oracles: []string{"str-slice"}},
		{Builtin: "`toUpperCase`, `toLowerCase`", Helper: "`ssrJS.Method` (`casing_table.go`)", Rule: "T-6.3c", Divergence: "Full SpecialCasing from a table generated with Node (`tools/gen-casing-table.mjs`): `\"straße\".toUpperCase()` is `\"STRASSE\"` ✓ (Go `STRAßE`); context-sensitive final sigma: `\"ΣΑΣ\".toLowerCase()` is `\"σας\"` ✓ (Go `σασ`).", Status: "implemented", Names: []string{".toUpperCase", ".toLowerCase"}, Oracles: []string{"str-case"}},
		{Builtin: "`trim`, `trimStart`, `trimEnd`", Helper: "`ssrJS.Method`", Rule: "—", Divergence: "JS whitespace set: U+FEFF is trimmed by JS but not by `strings.TrimSpace` ✓.", Status: "implemented", Names: []string{".trim", ".trimStart", ".trimEnd"}, Oracles: []string{"str-trim"}},
		{Builtin: "`padStart`, `padEnd`", Helper: "`ssrJS.Method`", Rule: "T-6.3a", Divergence: "Target length in UTF-16 units; pad string truncated.", Status: "implemented", Names: []string{".padStart", ".padEnd"}, Oracles: []string{"str-pad"}},
		{Builtin: "`repeat`", Helper: "`ssrJS.Method`", Rule: "T-6.5a", Divergence: "Negative or infinite count throws `RangeError`.", Status: "implemented", Names: []string{".repeat"}, Oracles: []string{"str-repeat"}},
		{Builtin: "`split(separator, limit)` with a string separator", Helper: "`ssrJS.Method`", Rule: "T-6.3a", Divergence: "`split(\"\")` splits into UTF-16 units and breaks surrogate pairs: `\"😀\".split(\"\").length` is `2` ✓.", Status: "implemented", Names: []string{".split"}, Oracles: []string{"str-split"}},
		{Builtin: "`replace(string, string)`, `replaceAll(string, string)`", Helper: "`ssrJS.Method`", Rule: "—", Divergence: "`replace` changes the first match only; replacement patterns `$&`, `$$`, `` $` ``, `$'` apply even with a string pattern: `\"a\".replace(\"a\", \"$&$&\")` is `\"aa\"` ✓.", Status: "implemented", Names: []string{".replace", ".replaceAll"}, Oracles: []string{"str-replace"}},
		{Builtin: "`replace` / `replaceAll` with a callback", Helper: "`ssrJS.Method`", Rule: "T-7.2b", Divergence: "Callback receives match, offset, string.", Status: "implemented", Oracles: []string{"str-replace"}},
		{Builtin: "`concat`, `toString`, `valueOf`", Helper: "`ssrJS.Method`", Rule: "—", Status: "implemented", Names: []string{".concat", ".toString", ".valueOf"}, Oracles: []string{"str-concat", "num-tostring"}},
		{Builtin: "spread / `for…of` over a string", Helper: "code-point iteration", Rule: "T-6.3a", Divergence: "`[...\"😀\"].length` is `1` ✓.", Status: "implemented", Oracles: []string{"arrays-spread"}},
		{Builtin: "`normalize(form)`", Helper: "`ssrJS.Method` (`golang.org/x/text/unicode/norm`)", Rule: "T-6.5a", Divergence: "NFC, NFD, NFKC, NFKD; another form throws `RangeError`. Unicode 17 tables, as in Node 24 (decision of §5).", Status: "implemented", Names: []string{".normalize"}, Oracles: []string{"str-normalize"}},
		{Builtin: "`localeCompare`, `toLocaleUpperCase`, `toLocaleLowerCase`", Helper: "host", Rule: "§3", Divergence: "Locale-dependent.", Status: "never"},
		{Builtin: "`match`, `matchAll`, `search`, `replace`/`split` with a RegExp", Helper: "host", Rule: "§3", Status: "never"},
	}},
	{Title: "2.3 Array methods", Rows: []ssrCatalogueRow{
		{Builtin: "`length`", Helper: "`vela.Length`", Rule: "—", Status: "implemented", Oracles: []string{"strings-length"}},
		{Builtin: "holes `[1, , 3]`", Helper: "`vela.Hole()`", Rule: "§6.2", Divergence: "A hole reads as `undefined`; `map` keeps it, `filter`/`forEach`/`some`/`every`/`reduce`/`flat` skip it, `find*` visit it as `undefined`.", Status: "implemented", Oracles: []string{"holes", "arr-callbacks"}},
		{Builtin: "`at`", Helper: "`ssrJS.Method`", Rule: "—", Divergence: "Negative index.", Status: "implemented", Oracles: []string{"arr-index"}},
		{Builtin: "`includes`", Helper: "`ssrJS.Method`", Rule: "—", Divergence: "SameValueZero: `[NaN].includes(NaN)` is `true` ✓.", Status: "implemented", Oracles: []string{"arr-index"}},
		{Builtin: "`indexOf`, `lastIndexOf`", Helper: "`ssrJS.Method`", Rule: "—", Divergence: "Strict equality: `[NaN].indexOf(NaN)` is `-1` ✓.", Status: "implemented", Oracles: []string{"arr-index"}},
		{Builtin: "`join`", Helper: "`ssrJS.Method`", Rule: "—", Divergence: "Default separator `,`; `null`/`undefined`/holes become empty.", Status: "implemented", Names: []string{".join"}, Oracles: []string{"arr-join"}},
		{Builtin: "`slice`, `concat`", Helper: "`ssrJS.Method`", Rule: "—", Divergence: "`concat` flattens array arguments one level.", Status: "implemented", Oracles: []string{"arr-slice"}},
		{Builtin: "`map`, `filter`, `forEach`, `some`, `every`, `find`, `findIndex`, `findLast`, `findLastIndex`", Helper: "`ssrJS.Method` with `vela.Func` callbacks", Rule: "T-7.2b", Divergence: "Callbacks receive `(item, index, array)`; `map`/`filter`/`forEach`/`some`/`every` skip holes: `[3,,1].map(x => x*2)` keeps the hole ✓; `find*` visit holes as `undefined`.", Status: "implemented", Names: []string{".map", ".filter", ".forEach", ".some", ".every", ".find", ".findIndex", ".findLast", ".findLastIndex"}, Oracles: []string{"arr-callbacks"}},
		{Builtin: "`reduce`, `reduceRight`", Helper: "`ssrJS.Method`", Rule: "T-6.5a", Divergence: "Empty array without initial value throws `TypeError`; holes skipped.", Status: "implemented", Names: []string{".reduce", ".reduceRight"}, Oracles: []string{"arr-reduce"}},
		{Builtin: "`flat(depth)`, `flatMap`", Helper: "`ssrJS.Method`", Rule: "—", Divergence: "Default depth 1; holes removed.", Status: "implemented", Names: []string{".flat", ".flatMap"}, Oracles: []string{"arr-flat"}},
		{Builtin: "`sort`, `toSorted`", Helper: "`ssrJS.Method`, `ssrJS.MutateArray`", Rule: "T-6.3a, T-6.6b", Divergence: "Stable. Without comparator: compares `String(x)` by UTF-16 units and places `undefined` last, holes after it: `[10,9,undefined,1].sort()` is `[1,10,9,undefined]` ✓. Comparator result `NaN` counts as `0`. `sort` only on arrays the function created.", Status: "implemented", Names: []string{".sort", ".toSorted"}, Oracles: []string{"arr-sort", "arr-mutate"}},
		{Builtin: "`reverse`, `toReversed`", Helper: "`ssrJS.Method`, `ssrJS.MutateArray`", Rule: "T-6.6b", Divergence: "`reverse` mutates: only on arrays the function created.", Status: "implemented", Names: []string{".reverse", ".toReversed"}, Oracles: []string{"arr-reverse", "arr-mutate"}},
		{Builtin: "`push`, `pop`, `shift`, `unshift`, `splice`, `fill`", Helper: "`ssrJS.MutateArray`", Rule: "T-6.6b", Divergence: "As a statement, a `const`/`let` initializer, or the right-hand side of an assignment, on a local array the function created; methods that change the length also need the array not to be copied into another variable, literal, or call argument. Otherwise `VELA-SSR-IMPURE`.", Status: "implemented", Names: []string{".push", ".pop", ".shift", ".unshift", ".splice", ".fill"}, Oracles: []string{"arr-mutate"}},
		{Builtin: "`entries`, `keys`, `values` in `for…of`", Helper: "index loop", Rule: "§7.1", Divergence: "Only as `for…of` sources in v1.", Status: "planned (task 056)"},
		{Builtin: "`Array.from(arrayLike, mapFn)`", Helper: "`ssrJS.ArrayFrom`", Rule: "—", Divergence: "Strings by code point; `{length: n}`; holes become `undefined`.", Status: "implemented", Names: []string{"Array.from"}, Oracles: []string{"arr-from"}},
		{Builtin: "`Array.of`", Helper: "`vela.ArrayOf`", Rule: "—", Status: "implemented", Names: []string{"Array.of"}, Oracles: []string{"arr-from"}},
	}},
	{Title: "2.4 Object functions", Rows: []ssrCatalogueRow{
		{Builtin: "`Object.keys`, `Object.values`, `Object.entries`", Helper: "`ssrJS.ObjectKeys` …", Rule: "T-5.1c", Divergence: "Integer-like keys first, ascending; then insertion or JSON order. `encoding/json` writes `\"10\"` before `\"2\"`, JS enumerates `\"2\"` first ✓. A Go map has no insertion order: its keys follow JSON order.", Status: "implemented", Names: []string{"Object.keys", "Object.values", "Object.entries"}, Oracles: []string{"obj-keys"}},
		{Builtin: "`Object.fromEntries`", Helper: "`ssrJS.ObjectFromEntries`", Rule: "T-5.1c", Divergence: "Later duplicate keys win but keep the first position.", Status: "implemented", Names: []string{"Object.fromEntries"}, Oracles: []string{"obj-from"}},
		{Builtin: "`Object.assign`", Helper: "`ssrJS.ObjectAssign`", Rule: "T-6.6b", Divergence: "The target must be an object literal: `Object.assign({}, a, b)`.", Status: "implemented", Names: []string{"Object.assign"}, Oracles: []string{"obj-assign"}},
		{Builtin: "`Object.hasOwn`", Helper: "`ssrJS.ObjectHasOwn`", Rule: "—", Status: "implemented", Names: []string{"Object.hasOwn"}, Oracles: []string{"obj-hasown"}},
		{Builtin: "`Object.freeze`, `Object.isFrozen`", Helper: "`vela.ObjectFreeze`, `ssrJS.ObjectIsFrozen`", Rule: "T-6.6b", Divergence: "A frozen object throws `TypeError` on writes, as in strict-mode JavaScript; freezing is shallow. An array is returned unchanged and `Object.isFrozen` rejects arrays (at build time when the type is proven, else at run time): a Go slice has no identity to mark (decision of §5).", Status: "implemented", Names: []string{"Object.freeze", "Object.isFrozen"}, Oracles: []string{"obj-freeze"}},
	}},
	{Title: "2.5 Math", Rows: []ssrCatalogueRow{
		{Builtin: "`Math.round`", Helper: "`vela.MathRound`", Rule: "T-6.4b", Divergence: "Ties round toward +∞: `Math.round(-2.5)` is `-2` ✓, `math.Round` gives `-3`. `-0` preserved; `Math.round(0.49999999999999994)` is `0`.", Status: "implemented", Names: []string{"Math.round"}, Oracles: []string{"math-round"}},
		{Builtin: "`Math.floor`, `Math.ceil`, `Math.trunc`, `Math.abs`, `Math.sign`", Helper: "`vela.Math…`", Rule: "—", Divergence: "`Math.sign(-0)` is `-0`.", Status: "implemented", Names: []string{"Math.floor", "Math.ceil", "Math.trunc", "Math.abs", "Math.sign"}, Oracles: []string{"math-basic"}},
		{Builtin: "`Math.min`, `Math.max`", Helper: "`vela.MathMin`, `vela.MathMax`", Rule: "—", Divergence: "No arguments: `Infinity` / `-Infinity` ✓; any `NaN` gives `NaN`; `-0 < +0` ✓.", Status: "implemented", Names: []string{"Math.min", "Math.max"}, Oracles: []string{"math-minmax", "spread-args"}},
		{Builtin: "`Math.pow`, `**`", Helper: "`vela.Pow`", Rule: "—", Divergence: "`Math.pow(1, Infinity)` is `NaN` ✓, `math.Pow` gives `1`; `pow(NaN, 0)` is `1`. Results are correctly rounded (integer exponents exactly, others in 256-bit arithmetic); `math.Pow` differs from V8 in about 40% of non-integer cases. V8 uses the platform libm, which is one unit in the last place off in rare cases next to a rounding boundary, such as `pow(1e21, 10)` (`TestFdlibmMatchesNode`, `testdata/math_fixture.json`).", Status: "implemented", Names: []string{"Math.pow"}, Oracles: []string{"math-pow"}},
		{Builtin: "`Math.sqrt`, `Math.cbrt`, `Math.hypot`", Helper: "`vela.Math…`", Rule: "—", Divergence: "`Math.hypot(NaN, Infinity)` is `Infinity`; `hypot` uses V8's scaled, compensated sum; `cbrt` is the FreeBSD algorithm V8 uses and matches it bit for bit, where `math.Cbrt` differs in about 10% of cases.", Status: "implemented", Names: []string{"Math.sqrt", "Math.cbrt", "Math.hypot"}, Oracles: []string{"math-pow"}},
		{Builtin: "`Math.log`, `log2`, `log10`, `log1p`, `exp`, `expm1`, `sin`, `cos`, `tan`, `asin`, `acos`, `atan`, `atan2`, `sinh`, `cosh`, `tanh`, `asinh`, `acosh`, `atanh`", Helper: "`vela.Math…` (`fdlibm_math.go`)", Rule: "—", Divergence: "Ports of the fdlibm functions V8 uses, bit for bit with V8 (`TestTranscendentalMatchesNode`, `testdata/math_fixture.json`), including V8's `exp(1) = E` special case. Go's `math` differs: wrong results for logarithms of subnormal numbers and `log2` near 1, up to 11 ulp for large trigonometric arguments (decision of §5).", Status: "implemented", Names: []string{"Math.log", "Math.log2", "Math.log10", "Math.log1p", "Math.exp", "Math.expm1", "Math.sin", "Math.cos", "Math.tan", "Math.asin", "Math.acos", "Math.atan", "Math.atan2", "Math.sinh", "Math.cosh", "Math.tanh", "Math.asinh", "Math.acosh", "Math.atanh"}, Oracles: []string{"math-transcendental"}},
		{Builtin: "`Math.fround`, `Math.clz32`, `Math.imul`", Helper: "`vela.Math…`", Rule: "—", Status: "implemented", Names: []string{"Math.fround", "Math.clz32", "Math.imul"}, Oracles: []string{"math-exact"}},
		{Builtin: "`Math.PI`, `Math.E`, other constants", Helper: "Go constants", Rule: "—", Status: "implemented", Names: []string{"Math.PI", "Math.E", "Math.LN2", "Math.LN10", "Math.LOG2E", "Math.LOG10E", "Math.SQRT2", "Math.SQRT1_2"}, Oracles: []string{"math-const"}},
		{Builtin: "`Math.random`", Helper: "host / client", Rule: "T-6.6c", Status: "never"},
	}},
	{Title: "2.6 Number methods", Rows: []ssrCatalogueRow{
		{Builtin: "`toString()` (radix 10), text conversion", Helper: "`ssrJS.Method`", Rule: "T-6.4a", Divergence: "Exponent from 1e21; `-0` prints `0`.", Status: "implemented", Oracles: []string{"num-tostring"}},
		{Builtin: "`toFixed(d)`", Helper: "`ssrJS.Method`", Rule: "T-6.4b", Divergence: "Exact ties round up: `(2.5).toFixed(0)` is `\"3\"`, `strconv` gives `\"2\"`; non-ties agree (`(8.345).toFixed(2)` is `\"8.35\"` in both ✓); values ≥ 1e21 use `toString` (`\"1e+21\"` ✓); `(-0.0001).toFixed(2)` is `\"-0.00\"`; `d > 100` throws `RangeError`.", Status: "implemented", Names: []string{".toFixed"}, Oracles: []string{"num-tofixed"}},
		{Builtin: "`toString(radix)`", Helper: "`ssrJS.Method`", Rule: "T-6.4a", Divergence: "Port of V8's `DoubleToRadixCString`: fraction digits up to the precision of the double, rounded to even (`TestTranscendentalMatchesNode` compares 1500 values); radix outside 2–36 throws `RangeError` (decision of §5).", Status: "implemented", Oracles: []string{"num-radix"}},
		{Builtin: "`toPrecision(p)`, `toExponential(f)`", Helper: "`ssrJS.Method`", Rule: "T-6.4b", Divergence: "Exact decimal rounding of the binary value, ties up: `(2.5).toPrecision(1)` is `\"3\"`; `toExponential()` uses the shortest digits; out-of-range arguments throw `RangeError` (decision of §5).", Status: "implemented", Names: []string{".toPrecision", ".toExponential"}, Oracles: []string{"num-precision", "num-exponential"}},
		{Builtin: "`Number.MAX_SAFE_INTEGER`, `MIN_SAFE_INTEGER`, `EPSILON`, `MAX_VALUE`, `MIN_VALUE`, `NaN`, `POSITIVE_INFINITY`, `NEGATIVE_INFINITY`", Helper: "Go constants", Rule: "—", Status: "implemented", Names: []string{"Number.MAX_SAFE_INTEGER", "Number.MIN_SAFE_INTEGER", "Number.EPSILON", "Number.MAX_VALUE", "Number.MIN_VALUE", "Number.NaN", "Number.POSITIVE_INFINITY", "Number.NEGATIVE_INFINITY"}, Oracles: []string{"math-const"}},
		{Builtin: "`toLocaleString`", Helper: "host", Rule: "§3", Status: "never"},
	}},
	{Title: "2.7 JSON", Rows: []ssrCatalogueRow{
		{Builtin: "`JSON.stringify(v)`, `JSON.stringify(v, null, indent)`", Helper: "`ssrJS.JSONStringify`", Rule: "T-5.1c, T-6.4a", Divergence: "JS does not escape `<`, `>`, `&`; `undefined` members and functions omitted, `NaN`/`Infinity` become `null`, `-0` becomes `0` ✓; lone surrogates are `\\u` escapes; key order per T-5.1c; cycles throw `TypeError`.", Status: "implemented", Names: []string{"JSON.stringify"}, Oracles: []string{"json-stringify"}},
		{Builtin: "`JSON.stringify` with replacer, `toJSON` methods", Helper: "—", Rule: "—", Divergence: "Not in v1.", Status: "never"},
		{Builtin: "`JSON.parse(s)`", Helper: "`ssrJS.JSONParse`", Rule: "T-6.5a", Divergence: "Result in the JSON value domain with `*vela.Object`; invalid input throws `SyntaxError` (message text differs from V8).", Status: "implemented", Names: []string{"JSON.parse"}, Oracles: []string{"json-parse"}},
	}},
	{Title: "2.8 Promise", Rows: []ssrCatalogueRow{
		{Builtin: "`Promise.resolve`, `Promise.reject`, `Promise.all`, `Promise.allSettled`, `.then`, `.catch`, `.finally`", Helper: "`vela.Resolve`, `vela.Reject`, `ssrJS.All`, `ssrJS.AllSettled`, `ssrJS.Then` …", Rule: "T-7.4b", Divergence: "See §1.6. Array order; first rejection wins; `allSettled` gives `{status, value}` / `{status, reason}`.", Status: "implemented", Oracles: []string{"async"}},
		{Builtin: "`Promise.race`, `Promise.any`, `new Promise(executor)`", Helper: "host / client", Rule: "T-7.4d", Divergence: "Depends on timing.", Status: "never"},
	}},
	{Title: "2.9 Error objects", Rows: []ssrCatalogueRow{
		{Builtin: "`err.message`, `err.name`, `new Error(m)`, `new TypeError(m)`, `new RangeError(m)`", Helper: "`vela.NewError`", Rule: "T-6.5d, T-7.4h", Divergence: "Host errors expose `\"Internal error\"` unless `vela.PublicError`.", Status: "implemented", Oracles: []string{"errors-new", "fn-try"}},
		{Builtin: "`String(err)`, `` `${err}` ``, `'' + err`", Helper: "`vela.String`", Rule: "T-6.4a", Divergence: "`Error.prototype.toString`: `\"Name: message\"`, the name alone when the message is empty, the message alone when the name is empty. The message is not enumerable: `Object.keys`, spread and `JSON.stringify` do not see it (`{}`), as in JavaScript.", Status: "implemented", Oracles: []string{"errors-tostring"}},
		{Builtin: "`err.stack`", Helper: "—", Rule: "—", Status: "never"},
	}},
}

// renderSSRCatalogue renders matrix §2.
func renderSSRCatalogue() string {
	var b strings.Builder
	b.WriteString("## 2. Built-ins\n\n")
	b.WriteString("Generated from `ssr_catalogue.go` (V-11.1a); regenerate with\n`VELA_UPDATE_MATRIX=1 go test -run TestSSRMatrixBuiltins .`. The\n*Divergence* column lists the known places where the obvious Go call\ndiffers from JavaScript; each has rows in the listed oracle groups of\n`testdata/ssr_expression_oracle.json`. Examples marked ✓ were checked\nagainst Node and Go on 2026-10-03.\n")
	for _, section := range ssrCatalogue {
		fmt.Fprintf(&b, "\n### %s\n\n", section.Title)
		if section.Intro != "" {
			b.WriteString(section.Intro + "\n\n")
		}
		b.WriteString("| Built-in | Go helper | Rule | Divergence / required edge cases | Oracles | Status |\n| --- | --- | --- | --- | --- | --- |\n")
		for _, row := range section.Rows {
			oracles := "—"
			if len(row.Oracles) > 0 {
				oracles = "`" + strings.Join(row.Oracles, "`, `") + "`"
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n", row.Builtin, row.Helper, row.Rule, row.Divergence, oracles, row.Status)
		}
	}
	return b.String()
}
