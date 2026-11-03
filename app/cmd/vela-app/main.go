// Command vela-app generates the sources of a Vela App from its routes
// directory (docs/APP.md). Run it in the project directory, the one with
// vela.config.json and go.mod.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/elauven/vela/app"
	"github.com/elauven/vela/app/internal/gen"
)

const usage = `usage: vela-app [-C dir] <command>

commands:
  gen               generate src/_app and the Go route table from src/routes
  watch             regenerate on changes of the routes, the mirrored Go tree
                    and the matchers
  scaffold [route]  create the missing load.go files in the mirrored Go tree,
                    for one route (its id, such as /users/[id=int]) or all
  version           print the version
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("vela-app", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dir := flags.String("C", ".", "project directory")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(stderr, "vela-app: %v\n\n%s", err, usage)
		return 2
	}
	args = flags.Args()
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	command, rest := args[0], args[1:]
	switch command {
	case "version", "--version", "-version":
		fmt.Fprintln(stdout, "vela-app "+app.Version)
		return 0
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return 0
	case "gen":
		if len(rest) != 0 {
			break
		}
		return generate(*dir, stdout, stderr)
	case "watch":
		if len(rest) != 0 {
			break
		}
		gen.Watch(ctx, *dir, 300*time.Millisecond, func(result gen.Result, diags []gen.Diagnostic) {
			report(result, diags, stdout, stderr)
		})
		return 0
	case "scaffold":
		if len(rest) > 1 {
			break
		}
		route := ""
		if len(rest) == 1 {
			route = rest[0]
		}
		created, diags := gen.Scaffold(*dir, route)
		for _, file := range created {
			fmt.Fprintln(stdout, "created "+file)
		}
		if len(diags) > 0 {
			printDiagnostics(diags, stderr)
			return 1
		}
		if len(created) == 0 {
			fmt.Fprintln(stdout, "every route has a load.go")
		}
		return 0
	default:
		fmt.Fprintf(stderr, "vela-app: unknown command %q\n\n%s", command, usage)
		return 2
	}
	fmt.Fprintf(stderr, "vela-app %s: unexpected arguments %q\n\n%s", command, rest, usage)
	return 2
}

func generate(dir string, stdout, stderr io.Writer) int {
	result, diags := gen.Generate(dir)
	if !report(result, diags, stdout, stderr) {
		return 1
	}
	return 0
}

// report prints the outcome of one generation and reports success.
func report(result gen.Result, diags []gen.Diagnostic, stdout, stderr io.Writer) bool {
	if len(diags) > 0 {
		printDiagnostics(diags, stderr)
		return false
	}
	for _, file := range result.Written {
		fmt.Fprintln(stdout, "wrote "+file)
	}
	for _, file := range result.Removed {
		fmt.Fprintln(stdout, "removed "+file)
	}
	if len(result.Written) == 0 && len(result.Removed) == 0 {
		fmt.Fprintln(stdout, "up to date")
	}
	return true
}

func printDiagnostics(diags []gen.Diagnostic, stderr io.Writer) {
	for _, d := range diags {
		fmt.Fprintln(stderr, d.String())
	}
	fmt.Fprintf(stderr, "vela-app: %d error(s)\n", len(diags))
}
