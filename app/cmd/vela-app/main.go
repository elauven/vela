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
	"strings"
	"time"

	"github.com/elauven/vela/app"
	"github.com/elauven/vela/app/internal/dev"
	"github.com/elauven/vela/app/internal/gen"
)

const usage = `usage: vela-app [-C dir] <command>

commands:
  gen               generate src/_app and the Go route table from src/routes
  watch             regenerate on changes of the routes, the mirrored Go tree
                    and the matchers
  scaffold [route]  create the missing load.go files in the mirrored Go tree,
                    for one route (its id, such as /users/[id=int]) or all
  dev [flags]       the development loop: watch, vela --watch, the Go server
                    restarted after each good build, and Vite
      -server cmd   the server command (go run ./cmd/server -dev http://localhost:5173)
      -vite cmd     the Vite command (npx vite); "" runs no Vite
      -vela cmd     the compiler in watch mode ($VELA_BINARY or vela, --watch)
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
	case "dev":
		return runDev(ctx, *dir, rest, stdout, stderr)
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

// runDev runs `vela-app dev`. A command flag is split at spaces.
func runDev(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) int {
	opts := dev.Defaults()
	flags := flag.NewFlagSet("vela-app dev", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	server := flags.String("server", strings.Join(opts.Server, " "), "")
	vite := flags.String("vite", strings.Join(opts.Vite, " "), "")
	vela := flags.String("vela", strings.Join(opts.Vela, " "), "")
	if err := flags.Parse(args); err != nil || flags.NArg() > 0 {
		fmt.Fprintf(stderr, "vela-app dev: bad arguments %q\n\n%s", args, usage)
		return 2
	}
	opts.Dir, opts.Stdout = dir, stdout
	opts.Server, opts.Vite, opts.Vela = strings.Fields(*server), strings.Fields(*vite), strings.Fields(*vela)
	if err := dev.Run(ctx, opts); err != nil {
		fmt.Fprintf(stderr, "vela-app dev: %v\n", err)
		return 1
	}
	return 0
}
