package main

// document_command.go — `vela document`: the virtual document of a
// component (internal/typecheck), the <script> and the template expressions
// as one JavaScript or TypeScript program, with the segments that map it
// back to the component. The ESLint processor of @govela/eslint-plugin
// (task 067) lints it.

import (
	"github.com/elauven/vela/internal/typecheck"

	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
)

// DocumentResult is the output of `vela document`. Offsets and lengths are
// UTF-16 units. A segment [src, virt, len] is text copied from the
// component; everything else in Text is generated.
type DocumentResult struct {
	Path       string   `json:"path"`
	TypeScript bool     `json:"typescript"`
	Text       string   `json:"text"`
	Segments   [][3]int `json:"segments"`
}

func runDocumentCommand(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vela document", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("path", "", "the component's path")
	useStdin := fs.Bool("stdin", false, "read the source from stdin instead of --path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *path == "" || fs.NArg() > 0 {
		fmt.Fprintln(stderr, "usage: vela document --path <file.vela> [--stdin]")
		return 2
	}
	var data []byte
	var err error
	if *useStdin {
		data, err = io.ReadAll(stdin)
	} else {
		data, err = os.ReadFile(*path)
	}
	if err != nil {
		fmt.Fprintf(stderr, "vela: %v\n", err)
		return 2
	}
	doc := typecheck.Build(*path, string(data))
	result := DocumentResult{Path: doc.VirtualPath(), TypeScript: doc.TypeScript, Text: doc.Text, Segments: [][3]int{}}
	for _, s := range doc.Segments() {
		result.Segments = append(result.Segments, [3]int{s.Src, s.Virt, s.Len})
	}
	line, err := json.Marshal(result)
	if err != nil {
		fmt.Fprintf(stderr, "vela: %v\n", err)
		return 2
	}
	fmt.Fprintf(stdout, "%s\n", line)
	return 0
}
