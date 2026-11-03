package app

import (
	"encoding/json"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// testdata/matchers.json lists segments the built-in matchers accept and
// reject (docs/APP.md P-13). The Go functions of this package and the
// exports of @govela/app/match must agree with it.

type matcherCases map[string]struct {
	Accept []string `json:"accept"`
	Reject []string `json:"reject"`
}

func loadMatcherCases(t *testing.T) matcherCases {
	t.Helper()
	data, err := os.ReadFile("testdata/matchers.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases matcherCases
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	return cases
}

func TestBuiltinMatchersGo(t *testing.T) {
	cases := loadMatcherCases(t)
	builtins := BuiltinMatchers()
	if len(cases) != len(builtins) {
		t.Fatalf("the fixture covers %d matchers, the package has %d", len(cases), len(builtins))
	}
	for name, c := range cases {
		match := builtins[name]
		if match == nil {
			t.Fatalf("no built-in matcher %q", name)
		}
		for _, s := range c.Accept {
			if !match(s) {
				t.Errorf("%s rejects %q", name, s)
			}
		}
		for _, s := range c.Reject {
			if match(s) {
				t.Errorf("%s accepts %q", name, s)
			}
		}
	}
}

const matcherScript = `
import { builtinMatchers } from %MATCH%;
import { readFileSync } from 'node:fs';
const cases = JSON.parse(readFileSync(process.argv[2], 'utf8'));
const failures = [];
if (Object.keys(cases).sort().join() !== Object.keys(builtinMatchers).sort().join()) failures.push('matchers: ' + Object.keys(builtinMatchers));
for (const [name, c] of Object.entries(cases)) {
  const match = builtinMatchers[name];
  if (!match) { failures.push('no matcher ' + name); continue; }
  for (const s of c.accept) if (match(s) !== true) failures.push(name + ' rejects ' + JSON.stringify(s));
  for (const s of c.reject) if (match(s) !== false) failures.push(name + ' accepts ' + JSON.stringify(s));
}
process.stdout.write(JSON.stringify(failures));
`

func TestBuiltinMatchersJS(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("VELA_REQUIRE_NODE") != "" {
			t.Fatal("node is required (VELA_REQUIRE_NODE)")
		}
		t.Skip("node not available")
	}
	matchJS, _ := filepath.Abs("../packages/app/match.js")
	url, _ := json.Marshal((&neturl.URL{Scheme: "file", Path: "/" + strings.TrimPrefix(filepath.ToSlash(matchJS), "/")}).String())
	script := filepath.Join(t.TempDir(), "matchers.mjs")
	if err := os.WriteFile(script, []byte(strings.Replace(matcherScript, "%MATCH%", string(url), 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	cases, _ := filepath.Abs("testdata/matchers.json")
	command := exec.Command(node, script, cases)
	var stderr strings.Builder
	command.Stderr = &stderr
	out, err := command.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s%s", err, out, stderr.String())
	}
	var failures []string
	if err := json.Unmarshal(out, &failures); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, f := range failures {
		t.Error(f)
	}
}
