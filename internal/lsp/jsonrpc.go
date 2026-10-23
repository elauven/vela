// Package lsp is the Vela language server (task 065): the Language Server
// Protocol over stdio, on top of the compile pipeline of internal/compiler.
// The editor talks to it alone; JSON-RPC and the protocol types are
// implemented here without dependencies.
package lsp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
)

// JSON-RPC error codes of the specification and of LSP.
const (
	codeParseError           = -32700
	codeInvalidRequest       = -32600
	codeMethodNotFound       = -32601
	codeInvalidParams        = -32602
	codeInternalError        = -32603
	codeServerNotInitialized = -32002
	codeRequestCancelled     = -32800
)

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return e.Message }

// message is a request (ID and Method), a notification (Method) or a
// response (ID, and Result or Error).
type message struct {
	ID     json.RawMessage
	Method string
	Params json.RawMessage
	Result json.RawMessage
	Error  *rpcError
}

func (m *message) UnmarshalJSON(data []byte) error {
	var raw struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
		Result  json.RawMessage `json:"result"`
		Error   *rpcError       `json:"error"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.JSONRPC != "2.0" {
		return fmt.Errorf("jsonrpc is %q, want 2.0", raw.JSONRPC)
	}
	if string(raw.ID) == "null" {
		raw.ID = nil
	}
	*m = message{ID: raw.ID, Method: raw.Method, Params: raw.Params, Result: raw.Result, Error: raw.Error}
	return nil
}

func (m *message) MarshalJSON() ([]byte, error) {
	out := map[string]any{"jsonrpc": "2.0"}
	if m.ID != nil {
		out["id"] = m.ID
	}
	switch {
	case m.Method != "":
		out["method"] = m.Method
		if m.Params != nil {
			out["params"] = m.Params
		}
	case m.Error != nil:
		out["error"] = m.Error
	default:
		// A response always has a result, null included.
		result := m.Result
		if result == nil {
			result = json.RawMessage("null")
		}
		out["result"] = result
	}
	return json.Marshal(out)
}

// conn reads and writes messages framed by a Content-Length header.
type conn struct {
	in  *bufio.Reader
	mu  sync.Mutex
	out io.Writer
}

func newConn(in io.Reader, out io.Writer) *conn {
	return &conn{in: bufio.NewReader(in), out: out}
}

// read returns the next message; io.EOF at the end of the input. A frame
// whose body is not a JSON-RPC message is an *rpcError.
func (c *conn) read() (*message, error) {
	header, err := textproto.NewReader(c.in).ReadMIMEHeader()
	if err != nil {
		if errors.Is(err, io.EOF) && len(header) == 0 {
			return nil, io.EOF
		}
		return nil, err
	}
	length, err := strconv.Atoi(strings.TrimSpace(header.Get("Content-Length")))
	if err != nil || length < 0 {
		return nil, fmt.Errorf("invalid Content-Length %q", header.Get("Content-Length"))
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(c.in, body); err != nil {
		return nil, err
	}
	m := &message{}
	if err := json.Unmarshal(body, m); err != nil {
		return nil, &rpcError{Code: codeParseError, Message: err.Error()}
	}
	return m, nil
}

func (c *conn) write(m *message) error {
	body, err := json.Marshal(m)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := fmt.Fprintf(c.out, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err = c.out.Write(body)
	return err
}

func (c *conn) notify(method string, params any) error {
	data, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return c.write(&message{Method: method, Params: data})
}

func (c *conn) reply(id json.RawMessage, result any, err error) error {
	if err != nil {
		var rpc *rpcError
		if !errors.As(err, &rpc) {
			rpc = &rpcError{Code: codeInternalError, Message: err.Error()}
		}
		return c.write(&message{ID: id, Error: rpc})
	}
	data, merr := json.Marshal(result)
	if merr != nil {
		return c.write(&message{ID: id, Error: &rpcError{Code: codeInternalError, Message: merr.Error()}})
	}
	return c.write(&message{ID: id, Result: data})
}
