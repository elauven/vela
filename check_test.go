package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// checkProject writes a project of components into a temporary directory.
func checkProject(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, source := range files {
		writeTestFile(t, filepath.Join(root, "src", name), source)
	}
	return filepath.Join(root, "src")
}

const (
	checkCleanComponent   = "<script>\n  let count = $state(0);\n</script>\n<template><button @click={() => count++}>{count}</button></template>"
	checkWarningComponent = "<script>\n  export let name = 'a';\n</script>\n<template><p title=\"x {name}\">{name}</p></template>"
	checkErrorComponent   = "<script>\n  let user = $state({ name: '' });\n  function reset() { user = { name: '' }; }\n</script>\n<template><input bind:value={user.name} @blur={reset} /></template>"
)

func runCheck(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runCheckCommand(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestCheckExitCodes(t *testing.T) {
	clean := checkProject(t, map[string]string{"App.vela": checkCleanComponent})
	warning := checkProject(t, map[string]string{"App.vela": checkWarningComponent})
	broken := checkProject(t, map[string]string{"App.vela": checkErrorComponent, "Other.vela": checkCleanComponent})
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"clean", []string{"--project", clean}, 0},
		{"clean ssr", []string{"--project", clean, "--ssr"}, 0},
		{"warning", []string{"--project", warning}, 0},
		{"warning fails on warning", []string{"--project", warning, "--fail-on", "warning"}, 1},
		{"error", []string{"--project", broken}, 1},
		{"listed file", []string{filepath.Join(broken, "App.vela")}, 1},
		{"bad format", []string{"--project", clean, "--format", "xml"}, 2},
		{"bad fail-on", []string{"--project", clean, "--fail-on", "hint"}, 2},
		{"no target", nil, 2},
		{"missing project", []string{"--project", filepath.Join(clean, "nope")}, 2},
		{"ssr without project", []string{"--ssr", filepath.Join(clean, "App.vela")}, 2},
	}
	for _, c := range cases {
		code, stdout, stderr := runCheck(t, c.args...)
		if code != c.want {
			t.Errorf("%s: exit %d, want %d\nstdout: %s\nstderr: %s", c.name, code, c.want, stdout, stderr)
		}
	}
}

func TestCheckJSONOutput(t *testing.T) {
	broken := checkProject(t, map[string]string{"App.vela": checkErrorComponent, "Warn.vela": checkWarningComponent})
	code, stdout, _ := runCheck(t, "--project", broken, "--format", "json")
	var doc checkJSON
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("%v\n%s", err, stdout)
	}
	if code != 1 || doc.Version != 1 || doc.Components != 2 || doc.Errors != 1 || doc.Warnings != 1 || len(doc.Diagnostics) != 2 {
		t.Fatalf("exit %d, document %+v", code, doc)
	}
	if d := doc.Diagnostics[0]; d.Code != "VELA-BIND-MEMBER" || d.Line != 5 || !strings.HasSuffix(d.File, "src/App.vela") {
		t.Fatalf("first diagnostic = %+v", d)
	}
}

// checkBuildFailures lists the components a build's error output marks as
// failed, by their path relative to the source root.
func checkBuildFailures(output string) map[string]bool {
	failed := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^  (?:✗|x)  (\S+?)(?::| )`).FindAllStringSubmatch(output, -1) {
		failed[filepath.ToSlash(m[1])] = true
	}
	return failed
}

func TestCheckReportsTheDiagnosticsOfABuild(t *testing.T) {
	projects := []struct {
		root string
		ssr  bool
	}{
		{"examples/dashboard", false},
		{"examples/dashboard", true},
		{"test_projects/qpage-builder/src", true},
		{"test_projects/corpus", false},
	}
	for _, p := range projects {
		name := fmt.Sprintf("%s ssr=%v", p.root, p.ssr)
		args := []string{"--project", p.root, "--format", "json"}
		if p.ssr {
			args = append(args, "--ssr")
		}
		_, stdout, stderr := runCheck(t, args...)
		var doc checkJSON
		if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
			t.Fatalf("%s: %v\n%s", name, err, stderr)
		}

		var buildErr, buildStatus bytes.Buffer
		opts := buildFileOptionsForCheck(t)
		opts.ErrWriter, opts.StatusWriter = &buildErr, &buildStatus
		files := CollectVela(p.root)
		out := t.TempDir()
		if p.ssr {
			BuildSSRProject(files, p.root, out, opts, false)
		} else {
			BuildProject(files, p.root, out, opts, false)
		}
		build := buildErr.String() + buildStatus.String()

		// Every diagnostic of check is in the build output, at its position.
		for _, d := range doc.Diagnostics {
			message, _, _ := strings.Cut(d.Message, " (reason: ")
			// An SSR error without a site has no position in the text.
			located := d.Line > 1 || d.Column > 1
			if !strings.Contains(build, message) || (located && !strings.Contains(build, fmt.Sprintf(":%d:%d", d.Line, d.Column))) {
				t.Errorf("%s: %+v is not in the build output", name, d)
			}
		}
		// Every component the build fails has an error from check.
		withErrors := map[string]bool{}
		for _, d := range doc.Diagnostics {
			if d.Severity == "error" {
				if rel, err := filepath.Rel(p.root, filepath.FromSlash(d.File)); err == nil {
					withErrors[filepath.ToSlash(rel)] = true
				}
				withErrors[d.File] = true
			}
		}
		failed := checkBuildFailures(build)
		if len(failed) == 0 && doc.Errors > 0 {
			t.Errorf("%s: check reports errors, the build none", name)
		}
		for component := range failed {
			if !withErrors[component] {
				t.Errorf("%s: the build fails %s, check reports no error for it", name, component)
			}
		}
	}
}

func buildFileOptionsForCheck(t *testing.T) CompileOptions {
	t.Helper()
	opts := buildFileOptions(t, nil)
	opts.SSRModule = SSRDefaultModule
	return opts
}

// treeDigest hashes the paths and contents of every file under root.
func treeDigest(t *testing.T, root string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s %v\n", path, info.IsDir())
		if !info.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			h.Write(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func TestCheckWritesNoFiles(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	project := checkProject(t, map[string]string{"App.vela": checkCleanComponent, "lib/Card.vela": checkErrorComponent})
	writeTestFile(t, filepath.Join(project, "format.js"), "export const f = (x) => x;\n")
	root := filepath.Dir(project)
	before := treeDigest(t, root)
	for _, args := range [][]string{{"--project", project}, {"--project", project, "--ssr"}, {filepath.Join(project, "App.vela")}} {
		runCheck(t, args...)
		if after := treeDigest(t, root); after != before {
			t.Fatalf("vela check %v changed the project", args)
		}
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Fatalf("vela check left %d temporary entries", len(left))
	}
}

func TestCheckWatchSnapshot(t *testing.T) {
	project := checkProject(t, map[string]string{"App.vela": checkCleanComponent})
	writeTestFile(t, filepath.Join(project, "node_modules", "x", "index.js"), "")
	a := watchSnapshot([]string{project})
	if len(a) != 1 {
		t.Fatalf("snapshot = %v, want the component only", a)
	}
	if !sameSnapshot(a, watchSnapshot([]string{project})) {
		t.Fatal("unchanged tree differs")
	}
	writeTestFile(t, filepath.Join(project, "B.vela"), checkCleanComponent)
	if sameSnapshot(a, watchSnapshot([]string{project})) {
		t.Fatal("a new file is not a change")
	}
}

func TestCheckSARIFValidatesAgainstSchema(t *testing.T) {
	var schema any
	if err := json.Unmarshal([]byte(readTestFile(t, filepath.Join("testdata", "sarif", "sarif-schema-2.1.0.json"))), &schema); err != nil {
		t.Fatal(err)
	}
	broken := checkProject(t, map[string]string{"App.vela": checkErrorComponent, "Warn.vela": checkWarningComponent, "Bad name.vela": "<script>let x = ;</script>"})
	for _, args := range [][]string{
		{"--project", broken, "--format", "sarif"},
		{"--project", "test_projects/qpage-builder/src", "--ssr", "--format", "sarif"},
		{"--project", checkProject(t, map[string]string{"App.vela": checkCleanComponent}), "--format", "sarif"},
	} {
		_, stdout, _ := runCheck(t, args...)
		var doc any
		if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		v := &schemaValidator{root: schema}
		v.validate(schema, doc, "$")
		for _, problem := range v.problems {
			t.Errorf("%v: %s", args, problem)
		}
		results := doc.(map[string]any)["runs"].([]any)[0].(map[string]any)["results"].([]any)
		if args[1] == broken && len(results) != 3 {
			t.Errorf("%v: %d results, want 3", args, len(results))
		}
		if len(results) > 0 {
			// The validator rejects a result the schema does not admit.
			results[0].(map[string]any)["level"] = "fatal"
			results[0].(map[string]any)["region"] = 1
			v := &schemaValidator{root: schema}
			v.validate(schema, doc, "$")
			if len(v.problems) != 2 {
				t.Errorf("%v: invalid level and property give problems %v", args, v.problems)
			}
		}
	}
}

// schemaValidator checks a JSON value against the JSON Schema draft-07
// keywords the SARIF schema uses; an unknown keyword is a problem, so the
// check never passes by skipping a rule.
type schemaValidator struct {
	root     any
	problems []string
}

func (v *schemaValidator) fail(at, format string, args ...any) {
	v.problems = append(v.problems, at+": "+fmt.Sprintf(format, args...))
}

func (v *schemaValidator) resolve(ref string) any {
	node := v.root
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		node = node.(map[string]any)[part]
	}
	return node
}

func (v *schemaValidator) matches(schema, value any) bool {
	probe := &schemaValidator{root: v.root}
	probe.validate(schema, value, "")
	return len(probe.problems) == 0
}

func jsonType(value any) string {
	switch x := value.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64:
		if x == math.Trunc(x) {
			return "integer"
		}
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	default:
		return "object"
	}
}

func (v *schemaValidator) validate(schema, value any, at string) {
	if b, ok := schema.(bool); ok {
		if !b {
			v.fail(at, "no value is allowed")
		}
		return
	}
	s := schema.(map[string]any)
	for keyword, rule := range s {
		switch keyword {
		case "$schema", "$id", "title", "description", "default", "definitions":
		case "$ref":
			v.validate(v.resolve(rule.(string)), value, at)
		case "type":
			got := jsonType(value)
			if want := rule.(string); got != want && !(want == "number" && got == "integer") {
				v.fail(at, "type %s, want %s", got, want)
			}
		case "enum":
			found := false
			for _, option := range rule.([]any) {
				found = found || fmt.Sprint(option) == fmt.Sprint(value)
			}
			if !found {
				v.fail(at, "%v is not one of %v", value, rule)
			}
		case "const":
			if fmt.Sprint(rule) != fmt.Sprint(value) {
				v.fail(at, "%v, want %v", value, rule)
			}
		case "minimum", "maximum":
			if n, ok := value.(float64); ok {
				limit := rule.(float64)
				if (keyword == "minimum" && n < limit) || (keyword == "maximum" && n > limit) {
					v.fail(at, "%v breaks %s %v", n, keyword, limit)
				}
			}
		case "pattern":
			if str, ok := value.(string); ok && !regexp.MustCompile(rule.(string)).MatchString(str) {
				v.fail(at, "%q does not match %s", str, rule)
			}
		case "format":
			if str, ok := value.(string); ok {
				u, err := url.Parse(str)
				switch rule.(string) {
				case "uri":
					if err != nil || u.Scheme == "" {
						v.fail(at, "%q is not a URI", str)
					}
				case "uri-reference":
					if err != nil {
						v.fail(at, "%q is not a URI reference", str)
					}
				case "date-time":
				default:
					v.fail(at, "unknown format %v", rule)
				}
			}
		case "required":
			if obj, ok := value.(map[string]any); ok {
				for _, name := range rule.([]any) {
					if _, ok := obj[name.(string)]; !ok {
						v.fail(at, "missing %v", name)
					}
				}
			}
		case "properties", "additionalProperties":
			obj, ok := value.(map[string]any)
			if !ok {
				continue
			}
			properties, _ := s["properties"].(map[string]any)
			for name, item := range obj {
				if sub, ok := properties[name]; ok {
					if keyword == "properties" {
						v.validate(sub, item, at+"."+name)
					}
				} else if keyword == "additionalProperties" {
					v.validate(rule, item, at+"."+name)
				}
			}
		case "items":
			if arr, ok := value.([]any); ok {
				for i, item := range arr {
					v.validate(rule, item, fmt.Sprintf("%s[%d]", at, i))
				}
			}
		case "minItems":
			if arr, ok := value.([]any); ok && float64(len(arr)) < rule.(float64) {
				v.fail(at, "%d items, want at least %v", len(arr), rule)
			}
		case "uniqueItems":
			if arr, ok := value.([]any); ok && rule.(bool) {
				seen := map[string]bool{}
				for _, item := range arr {
					key, _ := json.Marshal(item)
					if seen[string(key)] {
						v.fail(at, "duplicate item %s", key)
					}
					seen[string(key)] = true
				}
			}
		case "anyOf", "oneOf":
			n := 0
			for _, option := range rule.([]any) {
				if v.matches(option, value) {
					n++
				}
			}
			if n == 0 || (keyword == "oneOf" && n > 1) {
				v.fail(at, "%d alternatives of %s match", n, keyword)
			}
		default:
			v.fail(at, "unsupported schema keyword %q", keyword)
		}
	}
}
