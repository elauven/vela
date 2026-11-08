package main

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// TestSSRBoundary covers the server side of {#boundary} (task 091): a body
// that fails renders the catch branch in a "c" range in place of anything
// the body wrote, the payload records a "b" entry with the public message,
// the error observer of the render context receives the error with the
// block's position, and errors that are not render errors still fail the
// render.
func TestSSRBoundary(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not available")
	}
	p := newSSRHostProject(t)
	p.write("src/format.js", `export function formatCurrency(value) {
  return String(value) + ' RUB';
}
`)
	p.write("src/Price.vela", `<script>
  import { formatCurrency } from './format.js';
  export let total = 0;
  function check(value) {
    if (value === 7) throw new Error('seven');
    return value;
  }
</script>
<template><span class="price">{check(total)}: {formatCurrency(total)}</span></template>
`)
	p.write("src/App.vela", `<script>
  import { Price } from './Price.vela';
  export let total = 0;
</script>
<template>
  <div class="app">
    <h1>Shop</h1>
    {#boundary}
      <p class="before">before</p>
      <Price :total={total} />
    {:catch error, retry}
      <p class="failed">{error.message}</p>
      <button class="retry" @click={retry}>retry</button>
    {/boundary}
    <footer>end</footer>
  </div>
</template>
`)
	p.write("ssrhost/src/format/format_currency_host.go", `package format

import (
	"context"
	"errors"
	"fmt"

	vela "github.com/elauven/vela/ssr_runtime"
)

// FormatCurrency implements formatCurrency of src/format.js.
//
//vela:host formatCurrency
func FormatCurrency(ctx context.Context, value float64) (string, error) {
	switch {
	case value < 0:
		return "", &vela.PublicError{Message: "negative total", Err: errors.New("db: negative")}
	case value == 0:
		return "", errors.New("db: connection refused")
	}
	return fmt.Sprintf("%.0f RUB", value), nil
}
`)
	if errors, stderr := p.build(false); errors != 0 {
		t.Fatalf("build: %d errors\n%s", errors, stderr)
	}
	p.write("cmd/render/main.go", `package main

import (
	"context"
	"fmt"
	"os"
	"strconv"

	vela "github.com/elauven/vela/ssr_runtime"
	c "example.com/dash/out/server/components"
)

func main() {
	total, _ := strconv.ParseFloat(os.Args[1], 64)
	ctx := vela.WithErrorObserver(context.Background(), func(err error) {
		fmt.Printf("OBSERVED=%v\n", err)
	})
	err := c.RenderAppContext(ctx, os.Stdout, c.AppProps{Total: total, Set: map[string]bool{"total": true}}, c.AppSlots{})
	fmt.Printf("\nERR=%v\n", err)
}
`)
	render := func(total string) string {
		t.Helper()
		command := exec.Command("go", "run", "./cmd/render", total)
		command.Dir = p.root
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("go run: %v\n%s", err, output)
		}
		return string(output)
	}

	ok := render("5")
	for _, want := range []string{`<!--[--><p class="before">before</p>`, `5 RUB</span>`, "ERR=<nil>", `<footer>end</footer>`} {
		if !strings.Contains(ok, want) {
			t.Errorf("render 5 misses %q:\n%s", want, ok)
		}
	}
	if strings.Contains(ok, "OBSERVED") || strings.Contains(ok, `["b"`) {
		t.Errorf("a body without an error reported one:\n%s", ok)
	}

	for _, c := range []struct{ total, message, observed string }{
		{"0", "Internal error", "db: connection refused"},
		{"-1", "negative total", "negative total"},
		{"7", "Internal error", "seven"},
	} {
		out := render(c.total)
		for _, want := range []string{
			`<h1>Shop</h1> <!--[c--><p class="failed">` + c.message + `</p><button class="retry">retry</button><!--]--> <footer>end</footer>`,
			`["b","src/App.vela:8:5",{"message":"` + c.message + `"}]`,
			"OBSERVED={#boundary} at src/App.vela:8:5 caught: ",
			"ERR=<nil>",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("render %s misses %q:\n%s", c.total, want, out)
			}
		}
		if !regexp.MustCompile(`OBSERVED=.*` + regexp.QuoteMeta(c.observed)).MatchString(out) {
			t.Errorf("render %s: the observer did not get %q:\n%s", c.total, c.observed, out)
		}
		// Nothing the body wrote before the failure remains.
		if strings.Contains(out, `class="before"`) || strings.Contains(out, `class="price"`) {
			t.Errorf("render %s kept the failed body:\n%s", c.total, out)
		}
	}
}
