package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// TestSSRExpressionDifferentialFuzz generates random expressions over the
// admitted subset and its built-ins, evaluates them in Node, and renders
// them with generated Go in both tiers (V-11.2b). VELA_FUZZ_SEED picks the
// seed (default: a fixed one, so CI is reproducible), VELA_FUZZ_ROWS the
// number of expressions.
func TestSSRExpressionDifferentialFuzz(t *testing.T) {
	node, _ := requireJsdom(t)
	seed := int64(20261007)
	if value := os.Getenv("VELA_FUZZ_SEED"); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		seed = parsed
	}
	count := 120
	if value := os.Getenv("VELA_FUZZ_ROWS"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			t.Fatal(err)
		}
		count = parsed
	}
	g := &ssrFuzzGenerator{rand: rand.New(rand.NewSource(seed))}
	values := map[string]json.RawMessage{}
	for name, value := range ssrFuzzValues {
		values[name] = json.RawMessage(value)
	}
	rows := make([]ssrExpressionOracleRow, count)
	for i := range rows {
		rows[i] = ssrExpressionOracleRow{Group: "fuzz", Name: fmt.Sprintf("fuzz-%d-%d", seed, i), Expression: g.top(), Values: values}
	}
	// Node computes the expected text or exception of every row.
	script := `
const rows = JSON.parse(require('fs').readFileSync(0, 'utf8'));
for (const row of rows) {
  const names = Object.keys(row.values);
  try {
    row.expected = String(Function(...names, '"use strict"; return (' + row.expression + ');')(...names.map((n) => row.values[n])));
  } catch (e) {
    row.throws = e.constructor.name;
  }
}
process.stdout.write(JSON.stringify(rows));
`
	input, _ := json.Marshal(rows)
	command := exec.Command(node, "-e", script)
	command.Stdin = strings.NewReader(string(input))
	output, err := command.Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	if err := json.Unmarshal(output, &rows); err != nil {
		t.Fatal(err)
	}
	failures := runSSRExpressionRows(t, rows)
	for _, failure := range failures {
		t.Error(failure)
	}
	if len(failures) > 0 {
		t.Logf("reproduce with VELA_FUZZ_SEED=%d VELA_FUZZ_ROWS=%d", seed, count)
	}
}

// ssrFuzzValues are the props every generated expression may read.
var ssrFuzzValues = map[string]string{
	"n": `2.5`, "m": `-7`, "k": `3`, "big": `1e21`, "frac": `0.1`,
	"s": `"Straße 😀 Ж"`, "t": `" 12px "`, "e": `""`, "h": `"0x1f"`,
	"xs": `[3, 1, null, "b", 10, "a"]`, "ns": `[4.5, -2, 0, 9]`, "ss": `["b", "", "a😀", "Б"]`,
	"o": `{"a": 1, "b": "two", "c": [1, 2], "d": null}`,
	"yes": `true`, "no": `false`, "z": `null`,
}

type ssrFuzzGenerator struct{ rand *rand.Rand }

func (g *ssrFuzzGenerator) pick(options ...string) string { return options[g.rand.Intn(len(options))] }

func (g *ssrFuzzGenerator) top() string {
	switch g.rand.Intn(5) {
	case 0:
		return g.num(3)
	case 1:
		return g.bool(3)
	case 2:
		return g.arr(3) + ".join('|')"
	}
	return g.str(3)
}

func (g *ssrFuzzGenerator) smallInt() string {
	return g.pick("0", "1", "2", "3", "-1", "-2", "5")
}

func (g *ssrFuzzGenerator) num(depth int) string {
	if depth == 0 || g.rand.Intn(4) == 0 {
		// Literals are parenthesised: 0.toFixed is a syntax error.
		return g.pick("n", "m", "k", "big", "frac", "(0)", "(1.5)", "(-0.5)", "(100)", "xs.length", "s.length")
	}
	d := depth - 1
	switch g.rand.Intn(16) {
	case 0:
		return "(" + g.num(d) + " " + g.pick("+", "-", "*", "/", "%") + " " + g.num(d) + ")"
	case 1:
		return "Math." + g.pick("round", "floor", "ceil", "trunc", "abs", "sign", "sqrt", "cbrt") + "(" + g.num(d) + ")"
	case 2:
		return "Math." + g.pick("max", "min", "hypot") + "(" + g.num(d) + ", " + g.num(d) + ")"
	case 3:
		return g.str(d) + ".length"
	case 4:
		return g.str(d) + "." + g.pick("indexOf", "lastIndexOf") + "(" + g.str(0) + ")"
	case 5:
		return g.pick("parseInt", "parseFloat", "Number") + "(" + g.str(d) + ")"
	case 6:
		return g.arr(d) + ".length"
	case 7:
		return g.str(d) + ".charCodeAt(" + g.smallInt() + ")"
	case 8:
		return "ns.reduce((a, b) => a + b, " + g.num(d) + ")"
	case 9:
		return "Math.pow(" + g.num(d) + ", " + g.smallInt() + ")"
	case 10:
		return g.arr(d) + ".indexOf(" + g.any(d) + ")"
	case 11:
		return "ns.findIndex((x) => x > " + g.num(d) + ")"
	case 12:
		return "Object.keys(o).length"
	case 13:
		return "(" + g.bool(d) + " ? " + g.num(d) + " : " + g.num(d) + ")"
	case 14:
		return "-(" + g.num(d) + ")"
	}
	return "(Number.isInteger(" + g.num(d) + ") ? 1 : 0)"
}

func (g *ssrFuzzGenerator) str(depth int) string {
	if depth == 0 || g.rand.Intn(4) == 0 {
		return g.pick("s", "t", "e", "h", "'ab'", "'Ж'", "'😀'", "' x '", "'$&'")
	}
	d := depth - 1
	switch g.rand.Intn(20) {
	case 0:
		return "(" + g.str(d) + " + " + g.any(d) + ")"
	case 1:
		return g.str(d) + "." + g.pick("toUpperCase", "toLowerCase", "trim", "trimStart", "trimEnd") + "()"
	case 2:
		return g.str(d) + ".slice(" + g.smallInt() + ", " + g.smallInt() + ")"
	case 3:
		return g.str(d) + ".substring(" + g.smallInt() + ", " + g.smallInt() + ")"
	case 4:
		return g.str(d) + "." + g.pick("padStart", "padEnd") + "(" + g.pick("0", "4", "9") + ", " + g.str(0) + ")"
	case 5:
		return g.str(d) + ".repeat(" + g.pick("0", "1", "2") + ")"
	case 6:
		return g.str(d) + "." + g.pick("replace", "replaceAll") + "(" + g.pick("'a'", "'S'", "''", "'😀'") + ", " + g.pick("'-'", "'$&$&'", "'$$'", "(m) => m + m") + ")"
	case 7:
		return g.str(d) + "." + g.pick("charAt", "at") + "(" + g.smallInt() + ")"
	case 8:
		return g.arr(d) + ".join(" + g.pick("", "'-'", "''") + ")"
	case 9:
		return "String(" + g.any(d) + ")"
	case 10:
		return "JSON.stringify(" + g.any(d) + ")"
	case 11:
		return "(" + g.num(d) + ").toFixed(" + g.pick("0", "1", "2", "3") + ")"
	case 12:
		return "encodeURIComponent(" + g.str(d) + ")"
	case 13:
		return "`[${" + g.any(d) + "}|${" + g.any(d) + "}]`"
	case 14:
		return "(" + g.bool(d) + " ? " + g.str(d) + " : " + g.str(d) + ")"
	case 15:
		return "(" + g.any(d) + " ?? " + g.str(d) + ")"
	case 16:
		return g.str(d) + ".concat(" + g.any(d) + ")"
	case 17:
		return "typeof " + g.any(d)
	case 18:
		return "o." + g.pick("b", "missing", "c")
	}
	return "Object.entries(o).map((e) => e[0] + '=' + e[1]).join(';')"
}

func (g *ssrFuzzGenerator) bool(depth int) string {
	if depth == 0 || g.rand.Intn(4) == 0 {
		return g.pick("yes", "no", "true", "false")
	}
	d := depth - 1
	switch g.rand.Intn(10) {
	case 0:
		return "(" + g.num(d) + " " + g.pick("<", "<=", ">", ">=", "===", "!==") + " " + g.num(d) + ")"
	case 1:
		return "(" + g.str(d) + " " + g.pick("<", "===", "!==", ">=") + " " + g.str(d) + ")"
	case 2:
		return g.str(d) + "." + g.pick("includes", "startsWith", "endsWith") + "(" + g.str(0) + ")"
	case 3:
		return g.arr(d) + ".includes(" + g.any(d) + ")"
	case 4:
		return "Array.isArray(" + g.any(d) + ")"
	case 5:
		return g.pick("isNaN", "isFinite", "Number.isNaN", "Number.isFinite", "Boolean") + "(" + g.any(d) + ")"
	case 6:
		return "!" + g.bool(d)
	case 7:
		return "(" + g.bool(d) + " " + g.pick("&&", "||") + " " + g.bool(d) + ")"
	case 8:
		return "ns." + g.pick("some", "every") + "((x) => x > " + g.num(d) + ")"
	}
	return "Object.hasOwn(o, " + g.str(0) + ")"
}

func (g *ssrFuzzGenerator) arr(depth int) string {
	if depth == 0 || g.rand.Intn(4) == 0 {
		return g.pick("xs", "ns", "ss", "[]", "[1, , 3]")
	}
	d := depth - 1
	switch g.rand.Intn(12) {
	case 0:
		return "ns.map((x, i) => x * i + " + g.num(d) + ")"
	case 1:
		return g.arr(d) + ".filter(Boolean)"
	case 2:
		return g.arr(d) + ".slice(" + g.smallInt() + ")"
	case 3:
		return g.str(d) + ".split(" + g.pick("''", "' '", "'a'", "'😀'") + ")"
	case 4:
		return "[" + g.any(d) + ", " + g.any(d) + "]"
	case 5:
		return g.arr(d) + ".concat(" + g.arr(d) + ")"
	case 6:
		return "Object.keys(o)"
	case 7:
		return "[" + g.arr(d) + ", " + g.arr(d) + "].flat()"
	case 8:
		return g.arr(d) + ".toSorted()"
	case 9:
		return "ns.toSorted((a, b) => b - a)"
	case 10:
		return "[..." + g.arr(d) + ", ..." + g.str(0) + "]"
	}
	return g.arr(d) + ".toReversed()"
}

func (g *ssrFuzzGenerator) any(depth int) string {
	switch g.rand.Intn(6) {
	case 0:
		return g.num(depth)
	case 1:
		return g.bool(depth)
	case 2:
		return g.arr(depth)
	case 3:
		return g.pick("z", "o", "o.d", "o.c")
	}
	return g.str(depth)
}
