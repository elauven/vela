package gen

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/elauven/vela/app"
)

// The shared routing fixture (docs/APP.md P-15): app/testdata/routing is a
// routes tree, and cases.json lists URLs with the route and parameters
// they match. The Go matcher of package app and the JavaScript matcher of
// @govela/app/match must both give exactly these answers for the table the
// generator builds.

type routingCase struct {
	URL    string            `json:"url"`
	Route  *string           `json:"route"`
	Params map[string]string `json:"params"`
}

const routingFixture = "../../testdata/routing"

func loadRoutingFixture(t *testing.T) (*Model, []routingCase) {
	t.Helper()
	model, diags := Load(routingFixture)
	if len(diags) > 0 {
		t.Fatalf("fixture: %v", Error(diags))
	}
	data, err := os.ReadFile(filepath.Join(routingFixture, "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []routingCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	return model, cases
}

// hexMatch is the Go half of the fixture's custom matcher
// (testdata/routing/server/params/hex), which the go tool cannot import
// from testdata.
func hexMatch(segment string) bool {
	if len(segment) != 6 {
		return false
	}
	for i := 0; i < len(segment); i++ {
		if c := segment[i]; !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func TestRoutingFixtureGo(t *testing.T) {
	model, cases := loadRoutingFixture(t)
	matchers := map[string]func(string) bool{"hex": hexMatch}
	for name, match := range app.BuiltinMatchers() {
		matchers[name] = match
	}
	table := model.Tables(matchers)
	for _, c := range cases {
		match, ok := table.Match(c.URL)
		switch {
		case c.Route == nil && ok:
			t.Errorf("%q: matched %s %v, want no route", c.URL, match.Route.ID, match.Params)
		case c.Route != nil && !ok:
			t.Errorf("%q: no route, want %s", c.URL, *c.Route)
		case c.Route != nil && (match.Route.ID != *c.Route || !reflect.DeepEqual(match.Params, c.Params)):
			t.Errorf("%q: %s %v, want %s %v", c.URL, match.Route.ID, match.Params, *c.Route, c.Params)
		}
	}
}

const routingScript = `
import { matchPath, builtinMatchers } from %MATCH%;
import { match as hex } from %HEX%;
import { readFileSync } from 'node:fs';
const [tableFile, casesFile] = process.argv.slice(2);
const table = JSON.parse(readFileSync(tableFile, 'utf8'));
table.matchers = { ...builtinMatchers, hex };
const failures = [];
for (const c of JSON.parse(readFileSync(casesFile, 'utf8'))) {
  const got = matchPath(table, c.url);
  const actual = got && { route: got.route.id, params: sortKeys(got.params) };
  const want = c.route === null ? null : { route: c.route, params: sortKeys(c.params) };
  if (JSON.stringify(actual) !== JSON.stringify(want)) {
    failures.push(c.url + ': ' + JSON.stringify(actual) + ', want ' + JSON.stringify(want));
  }
}
function sortKeys(o) { return Object.fromEntries(Object.entries(o).sort()); }
process.stdout.write(JSON.stringify(failures));
`

func TestRoutingFixtureJS(t *testing.T) {
	node := requireNode(t)
	model, _ := loadRoutingFixture(t)
	routes, patterns := model.JSTable()
	dir := t.TempDir()
	table, _ := json.Marshal(map[string]any{"routes": routes, "patterns": patterns})
	tableFile := filepath.Join(dir, "table.json")
	writeFile(t, tableFile, string(table))
	matchJS, _ := filepath.Abs("../../../packages/app/match.js")
	hexJS, _ := filepath.Abs(filepath.Join(routingFixture, "src/params/hex.js"))
	script := strings.NewReplacer("%MATCH%", jsonString(fileURL(matchJS)), "%HEX%", jsonString(fileURL(hexJS))).Replace(routingScript)
	scriptFile := filepath.Join(dir, "routing.mjs")
	writeFile(t, scriptFile, script)
	casesFile, _ := filepath.Abs(filepath.Join(routingFixture, "cases.json"))
	out, err := runNode(node, scriptFile, tableFile, casesFile)
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	var failures []string
	if err := json.Unmarshal(out, &failures); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, f := range failures {
		t.Error(f)
	}
}

// TestRoutingFixtureCoverage keeps the fixture honest: it must exercise
// optional segments, groups, rest parameters and matchers (task 074).
func TestRoutingFixtureCoverage(t *testing.T) {
	model, cases := loadRoutingFixture(t)
	routes := map[string]bool{}
	for _, c := range cases {
		if c.Route != nil {
			routes[*c.Route] = true
		}
	}
	for _, r := range model.Routes {
		if !routes[r.ID] {
			t.Errorf("no case matches route %s", r.ID)
		}
	}
	for _, want := range []string{"[[", "(", "[...", "=int]", "=uuid]", "=slug]", "=hex]"} {
		found := false
		for id := range routes {
			found = found || strings.Contains(id, want)
		}
		if !found {
			t.Errorf("no case covers %s", want)
		}
	}
}

func requireNode(t *testing.T) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("VELA_REQUIRE_NODE") != "" {
			t.Fatal("node is required (VELA_REQUIRE_NODE)")
		}
		t.Skip("node not available")
	}
	return node
}

// runNode runs a script and returns its standard output; the standard
// error goes into the error.
func runNode(node string, args ...string) ([]byte, error) {
	var stderr strings.Builder
	command := exec.Command(node, args...)
	command.Stderr = &stderr
	out, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("%v\n%s%s", err, out, stderr.String())
	}
	return out, nil
}

func fileURL(path string) string {
	return (&url.URL{Scheme: "file", Path: "/" + strings.TrimPrefix(filepath.ToSlash(path), "/")}).String()
}

func jsonString(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}

func writeFile(t *testing.T, file, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}
