package lsp

import (
	"github.com/elauven/vela/internal/compiler"
	"github.com/elauven/vela/internal/diag"
	"github.com/elauven/vela/runtime"

	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"time"
)

// ErrExitWithoutShutdown is the result of Serve when the client sent exit
// without shutdown; the process exits with 1.
var ErrExitWithoutShutdown = errors.New("exit without shutdown")

// document is an open .vela document.
type document struct {
	uri, path string
	version   int
	text      *text
	// current analyzes the current version; lastScript and lastTemplate are
	// the last analyses whose script or template parsed without errors, for
	// the names a broken section misses.
	current, lastScript, lastTemplate *component
	timer                             *time.Timer
}

type server struct {
	conn    *conn
	delay   time.Duration
	project *project

	mu        sync.Mutex
	docs      map[string]*document
	ssr       map[string][]Diagnostic // by pathKey: the SSR diagnostics of the last project check
	ssrPaths  map[string]string       // the files of ssr
	checking  bool                    // a project check runs
	recheck   bool                    // a save arrived during it
	ready     bool                    // initialize answered
	shutdown  bool
	checkDone func() // test hook: a project check finished

	ts       *tsClient     // the TypeScript helper, nil without one
	tsReady  chan struct{} // closed when the helper started or failed
	virtuals map[string]*virtualDoc
}

// Serve runs the language server on in and out until exit or the end of
// the input. It changes the working directory to the workspace root on
// initialize: the compiler resolves vela.config.json, scope classes and
// diagnostic paths from it, as `vela` run in the root does.
func Serve(in io.Reader, out io.Writer) error {
	s := &server{conn: newConn(in, out), delay: 150 * time.Millisecond, docs: map[string]*document{}, ssr: map[string][]Diagnostic{}, virtuals: map[string]*virtualDoc{}}
	defer func() {
		if s.tsReady != nil {
			<-s.tsReady
			s.ts.close()
		}
	}()
	return s.run()
}

func (s *server) run() error {
	for {
		m, err := s.conn.read()
		if errors.Is(err, io.EOF) {
			return nil
		}
		var rpc *rpcError
		if errors.As(err, &rpc) {
			_ = s.conn.reply(nil, nil, rpc)
			continue
		}
		if err != nil {
			return err
		}
		if m.Method == "" {
			continue // a response; the server sends no requests
		}
		if m.Method == "exit" {
			if s.shutdown {
				return nil
			}
			return ErrExitWithoutShutdown
		}
		if m.ID == nil {
			s.notification(m)
			continue
		}
		result, err := s.request(m)
		if err := s.conn.reply(m.ID, result, err); err != nil {
			return err
		}
	}
}

func decode(params json.RawMessage, v any) error {
	if err := json.Unmarshal(params, v); err != nil {
		return &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	return nil
}

func (s *server) request(m *message) (any, error) {
	if m.Method == "initialize" {
		return s.initialize(m.Params)
	}
	if !s.ready {
		return nil, &rpcError{Code: codeServerNotInitialized, Message: "the server is not initialized"}
	}
	if s.shutdown {
		return nil, &rpcError{Code: codeInvalidRequest, Message: "the server is shut down"}
	}
	switch m.Method {
	case "shutdown":
		s.shutdown = true
		return nil, nil
	case "completionItem/resolve":
		var item CompletionItem
		if err := decode(m.Params, &item); err != nil {
			return nil, err
		}
		return s.resolveCompletion(item), nil
	case "textDocument/definition", "textDocument/hover", "textDocument/completion", "textDocument/references", "textDocument/rename", "textDocument/signatureHelp":
		var p RenameParams
		if err := decode(m.Params, &p); err != nil {
			return nil, err
		}
		doc, off := s.at(p.TextDocumentPositionParams)
		if doc == nil {
			return nil, nil
		}
		switch m.Method {
		case "textDocument/definition":
			return s.definition(doc, off), nil
		case "textDocument/hover":
			return s.hover(doc, off), nil
		case "textDocument/references":
			return s.references(doc, off), nil
		case "textDocument/rename":
			return s.rename(doc, off, p.NewName)
		case "textDocument/signatureHelp":
			return s.signatureHelp(doc, off), nil
		default:
			return s.completion(doc, off), nil
		}
	}
	return nil, &rpcError{Code: codeMethodNotFound, Message: "method not found: " + m.Method}
}

func (s *server) initialize(params json.RawMessage) (any, error) {
	if s.ready {
		return nil, &rpcError{Code: codeInvalidRequest, Message: "initialize was already sent"}
	}
	var p InitializeParams
	if err := decode(params, &p); err != nil {
		return nil, err
	}
	if d := p.InitializationOptions.DiagnosticsDelay; d != nil && *d >= 0 {
		s.delay = time.Duration(*d) * time.Millisecond
	}
	root := uriToPath(p.RootURI)
	if root == "" && len(p.WorkspaceFolders) > 0 {
		root = uriToPath(p.WorkspaceFolders[0].URI)
	}
	if root == "" {
		root = p.RootPath
	}
	if root != "" {
		if err := os.Chdir(root); err != nil {
			s.log(1, fmt.Sprintf("vela-lsp: cannot use the workspace root %s: %v", root, err))
		}
	}
	service, err := compiler.NewCompilerService("", nil)
	if err != nil {
		// Without the configuration components still compile with the
		// defaults; the problem is shown once.
		s.log(1, "vela-lsp: "+err.Error())
		cwd, _ := os.Getwd()
		service = &compiler.CompilerService{Cwd: cwd}
	}
	s.project = &project{service: service, disk: map[string]diskEntry{}}
	if enable := p.InitializationOptions.TypeScript.Enable; enable == nil || *enable {
		s.startTypeScript(service.Cwd, p.InitializationOptions.TypeScript.TSDK)
	}
	s.ready = true
	return map[string]any{
		"capabilities": map[string]any{
			"positionEncoding":      "utf-16",
			"textDocumentSync":      map[string]any{"openClose": true, "change": 2, "save": map[string]any{"includeText": false}},
			"definitionProvider":    true,
			"hoverProvider":         true,
			"referencesProvider":    true,
			"renameProvider":        true,
			"signatureHelpProvider": map[string]any{"triggerCharacters": []string{"(", ","}},
			"completionProvider":    map[string]any{"triggerCharacters": []string{"<", ":", "@", "{", "#", "/", "."}, "resolveProvider": true},
		},
		"serverInfo": map[string]any{"name": "vela-lsp", "version": runtime.Version},
	}, nil
}

// log sends window/logMessage; typ 1 is an error, 3 information.
func (s *server) log(typ int, msg string) {
	_ = s.conn.notify("window/logMessage", LogMessageParams{Type: typ, Message: msg})
}

func (s *server) notification(m *message) {
	if !s.ready || s.shutdown {
		return
	}
	switch m.Method {
	case "textDocument/didOpen":
		var p DidOpenTextDocumentParams
		if decode(m.Params, &p) != nil || !isVela(p.TextDocument.URI) {
			return
		}
		doc := &document{uri: p.TextDocument.URI, path: uriToPath(p.TextDocument.URI), version: p.TextDocument.Version, text: newText(p.TextDocument.Text)}
		s.mu.Lock()
		s.docs[doc.uri] = doc
		s.mu.Unlock()
		s.schedule(doc.uri, 0)
	case "textDocument/didChange":
		var p DidChangeTextDocumentParams
		if decode(m.Params, &p) != nil {
			return
		}
		s.mu.Lock()
		doc := s.docs[p.TextDocument.URI]
		if doc != nil {
			for _, change := range p.ContentChanges {
				doc.text = doc.text.apply(change.Range, change.Text)
			}
			doc.version, doc.current = p.TextDocument.Version, nil
		}
		s.mu.Unlock()
		if doc != nil {
			s.schedule(doc.uri, s.delay)
		}
	case "textDocument/didClose":
		var p DidCloseTextDocumentParams
		if decode(m.Params, &p) != nil {
			return
		}
		s.mu.Lock()
		doc := s.docs[p.TextDocument.URI]
		delete(s.docs, p.TextDocument.URI)
		ssr := s.ssr[pathKey(uriToPath(p.TextDocument.URI))]
		s.mu.Unlock()
		if doc != nil {
			if doc.timer != nil {
				doc.timer.Stop()
			}
			// A closed file keeps only the diagnostics of the project check.
			s.send(p.TextDocument.URI, nil, ssr)
		}
	case "textDocument/didSave":
		var p DidSaveTextDocumentParams
		if decode(m.Params, &p) == nil && isVela(p.TextDocument.URI) {
			s.checkProject()
		}
	}
}

func isVela(uri string) bool {
	return strings.EqualFold(filepath.Ext(uriToPath(uri)), ".vela")
}

// at is the open document and byte offset of a position.
func (s *server) at(p TextDocumentPositionParams) (*document, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc := s.docs[p.TextDocument.URI]
	if doc == nil {
		return nil, 0
	}
	return doc, doc.text.offset(p.Position)
}

// componentOf is the analysis of the document's current version.
func (s *server) componentOf(doc *document) *component {
	s.mu.Lock()
	current, t := doc.current, doc.text
	s.mu.Unlock()
	if current != nil {
		return current
	}
	c := s.project.analyze(doc.path, t.src)
	s.mu.Lock()
	defer s.mu.Unlock()
	if doc.text == t {
		doc.current = c
		if c.analysis.ScriptComplete {
			doc.lastScript = c
		}
		if c.analysis.TemplateComplete {
			doc.lastTemplate = c
		}
	}
	return c
}

// openComponent is the analysis of a component: the open document of its
// path, else the file.
func (s *server) openComponent(path string) *component {
	s.mu.Lock()
	var doc *document
	key := pathKey(path)
	for _, d := range s.docs {
		if pathKey(d.path) == key {
			doc = d
		}
	}
	s.mu.Unlock()
	if doc != nil {
		return s.componentOf(doc)
	}
	return s.project.file(path)
}

// schedule publishes the diagnostics of the document after delay; a later
// change reschedules them.
func (s *server) schedule(uri string, delay time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc := s.docs[uri]
	if doc == nil {
		return
	}
	if doc.timer != nil {
		doc.timer.Stop()
	}
	version := doc.version
	doc.timer = time.AfterFunc(delay, func() { s.publish(uri, version) })
}

// publish compiles version of the document and publishes its diagnostics,
// unless the document changed meanwhile.
func (s *server) publish(uri string, version int) {
	s.mu.Lock()
	doc := s.docs[uri]
	if doc == nil || doc.version != version {
		s.mu.Unlock()
		return
	}
	path, src := doc.path, doc.text
	s.mu.Unlock()

	source := src.src
	res := s.project.service.Compile(compiler.ServiceRequest{Path: path, Source: &source})
	var diags []Diagnostic
	for _, d := range res.Diagnostics {
		diags = append(diags, fromMachine(d))
	}
	if len(diags) == 0 && res.Error != "" && !res.OK {
		diags = append(diags, Diagnostic{Severity: SeverityError, Source: "vela", Message: res.Error})
	}
	if c := s.componentOf(doc); c.text.src == source {
		diags = append(diags, s.tsDiagnostics(c)...)
	}

	s.mu.Lock()
	doc = s.docs[uri]
	current := doc != nil && doc.version == version
	ssr := s.ssr[pathKey(path)]
	s.mu.Unlock()
	if current {
		s.send(uri, &version, append(diags, ssr...))
	}
}

func (s *server) send(uri string, version *int, diags []Diagnostic) {
	if diags == nil {
		diags = []Diagnostic{}
	}
	_ = s.conn.notify("textDocument/publishDiagnostics", PublishDiagnosticsParams{URI: uri, Version: version, Diagnostics: diags})
}

func fromMachine(d diag.MachineDiagnostic) Diagnostic {
	pos := func(line, col int) Position { return Position{Line: max(line-1, 0), Character: max(col-1, 0)} }
	return Diagnostic{
		Range:    Range{Start: pos(d.Line, d.Column), End: pos(d.EndLine, d.EndColumn)},
		Severity: severity(d.Severity),
		Code:     d.Code,
		Source:   "vela",
		Message:  diagnosticMessage(d.Message, d.Suggestion),
	}
}

func diagnosticMessage(message, suggestion string) string {
	if suggestion != "" {
		return message + "\n" + suggestion
	}
	return message
}

func severity(s string) DiagnosticSeverity {
	switch s {
	case "error":
		return SeverityError
	case "warning":
		return SeverityWarning
	}
	return SeverityHint
}

// checkProject runs the vela check of the configured project after a save
// when the project enables SSR: the SSR diagnostics need the whole project
// (component links, translated modules, hosts), so they come from the saved
// files. One check runs at a time; saves during it run one more.
func (s *server) checkProject() {
	target, ok, err := s.project.service.ProjectCheck()
	if err != nil {
		s.log(1, "vela-lsp: "+err.Error())
		return
	}
	if !ok || !target.SSR {
		return
	}
	s.mu.Lock()
	if s.checking {
		s.recheck = true
		s.mu.Unlock()
		return
	}
	s.checking = true
	s.mu.Unlock()
	go func() {
		for {
			s.runCheck(target)
			s.mu.Lock()
			again := s.recheck
			s.recheck = false
			if !again {
				s.checking = false
				done := s.checkDone
				s.mu.Unlock()
				if done != nil {
					done()
				}
				return
			}
			s.mu.Unlock()
		}
	}()
}

func (s *server) runCheck(target compiler.CheckTarget) {
	diags, _, err := target.Run()
	if err != nil {
		s.log(1, "vela-lsp: "+err.Error())
		return
	}
	found := map[string][]Diagnostic{}
	paths := map[string]string{}
	for _, d := range diags {
		// The client checks of every component come from its buffer.
		if !strings.HasPrefix(d.Code, "VELA-SSR") || d.File == "" {
			continue
		}
		path, err := filepath.Abs(filepath.FromSlash(d.File))
		if err != nil {
			continue
		}
		key := pathKey(path)
		paths[key] = path
		found[key] = append(found[key], fromMachine(d.Machine()))
	}
	s.mu.Lock()
	for key := range s.ssr {
		if _, ok := paths[key]; !ok {
			paths[key] = s.ssrPaths[key]
		}
	}
	s.ssr, s.ssrPaths = found, paths
	// Open documents are published with their buffer's diagnostics, under
	// the URI the client gave them.
	open := map[string]string{}
	for _, doc := range s.docs {
		open[pathKey(doc.path)] = doc.uri
	}
	s.mu.Unlock()
	for key, path := range paths {
		if uri, ok := open[key]; ok {
			s.schedule(uri, 0)
		} else {
			s.send(pathToURI(path), nil, found[key])
		}
	}
}

// pathKey identifies a file: its clean absolute path, without case on
// Windows and macOS, whose file systems ignore it by default.
func pathKey(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if goruntime.GOOS == "windows" || goruntime.GOOS == "darwin" {
		return strings.ToLower(path)
	}
	return path
}
