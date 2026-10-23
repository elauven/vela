package lsp

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// client is an LSP client over an in-process server: the same framing as
// stdio, without an editor.
type client struct {
	t      *testing.T
	conn   *conn
	done   chan error
	mu     sync.Mutex
	nextID int
	waits  map[string]chan *message
	notes  chan *message
}

func startClient(t *testing.T, root string) *client {
	t.Helper()
	t.Chdir(root)
	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	c := &client{t: t, conn: newConn(clientIn, clientOut), done: make(chan error, 1), waits: map[string]chan *message{}, notes: make(chan *message, 256)}
	go func() {
		c.done <- Serve(serverIn, serverOut)
		serverOut.Close()
	}()
	go func() {
		for {
			m, err := c.conn.read()
			if err != nil {
				close(c.notes)
				return
			}
			if m.Method != "" {
				c.notes <- m
				continue
			}
			c.mu.Lock()
			ch := c.waits[string(m.ID)]
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
		}
	}()
	t.Cleanup(func() {
		clientOut.Close()
		select {
		case <-c.done:
		case <-time.After(5 * time.Second):
			t.Error("the server did not stop at the end of the input")
		}
	})
	return c
}

// call sends a request and returns its response.
func (c *client) call(method string, params any) *message {
	c.t.Helper()
	c.mu.Lock()
	c.nextID++
	id := json.RawMessage(strings.TrimSpace(string(mustJSON(c.nextID))))
	ch := make(chan *message, 1)
	c.waits[string(id)] = ch
	c.mu.Unlock()
	if err := c.conn.write(&message{ID: id, Method: method, Params: mustJSON(params)}); err != nil {
		c.t.Fatal(err)
	}
	select {
	case m := <-ch:
		return m
	case <-time.After(20 * time.Second):
		c.t.Fatalf("no response to %s", method)
		return nil
	}
}

// result sends a request and decodes its result into v.
func (c *client) result(method string, params, v any) {
	c.t.Helper()
	m := c.call(method, params)
	if m.Error != nil {
		c.t.Fatalf("%s: %v", method, m.Error)
	}
	if err := json.Unmarshal(m.Result, v); err != nil {
		c.t.Fatalf("%s: %v in %s", method, err, m.Result)
	}
}

func (c *client) notify(method string, params any) {
	c.t.Helper()
	if err := c.conn.write(&message{Method: method, Params: mustJSON(params)}); err != nil {
		c.t.Fatal(err)
	}
}

// diagnostics waits for the diagnostics of uri that satisfy ok.
func (c *client) diagnostics(uri string, ok func(PublishDiagnosticsParams) bool) PublishDiagnosticsParams {
	c.t.Helper()
	timeout := time.After(20 * time.Second)
	for {
		select {
		case m, open := <-c.notes:
			if !open {
				c.t.Fatal("the server closed the connection")
			}
			if m.Method != "textDocument/publishDiagnostics" {
				continue
			}
			var p PublishDiagnosticsParams
			if err := json.Unmarshal(m.Params, &p); err != nil {
				c.t.Fatal(err)
			}
			if p.URI == uri && ok(p) {
				return p
			}
		case <-timeout:
			c.t.Fatalf("no matching diagnostics for %s", uri)
		}
	}
}

func mustJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

// initialize initializes the server without TypeScript: the Vela features
// alone (initializeTS adds it).
func (c *client) initialize(root string) {
	c.t.Helper()
	c.initializeWith(root, map[string]any{"enable": false})
}

func (c *client) initializeWith(root string, typescript map[string]any) {
	c.t.Helper()
	var res struct {
		Capabilities map[string]any `json:"capabilities"`
	}
	c.result("initialize", map[string]any{"processId": nil, "rootUri": pathToURI(root), "initializationOptions": map[string]any{"diagnosticsDelay": 0, "typescript": typescript}}, &res)
	if res.Capabilities["definitionProvider"] != true {
		c.t.Fatalf("capabilities %v", res.Capabilities)
	}
	c.notify("initialized", map[string]any{})
}

// open opens the file at path with its text on disk.
func (c *client) open(path string) (uri, text string) {
	c.t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		c.t.Fatal(err)
	}
	uri = pathToURI(path)
	c.notify("textDocument/didOpen", map[string]any{"textDocument": TextDocumentItem{URI: uri, LanguageID: "vela", Version: 1, Text: string(data)}})
	return uri, string(data)
}

// at is the position of the nth (0-based) occurrence of needle in src,
// plus delta bytes.
func at(t *testing.T, src, needle string, nth, delta int) Position {
	t.Helper()
	off := -1
	for i := 0; i <= nth; i++ {
		j := strings.Index(src[off+1:], needle)
		if j < 0 {
			t.Fatalf("no occurrence %d of %q", nth, needle)
		}
		off += 1 + j
	}
	return newText(src).position(off + delta)
}

func params(uri string, p Position) TextDocumentPositionParams {
	return TextDocumentPositionParams{TextDocument: TextDocumentIdentifier{URI: uri}, Position: p}
}

func fixture(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata", "lsp", "app"))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestLifecycle(t *testing.T) {
	root := fixture(t)
	c := startClient(t, root)
	if m := c.call("textDocument/hover", params("file:///x.vela", Position{})); m.Error == nil || m.Error.Code != codeServerNotInitialized {
		t.Errorf("before initialize: %+v", m)
	}
	c.initialize(root)
	if m := c.call("initialize", map[string]any{}); m.Error == nil {
		t.Errorf("second initialize: %+v", m)
	}
	if m := c.call("workspace/symbol", map[string]any{}); m.Error == nil || m.Error.Code != codeMethodNotFound {
		t.Errorf("unknown method: %+v", m)
	}
	if m := c.call("shutdown", nil); m.Error != nil || string(m.Result) != "null" {
		t.Errorf("shutdown: %+v", m)
	}
	c.notify("exit", nil)
	if err := <-c.done; err != nil {
		t.Errorf("exit after shutdown: %v", err)
	}
	c.done <- nil // for the cleanup

	c = startClient(t, root)
	c.initialize(root)
	c.notify("exit", nil)
	if err := <-c.done; !errors.Is(err, ErrExitWithoutShutdown) {
		t.Errorf("exit without shutdown: %v", err)
	}
	c.done <- nil
}

func TestDiagnosticsFollowTheBuffer(t *testing.T) {
	root := fixture(t)
	c := startClient(t, root)
	c.initialize(root)
	uri, src := c.open(filepath.Join(root, "src", "Card.vela"))
	if d := c.diagnostics(uri, func(PublishDiagnosticsParams) bool { return true }); len(d.Diagnostics) != 0 {
		t.Fatalf("Card.vela: %+v", d.Diagnostics)
	}

	// An incremental change that breaks the component, then one that
	// restores it.
	pos := at(t, src, "{title}", 0, 1)
	c.notify("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": uri, "version": 2},
		"contentChanges": []map[string]any{{"range": Range{Start: pos, End: Position{pos.Line, pos.Character + len("title")}}, "text": "titel"}},
	})
	d := c.diagnostics(uri, func(p PublishDiagnosticsParams) bool { return p.Version != nil && *p.Version == 2 })
	if len(d.Diagnostics) != 1 || !strings.Contains(d.Diagnostics[0].Message, `"titel"`) || d.Diagnostics[0].Range != (Range{Start: pos, End: Position{pos.Line, pos.Character + 5}}) || d.Diagnostics[0].Source != "vela" {
		t.Fatalf("broken: %+v", d.Diagnostics)
	}
	c.notify("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": uri, "version": 3},
		"contentChanges": []map[string]any{{"text": src}},
	})
	if d := c.diagnostics(uri, func(p PublishDiagnosticsParams) bool { return p.Version != nil && *p.Version == 3 }); len(d.Diagnostics) != 0 {
		t.Fatalf("restored: %+v", d.Diagnostics)
	}
	c.notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": uri}})
	if d := c.diagnostics(uri, func(p PublishDiagnosticsParams) bool { return p.Version == nil }); len(d.Diagnostics) != 0 {
		t.Fatalf("closed: %+v", d.Diagnostics)
	}
}

// TestSSRChecksOnSave: saving a component checks the SSR project of
// vela.config.json; a file that is not open gets its SSR diagnostics.
func TestSSRChecksOnSave(t *testing.T) {
	root := fixture(t)
	c := startClient(t, root)
	c.initialize(root)
	uri, _ := c.open(filepath.Join(root, "src", "App.vela"))
	c.diagnostics(uri, func(PublishDiagnosticsParams) bool { return true })
	c.notify("textDocument/didSave", map[string]any{"textDocument": map[string]any{"uri": uri}})
	clock := pathToURI(filepath.Join(root, "src", "Clock.vela"))
	d := c.diagnostics(clock, func(PublishDiagnosticsParams) bool { return true })
	if len(d.Diagnostics) != 1 || d.Diagnostics[0].Code != "VELA-SSR-EXPR" || d.Diagnostics[0].Range.Start != (Position{1, 19}) {
		t.Fatalf("Clock.vela: %+v", d.Diagnostics)
	}
}

func location(t *testing.T, c *client, uri string, p Position) Location {
	t.Helper()
	// A Location, or a list of them (TypeScript's).
	var raw json.RawMessage
	c.result("textDocument/definition", params(uri, p), &raw)
	var locs []Location
	if strings.HasPrefix(string(raw), "[") {
		if err := json.Unmarshal(raw, &locs); err != nil {
			t.Fatal(err)
		}
	} else if string(raw) != "null" {
		var loc Location
		if err := json.Unmarshal(raw, &loc); err != nil {
			t.Fatal(err)
		}
		locs = append(locs, loc)
	}
	if len(locs) == 0 {
		t.Fatalf("no definition at %+v", p)
	}
	return locs[0]
}

func TestDefinition(t *testing.T) {
	root := fixture(t)
	c := startClient(t, root)
	c.initialize(root)
	uri, src := c.open(filepath.Join(root, "src", "App.vela"))
	card := pathToURI(filepath.Join(root, "src", "Card.vela"))
	cardSrc, err := os.ReadFile(filepath.Join(root, "src", "Card.vela"))
	if err != nil {
		t.Fatal(err)
	}
	// The location covers target, found as the text after context in
	// targetSrc; no target is the start of a file.
	for _, tc := range []struct {
		name                       string
		pos                        Position
		uri                        string
		targetSrc, context, target string
	}{
		{"component tag", at(t, src, "<Card", 0, 2), card, "", "", ""},
		{"import specifier", at(t, src, "./Card.vela", 0, 3), card, "", "", ""},
		{"prop", at(t, src, ":title", 0, 3), card, string(cardSrc), "export let ", "title"},
		{"bindable prop", at(t, src, "bind:value", 0, 6), card, string(cardSrc), "export let ", "value"},
		{"event", at(t, src, "@on:close", 0, 5), card, string(cardSrc), "$$dispatch(", "'close'"},
		{"template name", at(t, src, "{count}", 0, 2), uri, src, "let ", "count"},
		{"each item", at(t, src, "item.title", 0, 1), uri, src, "items as ", "item"},
		{"script write", at(t, src, "open = false", 0, 1), uri, src, "let ", "open"},
		{"handler", at(t, src, "{close}", 0, 2), uri, src, "function ", "close"},
	} {
		loc := location(t, c, uri, tc.pos)
		if loc.URI != tc.uri {
			t.Errorf("%s: %s, want %s", tc.name, loc.URI, tc.uri)
			continue
		}
		want := Range{}
		if tc.target != "" {
			start := at(t, tc.targetSrc, tc.context+tc.target, 0, len(tc.context))
			want = Range{Start: start, End: Position{start.Line, start.Character + len(tc.target)}}
		}
		if loc.Range != want {
			t.Errorf("%s: %+v, want %q at %+v", tc.name, loc.Range, tc.target, want)
		}
	}
}

func hoverText(t *testing.T, c *client, uri string, p Position) string {
	t.Helper()
	var h *Hover
	c.result("textDocument/hover", params(uri, p), &h)
	if h == nil {
		return ""
	}
	return h.Contents.Value
}

func TestHover(t *testing.T) {
	root := fixture(t)
	c := startClient(t, root)
	c.initialize(root)
	uri, src := c.open(filepath.Join(root, "src", "App.vela"))
	for _, tc := range []struct {
		name string
		pos  Position
		want []string
	}{
		{"derived in the template", at(t, src, "{count}", 0, 2), []string{"let count = $derived(items.length);", "derived ($derived)"}},
		{"deep state in a block", at(t, src, "{#each items", 0, 8), []string{"deep state ($state.deep)"}},
		{"state in the script", at(t, src, "open = false", 0, 1), []string{"let open = $state(false);", "state ($state)"}},
		{"each item", at(t, src, "item.title", 0, 1), []string{"each item of items"}},
		{"component", at(t, src, "<Card", 0, 2), []string{"**Card** — Card.vela", "`title` — `export let title = 'Untitled';`", "`value` (bindable)", "`@on:close`"}},
		{"prop", at(t, src, ":title", 0, 3), []string{"export let title = 'Untitled';", "prop of Card"}},
		{"import", at(t, src, "{ Card }", 0, 3), []string{"import from './Card.vela'"}},
	} {
		got := hoverText(t, c, uri, tc.pos)
		for _, want := range tc.want {
			if !strings.Contains(got, want) {
				t.Errorf("%s: %q misses %q", tc.name, got, want)
			}
		}
	}
	if got := hoverText(t, c, uri, at(t, src, "<main>", 0, 0)); got != "" {
		t.Errorf("text: %q", got)
	}
}

func labels(t *testing.T, c *client, uri string, p Position) map[string]CompletionItem {
	t.Helper()
	var list CompletionList
	c.result("textDocument/completion", params(uri, p), &list)
	out := map[string]CompletionItem{}
	for _, item := range list.Items {
		out[item.Label] = item
	}
	return out
}

// edit replaces the document with text, as version.
func (c *client) edit(uri string, version int, text string) {
	c.notify("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": uri, "version": version},
		"contentChanges": []map[string]any{{"text": text}},
	})
}

func TestCompletion(t *testing.T) {
	root := fixture(t)
	c := startClient(t, root)
	c.initialize(root)
	uri, src := c.open(filepath.Join(root, "src", "App.vela"))
	check := func(name string, items map[string]CompletionItem, want, unwanted []string) {
		t.Helper()
		for _, w := range want {
			if _, ok := items[w]; !ok {
				t.Errorf("%s: no %q in %v", name, w, keys(items))
			}
		}
		for _, u := range unwanted {
			if _, ok := items[u]; ok {
				t.Errorf("%s: unexpected %q", name, u)
			}
		}
	}
	check("component attributes", labels(t, c, uri, at(t, src, ":title", 0, 0)), []string{":title", "bind:value", "@on:close", ":value"}, []string{"bind:title", "@click"})
	check("script", labels(t, c, uri, at(t, src, "let open", 0, 0)), []string{"$state", "$state.deep", "$derived", "$effect", "$bindable", "items", "close"}, nil)
	check("expression", labels(t, c, uri, at(t, src, "{count}", 0, 1)), []string{"count", "items", "open", "close", "Card"}, []string{"item"})
	check("expression in a block", labels(t, c, uri, at(t, src, "item.title", 0, 0)), []string{"item", "i", "count"}, nil)

	// Replaces from with to in the buffer and completes at the | of to.
	complete := func(version int, from, to string) (map[string]CompletionItem, string) {
		t.Helper()
		marked := strings.Replace(src, from, to, 1)
		p := at(t, marked, "|", 0, 0)
		edited := strings.Replace(marked, "|", "", 1)
		c.edit(uri, version, edited)
		return labels(t, c, uri, p), edited
	}
	items, _ := complete(2, "    <p>{count}</p>", "    <|")
	check("tag name", items, []string{"Card", "svelte:fragment"}, nil)

	items, _ = complete(3, "<p>{count}</p>", "<p |>{count}</p>")
	check("element attributes", items, []string{"@click", ":", "bind:value", "class:", "style:"}, []string{":title"})

	items, edited := complete(4, "<p>{count}</p>", "<p>{#e|}</p>")
	check("block", items, []string{"#each", "#if", "/each"}, nil)
	if item := items["#each"]; item.TextEdit == nil || item.TextEdit.Range != (Range{Start: at(t, edited, "{#e}", 0, 0), End: at(t, edited, "}</p>", 0, 1)}) || !strings.HasPrefix(item.TextEdit.NewText, "{#each ") || item.FilterText != "{#each" {
		t.Errorf("block edit %+v", item.TextEdit)
	}

	items, _ = complete(5, "<p>{count}</p>", "<p>{|}</p>")
	check("after an opening brace", items, []string{"#if", "#each", "count"}, nil)

	// A broken template keeps the script names; a broken script keeps the
	// names of its last complete version.
	items, _ = complete(6, "<p>{count}</p>", "<p>{coun|</p><div")
	check("broken template", items, []string{"count", "items"}, nil)
	broken := strings.Replace(src, "function close(event) {", "function close(event) {\n    let x = ;", 1)
	broken = strings.Replace(broken, "<p>{count}</p>", "<p>{|}</p>", 1)
	p := at(t, broken, "|", 0, 0)
	c.edit(uri, 7, strings.Replace(broken, "|", "", 1))
	if items := labels(t, c, uri, p); items["count"].Label == "" || items["open"].Label == "" {
		t.Errorf("broken script: %v", keys(items))
	}
}

func keys(m map[string]CompletionItem) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestDashboardExample: navigation and hovers of the dashboard example,
// whose components import each other through `./X.js`.
func TestDashboardExample(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "examples", "dashboard"))
	if err != nil {
		t.Fatal(err)
	}
	c := startClient(t, root)
	c.initialize(root)
	uri, src := c.open(filepath.Join(root, "Dashboard.vela"))
	c.diagnostics(uri, func(PublishDiagnosticsParams) bool { return true })
	sidebar := pathToURI(filepath.Join(root, "Sidebar.vela"))
	if loc := location(t, c, uri, at(t, src, "<Sidebar", 0, 3)); loc.URI != sidebar {
		t.Errorf("<Sidebar>: %s", loc.URI)
	}
	if loc := location(t, c, uri, at(t, src, "@navigate", 0, 3)); loc.URI != sidebar || loc.Range.Start.Line == 0 {
		t.Errorf("@navigate: %+v", loc)
	}
	if got := hoverText(t, c, uri, at(t, src, ":activePage", 0, 2)); !strings.Contains(got, "export let activePage = 'overview';") {
		t.Errorf(":activePage: %q", got)
	}
	if got := hoverText(t, c, uri, at(t, src, "sidebarOpen ?", 0, 1)); !strings.Contains(got, "bindable prop") {
		t.Errorf("sidebarOpen: %q", got)
	}
	if items := labels(t, c, uri, at(t, src, ":items", 0, 0)); items[":items"].Label == "" || items["@on:close"].Label == "" || items["@on:markAllRead"].Label == "" {
		t.Errorf("NotificationPanel attributes: %v", keys(items))
	}
}
