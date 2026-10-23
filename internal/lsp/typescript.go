package lsp

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

// velaEnv declares the runes and the compiler's modules for TypeScript, and
// the helpers of the render function of a virtual document.
const velaEnv = `declare function $state<T>(initial: T): T;
declare function $state<T = undefined>(): T | undefined;
declare namespace $state {
  function deep<T>(initial: T): T;
}
declare function $derived<T>(expression: T): T;
declare function $effect(fn: () => void | (() => void)): void;
declare function $bindable<T>(fallback?: T): T;
declare function $$dispatch(name: string, detail?: any): void;
declare function $$each<T>(items: Iterable<T> | ArrayLike<T> | null | undefined): Iterable<T>;
declare function $$awaited<T>(promise: T): Awaited<T>;
declare const $$error: any;
declare function $$on<K extends keyof HTMLElementEventMap>(event: K, handler: (event: HTMLElementEventMap[K]) => any): void;
declare function $$on(event: string, handler: (event: Event) => any): void;
declare function $$handler(handler: (event: CustomEvent) => any): void;
declare module 'vela/store' {
  export interface Readable<T> { subscribe(run: (value: T) => void): () => void }
  export interface Writable<T> extends Readable<T> { set(value: T): void; update(fn: (value: T) => T): void }
  export function writable<T>(value: T): Writable<T>;
  export function readable<T>(value: T, start?: (set: (value: T) => void) => void | (() => void)): Readable<T>;
  export function derived<T>(stores: Readable<any> | Readable<any>[], fn: (values: any, set: (value: T) => void) => T | void, initial?: T): Readable<T>;
}
declare module 'vela' {
  export function onMount(fn: () => void | (() => void) | Promise<void>): void;
  export function onDestroy(fn: () => void): void;
  export function tick(): Promise<void>;
  export function mount(component: any, options: { target: Element; props?: Record<string, any>; slots?: Record<string, any> }): any;
  export function warn(message: string): void;
  export { writable, readable, derived } from 'vela/store';
}
`

// tsClient runs the TypeScript helper: one node process, requests in
// order, responses by id.
type tsClient struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	version string
	checkJs bool

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

// startTypeScript starts the helper for the workspace root. tsdk is a
// TypeScript the editor provides, or "".
func startTypeScript(root, tsdk string) (*tsClient, error) {
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
	c := &tsClient{cmd: cmd, stdin: stdin, pending: map[int]chan tsResponse{}, sent: map[string]string{}}
	go c.read(stdout, &stderr)
	var started struct {
		TypeScript string `json:"typescript"`
		CheckJs    bool   `json:"checkJs"`
	}
	if err := c.call("start", map[string]any{"root": root, "tsdk": tsdk, "env": velaEnv}, &started); err != nil {
		c.close()
		return nil, err
	}
	c.version, c.checkJs = started.TypeScript, started.CheckJs
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

func (c *tsClient) read(stdout io.Reader, stderr *strings.Builder) {
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

func (c *tsClient) close() {
	if c == nil {
		return
	}
	// The helper exits at the end of its input; one that does not is killed.
	_ = c.stdin.Close()
	if p := c.cmd.Process; p != nil {
		time.AfterFunc(2*time.Second, func() { _ = p.Kill() })
	}
}

// call sends a request and decodes its result into out.
func (c *tsClient) call(method string, params, out any) error {
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
	case <-time.After(20 * time.Second):
		return fmt.Errorf("the TypeScript helper did not answer %s", method)
	}
}

// update sends the virtual documents the helper does not have yet.
func (c *tsClient) update(docs []*virtualDoc) error {
	type file struct {
		Path string `json:"path"`
		Text string `json:"text"`
	}
	var changed []file
	c.mu.Lock()
	for _, v := range docs {
		p := virtualPath(v.path)
		if c.sent[p] != v.text {
			changed = append(changed, file{p, v.text})
		}
	}
	c.mu.Unlock()
	if len(changed) == 0 {
		return nil
	}
	if err := c.call("update", map[string]any{"files": changed}, nil); err != nil {
		return err
	}
	c.mu.Lock()
	for _, f := range changed {
		c.sent[f.Path] = f.Text
	}
	c.mu.Unlock()
	return nil
}

// tsSpan is a location the helper returns: a UTF-16 span of file, with its
// line and character positions when TypeScript has the file.
type tsSpan struct {
	File         string `json:"file"`
	Start        int    `json:"start"`
	Length       int    `json:"length"`
	Line         *int   `json:"line"`
	Character    int    `json:"character"`
	EndLine      int    `json:"endLine"`
	EndCharacter int    `json:"endCharacter"`
	Prefix       string `json:"prefix"`
	Suffix       string `json:"suffix"`
}
