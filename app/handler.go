package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elauven/vela/app/internal/protocol"
	"github.com/elauven/vela/app/internal/value"
	vela "github.com/elauven/vela/ssr_runtime"
	"github.com/elauven/vela/ssr_runtime/vite"
)

// Config configures a Handler.
type Config struct {
	// Routes is the generated route table, appgen.Routes().
	Routes Routes
	// Assets gives the client tags: vite.Dev in development, vite.Load of
	// the build manifest in production. Without it pages are not hydrated.
	Assets *vite.Assets
	// Manifest is the path of vela.manifest.json. Its static roots choose
	// static fragments for auto navigations (R-9); without it they get data.
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
	// CrossOrigin rejects cross-origin POSTs to actions (CSRF); nil uses
	// http.NewCrossOriginProtection(). Add trusted origins or bypass
	// patterns to it to allow other sites.
	CrossOrigin *http.CrossOriginProtection
	// OnError sees the errors of guards, loaders, actions and rendering,
	// and panics after recover, but not Redirect and Fail (G-5). The error
	// it returns replaces the original (sql.ErrNoRows → app.NotFound());
	// nil keeps it. It may run concurrently for the loaders of one request.
	OnError func(ctx context.Context, r *Request, err error) error
	// CacheStore keeps the responses of pages that declare Cache (task
	// 092); nil keeps them in memory (NewMemoryCache(0)).
	CacheStore CacheStore
	// Prerendered is the directory of the pages vela-app build prerendered
	// (task 092); "prerendered" when empty. Without it every page renders.
	Prerendered string
}

// Handler serves a Vela App (docs/APP.md): it matches the URL, runs the
// loaders of the chain and renders the generated app root in the document.
type Handler struct {
	// renderSizes maps a route id to its last root render size.
	renderSizes sync.Map
	routes      Routes
	assets      *vite.Assets
	shell       shell
	build       string
	tags        string
	defaults    Head
	logger      *slog.Logger
	dev         bool
	maxBytes    int
	csrf        *http.CrossOriginProtection
	onError     func(ctx context.Context, r *Request, err error) error
	// static holds the segment ids whose root mode is static in
	// vela.manifest.json (R-9); nil without a manifest.
	static map[string]bool
	// cache is the response cache (task 092); inflight the keys being
	// rendered again in the background.
	cache      CacheStore
	inflightMu sync.Mutex
	inflight   map[string]bool
	now        func() time.Time
	// prerendered is the directory of prerendered pages, "" for none.
	prerendered string
}

// New checks the configuration and returns the handler.
func New(cfg Config) (*Handler, error) {
	h := &Handler{routes: cfg.Routes, assets: cfg.Assets, defaults: cfg.Head, logger: cfg.Logger, dev: cfg.Dev, maxBytes: cfg.MaxBytes, build: cfg.BuildID, csrf: cfg.CrossOrigin, onError: cfg.OnError}
	if h.csrf == nil {
		h.csrf = http.NewCrossOriginProtection()
	}
	if h.logger == nil {
		h.logger = slog.Default()
	}
	if h.maxBytes <= 0 {
		h.maxBytes = 8 << 20
	}
	h.cache, h.inflight, h.now = cfg.CacheStore, map[string]bool{}, time.Now
	if h.cache == nil {
		h.cache = NewMemoryCache(0)
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
		if h.static, err = staticRoots(data); err != nil {
			return nil, fmt.Errorf("app: Manifest: %s: %w", cfg.Manifest, err)
		}
	}
	if cfg.Assets != nil {
		if cfg.Routes.Entry == "" {
			return nil, errors.New("app: Routes.Entry is empty; regenerate the route table with vela-app gen")
		}
		if h.tags, err = cfg.Assets.Tags(cfg.Routes.Entry); err != nil {
			return nil, fmt.Errorf("app: Assets: %w", err)
		}
	}
	if h.build == "" && cfg.Assets != nil {
		h.build = cfg.Assets.BuildID()
	}
	if h.build == "" {
		sum := sha256.Sum256([]byte(h.tags))
		h.build = base64.RawURLEncoding.EncodeToString(sum[:9])
	}
	if dir := os.Getenv(PrerenderEnv); dir != "" {
		// vela-app build runs the server to prerender (task 092).
		h.prerenderFromEnv(dir, os.Stdout)
	}
	h.prerendered = cfg.Prerendered
	if h.prerendered == "" {
		h.prerendered = "prerendered"
	}
	if info, err := os.Stat(h.prerendered); err != nil || !info.IsDir() {
		h.prerendered = ""
	}
	return h, nil
}

// checkRoutes rejects a table that cannot be served.
func checkRoutes(t Routes) error {
	if t.Root.Render == nil {
		return errors.New("app: Routes.Root.Render is nil; pass appgen.Routes()")
	}
	for _, route := range t.Routes {
		for _, l := range route.Chain {
			for _, k := range l.Deps.Keys {
				if !ValidDependencyKey(k) {
					return fmt.Errorf("app: level %s declares the dependency key %q; a key is 1 to 64 letters, digits, _ - . or :", l.ID, k)
				}
			}
		}
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
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	r, _ = h.withState(r)
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
	if r.Method == http.MethodPost && (notFound || len(route.Actions) == 0) {
		methodNotAllowed(w)
		return
	}
	if isNav && route.Render == RenderDocument {
		h.writeJSON(w, r, nil, protocol.Response{Type: protocol.TypeReload, Reason: protocol.ReasonDocument})
		return
	}
	if r.Method == http.MethodPost {
		h.serveAction(w, r, route, params, isNav, nav.Mode)
		return
	}
	if !notFound && h.servePrerendered(w, r, route, isNav, nav) {
		return
	}
	render := func(w http.ResponseWriter, r *http.Request) {
		h.serveRoute(w, r, route, params, notFound, isNav, nav)
	}
	if !notFound && route.Cache != nil {
		h.serveCached(w, r, route, render)
		return
	}
	render(w, r)
}

// serveRoute runs the guards and loaders of a GET or HEAD and writes the
// document or the navigation response.
func (h *Handler) serveRoute(w http.ResponseWriter, r *http.Request, route *Route, params map[string]string, notFound, isNav bool, nav protocol.Request) {
	// The guards of the chain, before any loader (G-2).
	stop := h.runGuards(r, route, params)
	var redirect *RedirectError
	if stop != nil && errors.As(stop.err, &redirect) {
		h.writeRedirect(w, r, isNav, redirect)
		return
	}
	o := h.load(r, route, params, notFound, loadOptions{nav: isNav, have: nav.Have, stop: stop})
	defer o.run.close()
	if isNav {
		h.writeData(w, r, o, nav.Mode)
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
	// form is the form prop of the page after a failed action, and status
	// the status of that failure (app.Fail).
	form   any
	status int
}

// loadOptions say how load runs a chain.
type loadOptions struct {
	nav  bool     // a data navigation: the levels from start
	have []string // X-Vela-Have
	// failure is the error of the page's action, which the page level
	// renders instead of running its loader.
	failure error
	// stop is the guard that stopped the request: its level renders the
	// error and the levels below it do not run (G-2).
	stop   *guardStop
	form   any
	status int
}

// load runs the loaders of a chain: all of them for a document, the levels
// from start for a data navigation (N-4). A URL no route matches renders
// the root error page below the root layouts.
func (h *Handler) load(r *http.Request, route *Route, params map[string]string, notFound bool, opts loadOptions) *outcome {
	if params == nil {
		params = map[string]string{}
	}
	o := &outcome{route: route, params: params, query: r.URL.Query()}
	o.keys = make([]string, len(route.Chain))
	for i := range route.Chain {
		o.keys[i] = segmentKey(&route.Chain[i], params, o.query)
	}
	o.run = newChainRun(r.Context(), route.Chain, Request{Params: params, Query: o.query, HTTP: r, Route: route.ID})
	o.run.mapErr = h.mapError
	switch {
	case opts.stop != nil:
		o.run.preset(opts.stop.level, opts.stop.err)
	case opts.failure != nil:
		// The action of the page failed: its level renders the error.
		o.run.preset(len(route.Chain)-1, opts.failure)
	}
	o.form, o.status = opts.form, opts.status
	var failed int
	if opts.nav {
		o.start = protocol.Start(opts.have, o.keys)
		failed = o.run.runFrom(o.start)
	} else {
		failed = o.run.runAll()
	}
	var failure error
	switch {
	case failed >= 0:
		failure = o.run.levels[failed].err
	case notFound && opts.stop == nil:
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
func (o *outcome) collect() ([]levelHead, http.Header) {
	var heads []levelHead
	header := http.Header{}
	for i, l := range o.run.levels[:o.rendered] {
		if !o.run.ran(i) {
			continue
		}
		heads = append(heads, levelHead{level: i, head: l.req.head})
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
		h.writeRedirect(w, r, false, o.redirect)
		return
	}
	heads, header := o.collect()
	status := http.StatusOK
	chain := make([]any, 0, o.rendered+1)
	var preload []string
	for i, l := range o.run.levels[:o.rendered] {
		entry := chainEntry{"id": l.level.ID, "key": o.keys[i], "data": l.view, entryDataJSON: l.data}
		if len(l.level.Deps.Keys) > 0 {
			entry["depends"] = l.level.Deps.Keys
		}
		if o.form != nil && i == len(o.run.levels)-1 {
			entry["form"] = o.form
		}
		chain = append(chain, entry)
	}
	if o.status != 0 && o.segError == nil {
		status = o.status
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
			"error": errorProp(o.segError),
		})
		preload = append(preload, o.errorID)
	}
	body, err := h.document(h.renderContext(r, o), o.route.ID, routeProp(routeInfo(r, o)), chain, mergeHead(h.defaults, heads), preload)
	if err != nil {
		err = h.mapError(r.Context(), &Request{Params: o.params, Query: o.query, HTTP: r, Route: o.route.ID}, err)
		var redirect *RedirectError
		var mapped *StatusError
		switch {
		case errors.As(err, &redirect):
			h.writeRedirect(w, r, false, redirect)
		case errors.As(err, &mapped):
			h.writePage(w, r, mapped.Status, nil, []byte(builtinErrorPage(mapped.Status, mapped.Message)))
		default:
			h.logger.Error("vela app: render failed", "path", r.URL.Path, "route", o.route.ID, "err", err)
			message := "Internal Error"
			if h.dev {
				message = err.Error()
			}
			h.writePage(w, r, http.StatusInternalServerError, nil, []byte(builtinErrorPage(http.StatusInternalServerError, message)))
		}
		return
	}
	h.writePage(w, r, status, header, body)
}

// writeData writes the data response of a navigation (N-5, N-6): the
// levels from start, an error as the last segment, a redirect as JSON.
func (h *Handler) writeData(w http.ResponseWriter, r *http.Request, o *outcome, mode protocol.Mode) {
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
		segments = append(segments, protocol.Segment{ID: l.level.ID, Key: o.keys[o.start+i], Depends: l.level.Deps.Keys, Data: l.data})
	}
	if o.segError != nil {
		segments = append(segments, protocol.Segment{ID: o.errorID, Key: o.errorKey(), Error: o.segError})
	}
	// The head is the whole merged head (R-6): the server knows the heads
	// of every level that may set one, kept or not.
	head := mergeHead(h.defaults, heads).protocol()
	route := routeInfo(r, o)
	resp := protocol.Response{Type: protocol.TypeData, Route: &route, Start: o.start, Segments: segments, Head: head}
	if fragment, static := h.policy(o, mode); fragment {
		html, err := h.fragment(h.renderContext(r, o), o, route, static)
		if err != nil {
			h.logger.Error("vela app: fragment render failed", "path", r.URL.Path, "route", o.route.ID, "err", err)
			h.writeJSON(w, r, nil, protocol.Response{Type: protocol.TypeReload, Reason: protocol.ReasonDocument})
			return
		}
		resp.Type, resp.Fragment = protocol.TypeFragment, &protocol.Fragment{HTML: html, Static: static}
		if static {
			// A static level holds no data on the client.
			for i := range resp.Segments {
				resp.Segments[i].Data = nil
			}
		}
	}
	h.writeJSON(w, r, header, resp)
}

// writeJSON writes a navigation response: status 200 (N-6).
func (h *Handler) writeJSON(w http.ResponseWriter, r *http.Request, header http.Header, resp protocol.Response) {
	resp.Build = h.build
	for k, v := range header {
		w.Header()[k] = v
	}
	if h.finish(w, r) && (resp.Type == protocol.TypeData || resp.Type == protocol.TypeFragment) {
		// A user-dependent answer serves one navigation (G-4).
		resp.Cache = protocol.CacheOnce
	}
	body, err := protocol.Encode(resp)
	if err == nil && len(body) > h.maxBytes {
		err = fmt.Errorf("app: the response is larger than MaxBytes (%d)", h.maxBytes)
	}
	if err != nil {
		h.logger.Error("vela app: navigation response failed", "path", r.URL.Path, "err", err)
		for k := range header {
			if k != "Set-Cookie" {
				w.Header().Del(k)
			}
		}
		body, _ = protocol.Encode(protocol.Response{Type: protocol.TypeReload, Build: h.build, Reason: protocol.ReasonDocument})
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
	if errors.As(err, &panicked) && !panicked.logged {
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

// renderContext is the context of a page render: errors that a
// {#boundary} catches, and host errors that JavaScript code catches, go to
// the logger with the request path and route (task 091).
func (h *Handler) renderContext(r *http.Request, o *outcome) context.Context {
	return vela.WithErrorObserver(r.Context(), func(err error) {
		attrs := []any{"path", r.URL.Path, "route", o.route.ID}
		var caught *vela.BoundaryError
		if errors.As(err, &caught) {
			h.logger.Error("vela app: {#boundary} caught a render error", append(attrs, "source", caught.Source, "err", caught.Err)...)
			return
		}
		h.logger.Error("vela app: a caught host error", append(attrs, "err", err)...)
	})
}

// document renders the shell with the head and the app root (R-5).
func (h *Handler) document(ctx context.Context, routeID string, route map[string]any, chain []any, head mergedHead, preload []string) ([]byte, error) {
	// The root renders first: the <vela:head> tags of its components join
	// the head (task 091). Its buffer comes from a pool and starts at the
	// size of the route's last render, so a large page does not grow it
	// copy by copy.
	root := renderBuffers.Get().(*limitedBuffer)
	defer releaseRenderBuffer(root)
	root.max = h.maxBytes
	hint := h.renderSize(routeID)
	root.Grow(int(min(hint.Load(), int64(h.maxBytes))))
	var components vela.Head
	if err := h.routes.Root.Render(vela.WithHead(ctx, &components), root, RootProps{Route: route, Chain: chain}); err != nil {
		return nil, err
	}
	if root.err != nil {
		return nil, root.err
	}
	hint.Store(int64(root.Len()))
	out := &limitedBuffer{max: h.maxBytes}
	// The document is the root and a few kilobytes of head: one allocation.
	out.Grow(root.Len() + len(h.shell.beforeHead) + len(h.shell.betweenHeadBody) + len(h.shell.afterBody) + len(h.tags) + 4096)
	out.WriteString(h.shell.beforeHead)
	out.WriteString(head.html(components.Tags()))
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
	out.Write(root.Bytes())
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
	h.finish(w, r)
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
	if b.err != nil {
		return 0, b.err
	}
	if b.Len()+len(s) > b.max {
		b.err = fmt.Errorf("app: the response is larger than MaxBytes (%d)", b.max)
		return 0, b.err
	}
	return b.Buffer.WriteString(s)
}

// renderBuffers holds the buffers of root renders, which are copied into
// the document and then free again.
var renderBuffers = sync.Pool{New: func() any { return new(limitedBuffer) }}

// maxPooledBuffer keeps an unusually large render from staying in the pool.
const maxPooledBuffer = 8 << 20

func releaseRenderBuffer(b *limitedBuffer) {
	if b.Cap() > maxPooledBuffer {
		return
	}
	b.Reset()
	b.err = nil
	renderBuffers.Put(b)
}

// renderSize is the size of the last root render of a route.
func (h *Handler) renderSize(routeID string) *atomic.Int64 {
	if v, ok := h.renderSizes.Load(routeID); ok {
		return v.(*atomic.Int64)
	}
	v, _ := h.renderSizes.LoadOrStore(routeID, new(atomic.Int64))
	return v.(*atomic.Int64)
}

func methodNotAllowed(w http.ResponseWriter) {
	w.Header().Set("Allow", "GET, HEAD")
	http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
}

// serveAction runs the action of a POST to a page (task 078) and answers
// it. Without JavaScript: a success redirects with 303 to the page
// (post/redirect/get), app.Fail renders the page with its form prop and
// the status, another error renders the error page. With JavaScript
// (X-Vela-Navigate): a success is a data response with every level run
// again, a failure is {type: "form"}, a redirect is {type: "redirect"}.
func (h *Handler) serveAction(w http.ResponseWriter, r *http.Request, route *Route, params map[string]string, isNav bool, mode protocol.Mode) {
	w.Header().Set("Cache-Control", "no-store")
	if err := h.csrf.Check(r); err != nil {
		http.Error(w, "cross-origin request rejected", http.StatusForbidden)
		return
	}
	name := actionName(r.URL.RawQuery)
	// The page without the action: what the response shows.
	page := r.Clone(r.Context())
	page.URL.RawQuery = withoutAction(r.URL.RawQuery)
	page.Method = http.MethodGet
	// The guards run before the action, which never runs when one stops
	// the request (G-2).
	if stop := h.runGuards(page, route, params); stop != nil {
		var redirect *RedirectError
		if errors.As(stop.err, &redirect) {
			h.writeRedirect(w, r, isNav, redirect)
			return
		}
		h.answerAction(w, page, route, params, isNav, mode, loadOptions{stop: stop})
		return
	}
	var err error
	if action := route.Actions[name]; action == nil {
		err = Error(http.StatusNotFound, fmt.Sprintf("the page has no action %q", name))
	} else {
		err = h.runAction(r, route, params, action)
	}
	var redirect *RedirectError
	var fail *FailError
	switch {
	case errors.As(err, &redirect):
		h.writeRedirect(w, r, isNav, redirect)
	case errors.As(err, &fail):
		view, raw, verr := value.FromGo(fail.Value)
		if verr != nil {
			h.answerAction(w, page, route, params, isNav, mode, loadOptions{failure: fmt.Errorf("app: the value of Fail: %w", verr)})
			return
		}
		if isNav {
			h.writeJSON(w, r, nil, protocol.Response{Type: protocol.TypeForm, Status: fail.Status, Form: raw})
			return
		}
		if view == nil {
			// A failure without a value still sets the form prop.
			view = map[string]any{}
		}
		h.answerAction(w, page, route, params, false, mode, loadOptions{form: view, status: fail.Status})
	case err != nil:
		h.answerAction(w, page, route, params, isNav, mode, loadOptions{failure: err})
	case isNav:
		h.answerAction(w, page, route, params, true, mode, loadOptions{})
	default:
		target := page.URL.EscapedPath()
		if page.URL.RawQuery != "" {
			target += "?" + page.URL.RawQuery
		}
		w.Header().Set("Location", target)
		h.finish(w, r)
		w.WriteHeader(http.StatusSeeOther)
	}
}

// writeRedirect answers a redirect of a guard, loader or action (L-5): a
// navigation gets {type: "redirect"}, a document the status and Location.
func (h *Handler) writeRedirect(w http.ResponseWriter, r *http.Request, isNav bool, redirect *RedirectError) {
	if isNav {
		h.writeJSON(w, r, nil, protocol.Response{Type: protocol.TypeRedirect, Location: redirect.Location, Status: redirect.Status})
		return
	}
	w.Header().Set("Location", redirect.Location)
	if w.Header().Get("Cache-Control") == "" {
		w.Header().Set("Cache-Control", "private")
	}
	h.finish(w, r)
	w.WriteHeader(redirect.Status)
}

// answerAction renders the page after its action: the document, or a data
// response from level 0 (every loader runs again).
func (h *Handler) answerAction(w http.ResponseWriter, page *http.Request, route *Route, params map[string]string, isNav bool, mode protocol.Mode, opts loadOptions) {
	opts.nav = isNav
	o := h.load(page, route, params, false, opts)
	defer o.run.close()
	if isNav {
		h.writeData(w, page, o, mode)
		return
	}
	h.writeDocument(w, page, o)
}

// runAction runs an action with the page's request; Parent runs the
// layouts above it on demand. A panic is an error.
func (h *Handler) runAction(r *http.Request, route *Route, params map[string]string, action Action) (err error) {
	if params == nil {
		params = map[string]string{}
	}
	query, _ := url.ParseQuery(withoutAction(r.URL.RawQuery))
	run := newChainRun(r.Context(), route.Chain, Request{Params: params, Query: query, HTTP: r, Route: route.ID})
	defer run.close()
	page := run.levels[len(run.levels)-1]
	defer func() {
		if v := recover(); v != nil {
			err = &PanicError{Value: v, Stack: debug.Stack()}
		}
		err = h.mapError(page.ctx, page.req, err)
	}()
	_, err = action(page.ctx, page.req)
	return err
}
