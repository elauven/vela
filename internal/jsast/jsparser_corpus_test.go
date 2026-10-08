package jsast

// Optional, environment-driven checks against real code.  Both are skipped
// unless configured, so the normal `go test` run stays hermetic.
//
//	AST_CORPUS_DIR=/path/to/js   every .js/.mjs/.cjs file below must parse with
//	                             no diagnostics and no invariant violations
//	                             and WalkNode must visit every node
//	                             (point it at a trusted, valid code base such as
//	                             a node_modules directory or your own sources)
//
//	AST_DIFF_NODE=1              differential test against V8: real files are
//	  AST_CORPUS_DIR=…           randomly mutated (delete/insert punctuation,
//	                             truncate, duplicate) and the parser's verdict is
//	                             compared with `node --check`.  A "false positive"
//	                             (Node accepts, we report a diagnostic) fails the
//	                             test; "missed" errors are only logged, because
//	                             early errors and regex-pattern errors are known
//	                             gaps (see the Errors group of the matrix).

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func ctCorpusFiles(t *testing.T, maxSize int64) []string {
	t.Helper()
	dir := os.Getenv("AST_CORPUS_DIR")
	if dir == "" {
		t.Skip("set AST_CORPUS_DIR to run")
	}
	var files []string
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		switch filepath.Ext(p) {
		case ".js", ".mjs", ".cjs":
			if maxSize == 0 || info.Size() <= maxSize {
				files = append(files, p)
			}
		}
		return nil
	})
	sort.Strings(files)
	if len(files) == 0 {
		t.Skipf("no JavaScript files under %s", dir)
	}
	return files
}

func TestCompatRealCorpus(t *testing.T) {
	files := ctCorpusFiles(t, 0)
	bad := 0
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		src := string(b)
		r := ctParse(src)
		var problems []string
		switch {
		case r.hang:
			problems = append(problems, "parser did not terminate")
		case r.panic != nil:
			problems = append(problems, fmt.Sprintf("panic: %v", r.panic))
		default:
			for _, d := range r.diags {
				problems = append(problems, d.Message)
			}
			problems = append(problems, ctInvariants(src, r.prog)...)
			// WalkNode (rewriter.go) must reach every node of every real file.
			problems = append(problems, ctWalkProblems(src)...)
		}
		if len(problems) > 0 {
			bad++
			if bad <= 20 {
				t.Errorf("%s: %d problem(s), first: %s", f, len(problems), problems[0])
			}
		}
	}
	t.Logf("parsed %d files, %d with problems", len(files), bad)
}

// nodeAccepts reports whether V8 accepts src as CommonJS or as an ES module.
// Explicit .cjs/.mjs extensions matter: for .js files Node's module
// auto-detection makes `node --check` succeed on broken ESM.
func nodeAccepts(dir, src string) bool {
	c, m := filepath.Join(dir, "mut.cjs"), filepath.Join(dir, "mut.mjs")
	os.WriteFile(c, []byte(src), 0o644)
	os.WriteFile(m, []byte(src), 0o644)
	return exec.Command("node", "--check", c).Run() == nil || exec.Command("node", "--check", m).Run() == nil
}

func TestCompatDifferentialAgainstNode(t *testing.T) {
	if os.Getenv("AST_DIFF_NODE") == "" {
		t.Skip("set AST_DIFF_NODE=1 (and AST_CORPUS_DIR) to run")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	files := ctCorpusFiles(t, 15000)
	tmp := t.TempDir()
	rng := rand.New(rand.NewSource(7))
	const punct = "(){}[];,.:?=+-*/<>!&|`'\""
	falsePos, missed, agree := 0, 0, 0
	for i := 0; i < 700; i++ {
		b, err := os.ReadFile(files[rng.Intn(len(files))])
		if err != nil || len(b) < 50 {
			continue
		}
		s := string(b)
		at := rng.Intn(len(s))
		switch rng.Intn(4) {
		case 0:
			for j := at; j < len(s); j++ {
				if strings.IndexByte(punct, s[j]) >= 0 {
					s = s[:j] + s[j+1:]
					break
				}
			}
		case 1:
			s = s[:at] + string(punct[rng.Intn(len(punct))]) + s[at:]
		case 2:
			s = s[:at]
		case 3:
			end := at + rng.Intn(20)
			if end > len(s) {
				end = len(s)
			}
			s = s[:end] + s[at:end] + s[end:]
		}
		r := ctParse(s)
		if r.hang || r.panic != nil {
			t.Fatalf("parser crashed on a mutated input (hang=%v panic=%v)", r.hang, r.panic)
		}
		ours, theirs := len(r.diags) == 0, nodeAccepts(tmp, s)
		switch {
		case theirs && !ours:
			falsePos++
			t.Errorf("false positive: Node accepts this input but we report %q\n%s", r.diags[0].Message, ctShorten(s))
		case !theirs && ours:
			missed++
		default:
			agree++
		}
	}
	t.Logf("agree=%d  false-positives=%d  missed-errors=%d", agree, falsePos, missed)
}
