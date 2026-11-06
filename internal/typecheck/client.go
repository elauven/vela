package typecheck

import (
	"github.com/elauven/vela/runtime"

	"bufio"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed ts_helper.mjs
var tsHelperSource []byte

// Client runs the TypeScript helper: one node process, requests in
// order, responses by id.
type Client struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	// Version is the TypeScript the helper loaded; CheckJs is set when the
	// project's configuration checks JavaScript; Config is the tsconfig.json
	// or jsconfig.json it read, "" for none.
	Version string
	CheckJs bool
	Config  string
	// Warnings are the settings of Config that contradict TS-10.2.
	Warnings []ConfigWarning

	mu      sync.Mutex
	nextID  int
	pending map[int]chan tsResponse
	dead    error
	sent    map[string]string // virtual path → text the helper has
}

type tsResponse struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error"`
}

// helperFile writes the embedded helper to the user cache once per content.
func helperFile() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	sum := sha256.Sum256(tsHelperSource)
	file := filepath.Join(dir, "vela", "ts-helper-"+runtime.Version+"-"+hex.EncodeToString(sum[:4])+".mjs")
	if data, err := os.ReadFile(file); err == nil && string(data) == string(tsHelperSource) {
		return file, nil
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return "", err
	}
	tmp := fmt.Sprintf("%s.%d", file, os.Getpid())
	if err := os.WriteFile(tmp, tsHelperSource, 0o644); err != nil {
		return "", err
	}
	return file, os.Rename(tmp, file)
}

// ConfigWarning is a compiler option of the project that contradicts the
// configuration of TS-10.2, and the value it wants.
type ConfigWarning struct {
	Option string `json:"option"`
	Want   string `json:"want"`
}

// Options of the helper.
type Options struct {
	// Tsdk is a TypeScript directory the editor or `--tsdk` names, or "".
	Tsdk string
	// Project adds the files of the project's tsconfig.json to the program,
	// for a type check of the project (`vela check --types`); the language
	// server leaves them to the files it opens.
	Project bool
}

// Start starts the helper for the workspace root.
func Start(root string, opts Options) (*Client, error) {
	node, err := exec.LookPath("node")
	if err != nil {
		return nil, errors.New("node is not on PATH")
	}
	file, err := helperFile()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(node, file)
	cmd.Dir = root
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr strings.Builder
	cmd.Stderr = &limitedWriter{w: &stderr, n: 4096}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &Client{cmd: cmd, stdin: stdin, pending: map[int]chan tsResponse{}, sent: map[string]string{}}
	go c.read(stdout, &stderr)
	var started struct {
		TypeScript string          `json:"typescript"`
		CheckJs    bool            `json:"checkJs"`
		Config     string          `json:"config"`
		Warnings   []ConfigWarning `json:"warnings"`
	}
	params := map[string]any{"root": root, "tsdk": opts.Tsdk, "ambient": runtime.AmbientDeclarations(), "ambientSpecifier": runtime.AmbientTypesSpecifier, "helpers": RenderHelpers, "project": opts.Project}
	if err := c.Call("start", params, &started); err != nil {
		c.Close()
		return nil, err
	}
	c.Version, c.CheckJs, c.Config, c.Warnings = started.TypeScript, started.CheckJs, started.Config, started.Warnings
	return c, nil
}

type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n > 0 {
		k := min(len(p), l.n)
		l.n -= k
		_, _ = l.w.Write(p[:k])
	}
	return len(p), nil
}

func (c *Client) read(stdout io.Reader, stderr *strings.Builder) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1<<20), 64<<20)
	for scanner.Scan() {
		var res tsResponse
		if json.Unmarshal(scanner.Bytes(), &res) != nil {
			continue
		}
		c.mu.Lock()
		ch := c.pending[res.ID]
		delete(c.pending, res.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- res
		}
	}
	err := c.cmd.Wait()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dead = fmt.Errorf("the TypeScript helper exited (%v): %s", err, strings.TrimSpace(stderr.String()))
	for id, ch := range c.pending {
		ch <- tsResponse{ID: id, Error: c.dead.Error()}
		delete(c.pending, id)
	}
}

func (c *Client) Close() {
	if c == nil {
		return
	}
	// The helper exits at the end of its input; one that does not is killed.
	_ = c.stdin.Close()
	if p := c.cmd.Process; p != nil {
		time.AfterFunc(2*time.Second, func() { _ = p.Kill() })
	}
}

// Call sends a request and decodes its result into out.
func (c *Client) Call(method string, params, out any) error {
	return c.CallTimeout(method, params, out, 20*time.Second)
}

// CallTimeout is Call with its own time limit, for a check of a project.
func (c *Client) CallTimeout(method string, params, out any, timeout time.Duration) error {
	c.mu.Lock()
	if c.dead != nil {
		err := c.dead
		c.mu.Unlock()
		return err
	}
	c.nextID++
	id := c.nextID
	ch := make(chan tsResponse, 1)
	c.pending[id] = ch
	line, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err == nil {
		_, err = c.stdin.Write(append(line, '\n'))
	}
	c.mu.Unlock()
	if err != nil {
		return err
	}
	select {
	case res := <-ch:
		if res.Error != "" {
			return errors.New(res.Error)
		}
		if out != nil && len(res.Result) > 0 {
			return json.Unmarshal(res.Result, out)
		}
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("the TypeScript helper did not answer %s", method)
	}
}

// Update sends the virtual documents the helper does not have yet.
func (c *Client) Update(docs []*Document) error {
	type file struct {
		Path string `json:"path"`
		Text string `json:"text"`
	}
	var changed []file
	c.mu.Lock()
	for _, v := range docs {
		p := v.VirtualPath()
		if c.sent[p] != v.Text {
			changed = append(changed, file{p, v.Text})
		}
	}
	c.mu.Unlock()
	if len(changed) == 0 {
		return nil
	}
	if err := c.Call("update", map[string]any{"files": changed}, nil); err != nil {
		return err
	}
	c.mu.Lock()
	for _, f := range changed {
		c.sent[f.Path] = f.Text
	}
	c.mu.Unlock()
	return nil
}
