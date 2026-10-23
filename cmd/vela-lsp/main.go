// Command vela-lsp is the Vela language server: the Language Server
// Protocol over stdin and stdout (docs/EDITORS.md).
package main

import (
	"github.com/elauven/vela/internal/lsp"
	"github.com/elauven/vela/runtime"

	"errors"
	"fmt"
	"os"
)

func main() {
	for _, arg := range os.Args[1:] {
		switch arg {
		case "--stdio":
			// The only transport; accepted because clients pass it.
		case "--version", "-version":
			fmt.Println("vela-lsp " + runtime.Version)
			return
		default:
			fmt.Fprintf(os.Stderr, "usage: vela-lsp [--stdio]\nunknown argument %q\n", arg)
			os.Exit(2)
		}
	}
	if err := lsp.Serve(os.Stdin, os.Stdout); err != nil {
		if !errors.Is(err, lsp.ErrExitWithoutShutdown) {
			fmt.Fprintf(os.Stderr, "vela-lsp: %v\n", err)
		}
		os.Exit(1)
	}
}
