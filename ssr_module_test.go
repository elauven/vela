package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ssrModuleFixture is a project whose component imports local modules,
// one of which imports another (T-7.3a).
var ssrModuleFixture = map[string]string{
	"lib/util.js": `const SUFFIX = '!';
const LABELS = { a: 'Alpha', b: 'Beta' };
export const VERSION = 'v' + 2;

function shout(s) {
  return s + SUFFIX;
}

export function label(key) {
  if (LABELS[key] === undefined) return 'unknown';
  return shout(LABELS[key]);
}

export function sum(xs) {
  let total = 0;
  for (const x of xs) total += x;
  return total;
}

export function fail(v) {
  if (v) throw new TypeError('bad ' + v);
  return 'fine';
}

export const pick = (o, k = 'a') => {
  try {
    return o[k].name;
  } catch (e) {
    return e.name;
  }
};
`,
	"fmt.js": `import { label } from './lib/util.js';
export function twice(k) {
  return label(k) + '/' + label(k);
}
`,
	"Card.vela": `<script>
  import { label, sum, VERSION, fail, pick } from './lib/util.js';
  import { twice } from './fmt.js';
  export let kind = 'a';
  export let nums = [1, 2, 3];
  export let bad = false;
  export let item = { a: { name: 'n' } };
</script>
<template><p>{label(kind)} {label('z')} {sum(nums)} {VERSION} {twice(kind)} {fail(bad)} {pick(item)} {pick(item, 'b')}</p></template>
`,
}

func writeSSRModuleFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "src")
	for name, text := range files {
		file := filepath.Join(src, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return src
}

// TestSSRModulesMatchNode renders a component that calls translated module
// functions and compares the text with Node running the same modules.
func TestSSRModulesMatchNode(t *testing.T) {
	node, _ := requireJsdom(t)
	src := writeSSRModuleFixture(t, ssrModuleFixture)
	cases := []string{
		`{}`,
		`{"kind":"b","nums":[4.5,5],"item":{"a":{"name":"x"},"b":{"name":"y"}}}`,
		`{"kind":"q","nums":[],"item":{}}`,
		`{"bad":true}`,
	}
	// Node: the same template text, or the exception's constructor name.
	script := `
import { label, sum, VERSION, fail, pick } from './lib/util.js';
import { twice } from './fmt.js';
const defaults = { kind: 'a', nums: [1, 2, 3], bad: false, item: { a: { name: 'n' } } };
const out = [];
for (const text of JSON.parse(process.argv[2])) {
  const { kind, nums, bad, item } = { ...defaults, ...JSON.parse(text) };
  try {
    out.push(` + "`${label(kind)} ${label('z')} ${sum(nums)} ${VERSION} ${twice(kind)} ${fail(bad)} ${pick(item)} ${pick(item, 'b')}`" + `);
  } catch (e) {
    out.push('THROW:' + e.constructor.name);
  }
}
console.log(JSON.stringify(out));
`
	if err := os.WriteFile(filepath.Join(src, "oracle.mjs"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(cases)
	command := exec.Command(node, "oracle.mjs", string(encoded))
	command.Dir = src
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, output)
	}
	var want []string
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatalf("node output: %v\n%s", err, output)
	}
	if err := os.Remove(filepath.Join(src, "oracle.mjs")); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(filepath.Dir(src), "out")

	opts := CompileOptions{StatusWriter: io.Discard, SSRModule: "example.com/mods/server"}
	if errors := buildSSRProject(collectVela(src), src, out, opts, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}

	for _, file := range []string{"server/modules/lib/util/util.go", "server/modules/fmt/fmt.go"} {
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(file))); err != nil {
			t.Fatalf("module package %s: %v", file, err)
		}
	}
	got := ssrRenderCases(t, out, "example.com/mods", "Card", cases)
	for i := range cases {
		if got[i] != want[i] {
			t.Errorf("props %s: Go %q, Node %q", cases[i], got[i], want[i])
		}
	}
}

// ssrRenderCases renders component name statically for each JSON props
// value with the generated project in out.
func ssrRenderCases(t *testing.T, out, module, name string, cases []string) []string {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	mod := "module " + module + "\n\ngo 1.25\n\nrequire github.com/elauven/vela v0.0.0\n\nreplace github.com/elauven/vela => " + filepath.ToSlash(root) + "\n"
	main := `package main

import (
	"encoding/json"
	"errors"
	"html"
	"os"
	"reflect"
	"regexp"
	"strings"

	vela "github.com/elauven/vela/ssr_runtime"
	c "` + module + `/server/components"
)

func main() {
	var cases []string
	if err := json.Unmarshal([]byte(os.Args[1]), &cases); err != nil {
		panic(err)
	}
	paragraph := regexp.MustCompile("^<p[^>]*>(.*)</p>$")
	var out []string
	for _, text := range cases {
		var raw map[string]any
		if err := json.Unmarshal([]byte(text), &raw); err != nil {
			panic(err)
		}
		var props c.` + name + `Props
		target := reflect.ValueOf(&props).Elem()
		set := map[string]bool{}
		for key, value := range raw {
			field := target.FieldByNameFunc(func(field string) bool { return strings.EqualFold(field, key) })
			field.Set(reflect.ValueOf(value).Convert(field.Type()))
			set[key] = true
		}
		target.FieldByName("Set").Set(reflect.ValueOf(set))
		var html_ strings.Builder
		if err := c.Render` + name + `Static(&html_, props, c.` + name + `Slots{}); err != nil {
			var thrown *vela.RenderError
			if errors.As(err, &thrown) {
				out = append(out, "THROW:"+thrown.Kind)
				continue
			}
			out = append(out, "ERROR:"+err.Error())
			continue
		}
		text := html_.String()
		if match := paragraph.FindStringSubmatch(text); match != nil {
			text = html.UnescapeString(match[1])
		}
		out = append(out, text)
	}
	json.NewEncoder(os.Stdout).Encode(out)
}
`
	for file, text := range map[string]string{"go.mod": mod, "main.go": main} {
		if err := os.WriteFile(filepath.Join(out, file), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	encoded, _ := json.Marshal(cases)
	command := exec.Command("go", "run", ".", string(encoded))
	command.Dir = out
	command.Stderr = os.Stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var got []string
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("render output: %v\n%s", err, output)
	}
	return got
}

// TestSSRModuleDiagnostics: a module with top-level side effects or a
// circular import is not translated, and the call names the cause and the
// host that would fix it (T-7.3b).
func TestSSRModuleDiagnostics(t *testing.T) {
	src := writeSSRModuleFixture(t, map[string]string{
		"effect.js": "export function f() { return 1; }\nconsole.log('loaded');\n",
		"state.js":  "let n = 0;\nexport function g() { return n; }\n",
		"a.js":      "import { b } from './b.js';\nexport function a() { return b(); }\n",
		"b.js":      "import { a } from './a.js';\nexport function b() { return 2; }\nexport function c() { return a(); }\n",
		"Page.vela": "<script>\n  import { f } from './effect.js';\n  import { g } from './state.js';\n  import { a } from './a.js';\n</script>\n<template><p>{f()} {g()} {a()}</p></template>\n",
	})
	out := filepath.Join(filepath.Dir(src), "out")
	report := filepath.Join(out, "report.json")

	opts := CompileOptions{StatusWriter: io.Discard, SSRModule: "example.com/diag/server", SSRReport: report}
	if errors := buildSSRProject(collectVela(src), src, out, opts, false); errors == 0 {
		t.Fatal("expected SSR errors")
	}

	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"top-level statements other than imports, functions, and const declarations are side effects (T-7.3b)",
		"top-level let bindings are mutable module state",
		"circular module import",
		"//vela:host f",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("report lacks %q", want)
		}
	}
}
