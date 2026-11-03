package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elauven/vela/app"
)

func TestRun(t *testing.T) {
	project := t.TempDir()
	write := func(name, data string) {
		file := filepath.Join(project, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("vela.config.json", `{ "project": "src", "ssrModule": "example.com/site/out/server" }`)
	write("go.mod", "module example.com/site\n\ngo 1.25.0\n")
	write("src/routes/Page.vela", "<template><p>home</p></template>\n")
	empty := t.TempDir()
	for _, tc := range []struct {
		args   []string
		code   int
		stdout string
		stderr string
	}{
		{[]string{"version"}, 0, "vela-app " + app.Version, ""},
		{[]string{"help"}, 0, "usage: vela-app", ""},
		{nil, 2, "", "usage: vela-app"},
		{[]string{"nope"}, 2, "", `unknown command "nope"`},
		{[]string{"gen", "x"}, 2, "", `vela-app gen: unexpected arguments ["x"]`},
		{[]string{"-C", empty, "gen"}, 1, "", "[VELA-APP-CONFIG]"},
		{[]string{"-C", project, "gen"}, 0, "wrote src/_app/VelaApp.vela", ""},
		{[]string{"-C", project, "gen"}, 0, "up to date", ""},
		{[]string{"-C", project, "scaffold", "/"}, 0, "created server/routes/load.go", ""},
		{[]string{"-C", project, "scaffold"}, 0, "every route has a load.go", ""},
		{[]string{"-C", project, "gen"}, 0, "wrote internal/appgen/routes_gen.go", ""},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), tc.args, &stdout, &stderr); code != tc.code {
			t.Errorf("%v: exit %d, want %d\n%s%s", tc.args, code, tc.code, stdout.String(), stderr.String())
		}
		if !strings.Contains(stdout.String(), tc.stdout) || (tc.stdout == "" && stdout.Len() > 0) {
			t.Errorf("%v: stdout %q, want %q", tc.args, stdout.String(), tc.stdout)
		}
		if !strings.Contains(stderr.String(), tc.stderr) || (tc.stderr == "" && stderr.Len() > 0) {
			t.Errorf("%v: stderr %q, want %q", tc.args, stderr.String(), tc.stderr)
		}
	}
}
