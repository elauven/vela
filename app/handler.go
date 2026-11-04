package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/elauven/vela/app/internal/protocol"
	"github.com/elauven/vela/ssr_runtime/vite"
)

// Config configures a Handler.
type Config struct {
	// Routes is the generated route table, appgen.Routes().
	Routes Routes
	// Assets gives the client tags: vite.Dev in development, vite.Load of
	// the build manifest in production. Without it pages are not hydrated.
	Assets *vite.Assets
	// Manifest is the path of vela.manifest.json (the root modes of task
	// 077). It is checked when set.
	Manifest string
	// Document is the path of a document shell with %vela.head% and
	// %vela.body% (R-4); the built-in shell is used when it is empty.
	Document string
	// BuildID identifies the client build (task 078). The default is a hash
	// of the client tags, which name the hashed files of a build.
	BuildID string
	// Head holds the default title and meta tags (R-6).
	Head Head
	// Logger receives loader and render errors; slog.Default() when nil.
	Logger *slog.Logger
	// Dev shows the messages of internal errors on the error page. In
	// production the page says only "Internal Error".
	Dev bool
	// MaxBytes limits the size of a rendered response; 8 MiB when 0.
	MaxBytes int
}

// Handler serves a Vela App (docs/APP.md): it matches the URL, runs the
// loaders of the chain and renders the generated app root in the document.
type Handler struct {
	routes   Routes
	assets   *vite.Assets
	shell    shell
	build    string
	tags     string
	defaults Head
	logger   *slog.Logger
	dev      bool
	maxBytes int
	manifest json.RawMessage
}

// New checks the configuration and returns the handler.
func New(cfg Config) (*Handler, error) {
	h := &Handler{routes: cfg.Routes, assets: cfg.Assets, defaults: cfg.Head, logger: cfg.Logger, dev: cfg.Dev, maxBytes: cfg.MaxBytes, build: cfg.BuildID}
	if h.logger == nil {
		h.logger = slog.Default()
	}
	if h.maxBytes <= 0 {
		h.maxBytes = 8 << 20
	}
	if err := checkRoutes(cfg.Routes); err != nil {
		return nil, err
	}
	source, name := defaultShell, "the built-in document"
	if cfg.Document != "" {
		data, err := os.ReadFile(cfg.Document)
		if err != nil {
			return nil, fmt.Errorf("app: Document: %w", err)
		}
		source, name = string(data), cfg.Document
	}
	var err error
	if h.shell, err = parseShell(source, name); err != nil {
		return nil, err
	}
	if cfg.Manifest != "" {
		data, err := os.ReadFile(cfg.Manifest)
		if err != nil {
			return nil, fmt.Errorf("app: Manifest: %w", err)
		}
		if !json.Valid(data) {
			return nil, fmt.Errorf("app: Manifest: %s is not JSON", cfg.Manifest)
		}
		h.manifest = data
	}
	if cfg.Assets != nil {
		if cfg.Routes.Entry == "" {
			return nil, errors.New("app: Routes.Entry is empty; regenerate the route table with vela-app gen")
		}
		if h.tags, err = cfg.Assets.Tags(cfg.Routes.Entry); err != nil {
			return nil, fmt.Errorf("app: Assets: %w", err)
		}
	}
	if h.build == "" {
		sum := sha256.Sum256([]byte(h.tags))
		h.build = base64.RawURLEncoding.EncodeToString(sum[:9])
	}
	return h, nil
}

// checkRoutes rejects a table that cannot be served.
func checkRoutes(t Routes) error {
	if t.Root.Render == nil {
		return errors.New("app: Routes.Root.Render is nil; pass appgen.Routes()")
	}
	for _, p := range t.Patterns {
		if p.Route < 0 || p.Route >= len(t.Routes) {
			return fmt.Errorf("app: a pattern names route %d of %d", p.Route, len(t.Routes))
		}
		for _, s := range p.Segments {
			if s.Matcher != "" && t.Matchers[s.Matcher] == nil {
				return fmt.Errorf("app: route %s uses the unknown matcher %q", t.Routes[p.Route].ID, s.Matcher)
			}
		}
	}
	return nil
}

// BuildID is the id of the client build the handler serves.
func (h *Handler) BuildID() string { return h.build }

// ServeHTTP serves a document request, or a navigation request of the
// client router (R-8, N-2), which gets JSON (N-5).
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Vary", protocol.Vary)
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	nav, isNav, err := protocol.ParseRequest(r.Header)
	if isNav {
		switch {
		case err != nil:
			h.writeJSON(w, r, nil, protocol.Response{Type: protocol.TypeReload, Reason: protocol.ReasonProtocol})
			return
		case nav.Build != "" && nav.Build != h.build:
			h.writeJSON(w, r, nil, protocol.Response{Type: protocol.TypeReload, Reason: protocol.ReasonBuild})
			return
		}
	}
	path := r.URL.EscapedPath()
	if len(path) > 1 && strings.HasSuffix(path, "/") {
		// P-7: without the trailing slash, keeping the query. Leading
		// slashes collapse, so //host/ cannot redirect to another host.
		target := "/" + strings.TrimLeft(strings.TrimRight(path, "/"), "/")
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		if isNav {
			h.writeJSON(w, r, nil, protocol.Response{Type: protocol.TypeRedirect, Location: target, Status: http.StatusPermanentRedirect})
			return
		}
		w.Header().Set("Location", target)
		w.WriteHeader(http.StatusPermanentRedirect)
		return
	}
	route, params, notFound := &h.routes.NotFound, map[string]string(nil), true
	if match, ok := h.routes.Match(path); ok {
		route, params, notFound = match.Route, match.Params, false
	}
	if isNav && route.Render == RenderDocument {
		h.writeJSON(w, r, nil, protocol.Response{Type: protocol.TypeReload, Reason: protocol.ReasonDocument})
		return
	}
	// An explicit fragment request is answered with data until fragments
	// exist (task 077); the response type says what the client got.
	o := h.load(r, route, params, notFound, isNav, nav.Have)
	defer o.run.close()
	if isNav {
		h.writeData(w, r, o)
		return
	}
	h.writeDocument(w, r, o)
}

// outcome is a chain whose loaders ran.
type outcome struct {
	route  *Route
	params map[string]string
	query  url.Values
	keys   []string // the key of every level (L-6)
	run    *chainRun
	// start is the first level the response sends: 0 for a document,
	// the start of N-4 for a data response.
	start int
	// rendered is the number of levels rendered before the error segment,
	// or all of them.
	rendered int
	redirect *RedirectError
	// segError and errorID are the error segment (R-3); errorID is ""
	// when there is no Error.vela.
	segError *protocol.SegmentError
	errorID  string
}

// load runs the loaders of a chain: all of them for a document, the levels
// from start for a data navigation (N-4). A URL no route matches renders
// the root error page below the root layouts.
func (h *Handler) load(r *http.Request, route *Route, params map[string]string, notFound, nav bool, have []string) *outcome {
	if params == nil {
		params = map[string]string{}
	}
	o := &outcome{route: route, params: params, query: r.URL.Query()}
	o.keys = make([]string, len(route.Chain))
	for i := range route.Chain {
		o.keys[i] = segmentKey(&route.Chain[i], params, o.query)
	}
	o.run = newChainRun(r.Context(), route.Chain, Request{Params: params, Query: o.query, HTTP: r, Route: route.ID})
	var failed int
	if nav {
		o.start = protocol.Start(have, o.keys)
		failed = o.run.runFrom(o.start)
	} else {
		failed = o.run.runAll()
	}
	var failure error
	switch {
	case failed >= 0:
		failure = o.run.levels[failed].err
	case notFound:
		failed, failure = len(o.run.levels), NotFound()
	}
	o.rendered = len(o.run.levels)
	if failure == nil || errors.As(failure, &o.redirect) {
		return o
	}
	// The error page replaces the failed level and those below it; a kept
	// level that failed (on demand, or for its head) is sent again.
	o.rendered, o.start = failed, min(o.start, failed)
	o.segError = h.describe(r, route, failed, failure)
	o.errorID = h.routes.Error
	if failed < len(route.Chain) {
		o.errorID = route.Chain[failed].Error
	}
	return o
}

// collect gathers the heads and headers of the rendered levels whose
// loaders ran, in level order; a deeper level replaces a header.
func (o *outcome) collect() ([]Head, http.Header) {
	var heads []Head
	header := http.Header{}
	for i, l := range o.run.levels[:o.rendered] {
		if !o.run.ran(i) {
			continue
		}
		heads = append(heads, l.req.head)
		for k, v := range l.req.header {
			header[k] = v
		}
	}
	return heads, header
}

// errorKey is the key of an error segment.
func (o *outcome) errorKey() string {
	return protocol.SegmentKey(o.errorID, strconv.Itoa(o.segError.Status), o.segError.Message)
}

// writeDocument writes the document of a chain (R-5).
func (h *Handler) writeDocument(w http.ResponseWriter, r *http.Request, o *outcome) {
	if o.redirect != nil {
		w.Header().Set("Location", o.redirect.Location)
		w.Header().Set("Cache-Control", "private")
		w.WriteHeader(o.redirect.Status)
		return
	}
	heads, header := o.collect()
	status := http.StatusOK
	chain := make([]any, 0, o.rendered+1)
	var preload []string
	for i, l := range o.run.levels[:o.rendered] {
		chain = append(chain, map[string]any{"id": l.level.ID, "key": o.keys[i], "data": l.view})
	}
	if o.segError == nil && o.rendered > 0 {
		preload = append(preload, o.run.levels[o.rendered-1].level.ID)
	}
	if o.segError != nil {
		status = o.segError.Status
		if o.errorID == "" {
			h.writePage(w, r, status, header, []byte(builtinErrorPage(status, o.segError.Message)))
			return
		}
		chain = append(chain, map[string]any{
			"id":    o.errorID,
			"key":   o.errorKey(),
			"error": map[string]any{"status": float64(o.segError.Status), "message": o.segError.Message},
		})
		preload = append(preload, o.errorID)
	}
	body, err := h.document(r.Context(), routeProp(routeInfo(r, o)), chain, mergeHead(h.defaults, heads), preload)
	if err != nil {
		h.logger.Error("vela app: render failed", "path", r.URL.Path, "route", o.route.ID, "err", err)
		message := "Internal Error"
		if h.dev {
			message = err.Error()
		}
		h.writePage(w, r, http.StatusInternalServerError, nil, []byte(builtinErrorPage(http.StatusInternalServerError, message)))
		return
	}
	h.writePage(w, r, status, header, body)
}

// writeData writes the data response of a navigation (N-5, N-6): the
// levels from start, an error as the last segment, a redirect as JSON.
func (h *Handler) writeData(w http.ResponseWriter, r *http.Request, o *outcome) {
	if o.redirect != nil {
		h.writeJSON(w, r, nil, protocol.Response{Type: protocol.TypeRedirect, Location: o.redirect.Location, Status: o.redirect.Status})
		return
	}
	if o.segError != nil && o.errorID == "" {
		// The built-in error page is a document of its own.
		h.writeJSON(w, r, nil, protocol.Response{Type: protocol.TypeReload, Reason: protocol.ReasonDocument})
		return
	}
	heads, header := o.collect()
	segments := make([]protocol.Segment, 0, o.rendered-o.start+1)
	for i, l := range o.run.levels[o.start:o.rendered] {
		segments = append(segments, protocol.Segment{ID: l.level.ID, Key: o.keys[o.start+i], Data: l.data})
	}
	if o.segError != nil {
		segments = append(segments, protocol.Segment{ID: o.errorID, Key: o.errorKey(), Error: o.segError})
	}
	// The head is the whole merged head (R-6): the server knows the heads
	// of every level that may set one, kept or not.
	merged := mergeHead(h.defaults, heads)
	head := &protocol.Head{Title: merged.Title}
	for _, m := range merged.Meta {
		head.Meta = append(head.Meta, protocol.Meta{Name: m.Name, Property: m.Property, Content: m.Content})
	}
	route := routeInfo(r, o)
	h.writeJSON(w, r, header, protocol.Response{Type: protocol.TypeData, Route: &route, Start: o.start, Segments: segments, Head: head})
}

// writeJSON writes a navigation response: status 200 (N-6).
func (h *Handler) writeJSON(w http.ResponseWriter, r *http.Request, header http.Header, resp protocol.Response) {
	resp.Build = h.build
	body, err := protocol.Encode(resp)
	if err == nil && len(body) > h.maxBytes {
		err = fmt.Errorf("app: the response is larger than MaxBytes (%d)", h.maxBytes)
	}
	if err != nil {
		h.logger.Error("vela app: navigation response failed", "path", r.URL.Path, "err", err)
		header = nil
		body, _ = protocol.Encode(protocol.Response{Type: protocol.TypeReload, Build: h.build, Reason: protocol.ReasonDocument})
	}
	for k, v := range header {
		w.Header()[k] = v
	}
	if w.Header().Get("Cache-Control") == "" {
		w.Header().Set("Cache-Control", "private")
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// describe turns a loader error into the error of the error page (L-5),
// logging internal errors.
func (h *Handler) describe(r *http.Request, route *Route, failed int, err error) *protocol.SegmentError {
	var status *StatusError
	if errors.As(err, &status) {
		return &protocol.SegmentError{Status: status.Status, Message: status.Message}
	}
	level := ""
	if failed < len(route.Chain) {
		level = route.Chain[failed].ID
	}
	attrs := []any{"path", r.URL.Path, "route", route.ID, "level", level, "err", err}
	var panicked *PanicError
	if errors.As(err, &panicked) {
		attrs = append(attrs, "stack", string(panicked.Stack))
	}
	h.logger.Error("vela app: loader failed", attrs...)
	message := "Internal Error"
	if h.dev {
		message = err.Error()
	}
	return &protocol.SegmentError{Status: http.StatusInternalServerError, Message: message}
}

// segmentKey derives a level's key from its id and its dependencies (L-6,
// N-3): the parameters of its path and the declared query names.
func segmentKey(l *Level, params map[string]string, query map[string][]string) string {
	deps := make([]string, 0, len(l.Params)+len(l.Deps.Query))
	for _, p := range l.Params {
		if v, ok := params[p]; ok {
			deps = append(deps, p+"="+v)
		} else {
			deps = append(deps, p)
		}
	}
	for _, q := range l.Deps.Query {
		deps = append(deps, "?"+q+"="+strings.Join(query[q], "\x00"))
	}
	return protocol.SegmentKey(l.ID, deps...)
}

// routeInfo is the route of R-1 for the request.
func routeInfo(r *http.Request, o *outcome) protocol.Route {
	query := map[string]string{}
	for k, v := range o.query {
		if len(v) > 0 {
			query[k] = v[0]
		}
	}
	target := r.URL.EscapedPath()
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	return protocol.Route{ID: o.route.ID, URL: target, Path: r.URL.EscapedPath(), Params: o.params, Query: query}
}

// routeProp is the route prop of the server render: the route as the
// values JSON gives the client.
func routeProp(route protocol.Route) map[string]any {
	params := map[string]any{}
	for k, v := range route.Params {
		params[k] = v
	}
	query := map[string]any{}
	for k, v := range route.Query {
		query[k] = v
	}
	return map[string]any{"id": route.ID, "url": route.URL, "path": route.Path, "params": params, "query": query}
}

// document renders the shell with the head and the app root (R-5).
func (h *Handler) document(ctx context.Context, route map[string]any, chain []any, head Head, preload []string) ([]byte, error) {
	out := &limitedBuffer{max: h.maxBytes}
	out.WriteString(h.shell.beforeHead)
	out.WriteString(head.html())
	fmt.Fprintf(out, `<meta name="vela-build" content="%s">`, html.EscapeString(h.build))
	out.WriteString(h.tags)
	if h.assets != nil && len(preload) > 0 {
		modules := make([]string, len(preload))
		for i, id := range preload {
			modules[i] = strings.TrimPrefix(h.routes.Source+"/"+id+".vela", "/")
		}
		tags, err := h.assets.Preload([]string{h.routes.Entry}, modules...)
		if err != nil {
			h.logger.Warn("vela app: preload", "err", err)
		}
		out.WriteString(tags)
	}
	out.WriteString(h.shell.betweenHeadBody)
	fmt.Fprintf(out, `<div id="%s">`, RootID)
	if err := h.routes.Root.Render(ctx, out, RootProps{Route: route, Chain: chain}); err != nil {
		return nil, err
	}
	out.WriteString("</div>")
	out.WriteString(h.shell.afterBody)
	if out.err != nil {
		return nil, out.err
	}
	return out.Bytes(), nil
}

// writePage writes a buffered HTML response.
func (h *Handler) writePage(w http.ResponseWriter, r *http.Request, status int, header http.Header, body []byte) {
	for k, v := range header {
		w.Header()[k] = v
	}
	if w.Header().Get("Cache-Control") == "" {
		w.Header().Set("Cache-Control", "private")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// limitedBuffer is the response buffer (R-7): rendering fails once it
// grows past max.
type limitedBuffer struct {
	bytes.Buffer
	max int
	err error
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.err != nil {
		return 0, b.err
	}
	if b.Len()+len(p) > b.max {
		b.err = fmt.Errorf("app: the response is larger than MaxBytes (%d)", b.max)
		return 0, b.err
	}
	return b.Buffer.Write(p)
}

func (b *limitedBuffer) WriteString(s string) (int, error) {
	return b.Write([]byte(s))
}
