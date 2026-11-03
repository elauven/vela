package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/elauven/vela/app"
)

func TestRun(t *testing.T) {
	for _, tc := range []struct {
		args   []string
		code   int
		stdout string
		stderr string
	}{
		{[]string{"version"}, 0, "vela-app " + app.Version, ""},
		{[]string{"help"}, 0, "usage: vela-app", ""},
		{nil, 2, "", "usage: vela-app"},
		{[]string{"gen"}, 1, "", "not implemented yet"},
		{[]string{"nope"}, 2, "", `unknown command "nope"`},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(tc.args, &stdout, &stderr); code != tc.code {
			t.Errorf("%v: exit %d, want %d", tc.args, code, tc.code)
		}
		if !strings.Contains(stdout.String(), tc.stdout) || (tc.stdout == "" && stdout.Len() > 0) {
			t.Errorf("%v: stdout %q, want %q", tc.args, stdout.String(), tc.stdout)
		}
		if !strings.Contains(stderr.String(), tc.stderr) || (tc.stderr == "" && stderr.Len() > 0) {
			t.Errorf("%v: stderr %q, want %q", tc.args, stderr.String(), tc.stderr)
		}
	}
}
