// Command vela-app generates the sources of a Vela App from its routes
// directory (docs/APP.md). The commands are implemented in task 074.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/elauven/vela/app"
)

const usage = `usage: vela-app <command>

commands:
  gen        generate src/_app and the Go route table from src/routes
  watch      regenerate on changes of the routes and the mirrored Go tree
  scaffold   create missing load.go files in the mirrored Go tree
  version    print the version
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version", "--version", "-version":
		fmt.Fprintln(stdout, "vela-app "+app.Version)
		return 0
	case "gen", "watch", "scaffold":
		fmt.Fprintf(stderr, "vela-app %s: not implemented yet (task 074)\n", args[0])
		return 1
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return 0
	}
	fmt.Fprintf(stderr, "vela-app: unknown command %q\n\n%s", args[0], usage)
	return 2
}
