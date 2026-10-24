package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/textproto"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestBinaryOverStdio runs the built vela-lsp as an editor does: initialize,
// open an example component with an error, read its diagnostics, shutdown
// and exit with status 0.
func TestBinaryOverStdio(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "vela-lsp")
	if goruntime.GOOS == "windows" {
		binary += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	if out, err := exec.Command(binary, "--version").Output(); err != nil || !strings.HasPrefix(string(out), "vela-lsp ") {
		t.Fatalf("--version: %q %v", out, err)
	}
	root, err := filepath.Abs(filepath.Join("..", "..", "examples"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "--stdio")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()

	send := func(v map[string]any) {
		v["jsonrpc"] = "2.0"
		body, _ := json.Marshal(v)
		if _, err := fmt.Fprintf(stdin, "Content-Length: %d\r\n\r\n%s", len(body), body); err != nil {
			t.Fatal(err)
		}
	}
	messages := make(chan map[string]any, 64)
	go func() {
		defer close(messages)
		r := bufio.NewReader(stdout)
		for {
			header, err := textproto.NewReader(r).ReadMIMEHeader()
			if err != nil {
				return
			}
			n, _ := strconv.Atoi(header.Get("Content-Length"))
			body := make([]byte, n)
			if _, err := io.ReadFull(r, body); err != nil {
				return
			}
			var m map[string]any
			_ = json.Unmarshal(body, &m)
			messages <- m
		}
	}()
	next := func(ok func(map[string]any) bool) map[string]any {
		t.Helper()
		timeout := time.After(30 * time.Second)
		for {
			select {
			case m, open := <-messages:
				if !open {
					t.Fatal("the server closed stdout")
				}
				if ok(m) {
					return m
				}
			case <-timeout:
				t.Fatal("timeout")
			}
		}
	}
	response := func(id float64) func(map[string]any) bool {
		return func(m map[string]any) bool { return m["id"] == id }
	}

	rootURI := "file://" + filepath.ToSlash(root)
	if !strings.HasPrefix(filepath.ToSlash(root), "/") {
		rootURI = "file:///" + filepath.ToSlash(root)
	}
	send(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{"processId": nil, "rootUri": rootURI}})
	if m := next(response(1)); m["result"] == nil {
		t.Fatalf("initialize: %v", m)
	}
	send(map[string]any{"method": "initialized", "params": map[string]any{}})
	uri := rootURI + "/Counter.vela"
	send(map[string]any{"method": "textDocument/didOpen", "params": map[string]any{"textDocument": map[string]any{
		"uri": uri, "languageId": "vela", "version": 1, "text": "<template><p>{missing}</p></template>\n",
	}}})
	m := next(func(m map[string]any) bool { return m["method"] == "textDocument/publishDiagnostics" })
	diags := m["params"].(map[string]any)["diagnostics"].([]any)
	if len(diags) != 1 || !strings.Contains(diags[0].(map[string]any)["message"].(string), `"missing"`) {
		t.Fatalf("diagnostics: %v", m)
	}
	send(map[string]any{"id": 2, "method": "shutdown"})
	next(response(2))
	send(map[string]any{"method": "exit"})
	stdin.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("exit: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the server did not exit")
	}
}
