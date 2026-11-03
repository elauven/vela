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

// ServeHTTP serves a document request (R-8). Navigation requests (task 076)
// get a document too until the router exists.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Vary", protocol.Vary)
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	path := r.URL.EscapedPath()
	if len(path) > 1 && strings.HasSuffix(path, "/") {
		// P-7: without the trailing slash, keeping the query. Leading
		// slashes collapse, so //host/ cannot redirect to another host.
		target := "/" + strings.TrimLeft(strings.TrimRight(path, "/"), "/")
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		w.Header().Set("Location", target)
		w.WriteHeader(http.StatusPermanentRedirect)
		return
	}
	match, ok := h.routes.Match(path)
	if !ok {
		h.serveChain(w, r, &h.routes.NotFound, nil, true)
		return
	}
	h.serveChain(w, r, match.Route, match.Params, false)
}

// serveChain runs the loaders of a chain and writes the document. A URL
// no route matches renders the root error page below the root layouts.
func (h *Handler) serveChain(w http.ResponseWriter, r *http.Request, route *Route, params map[string]string, notFound bool) {
	if params == nil {
		params = map[string]string{}
	}
	query := r.URL.Query()
	run := newChainRun(r.Context(), route.Chain, Request{Params: params, Query: query, HTTP: r, Route: route.ID})
	defer run.close()
	failed := run.runAll()
	var failure error
	switch {
	case failed >= 0:
		failure = run.levels[failed].err
	case notFound:
		failed, failure = len(run.levels), NotFound()
	}
	var redirect *RedirectError
	if errors.As(failure, &redirect) {
		w.Header().Set("Location", redirect.Location)
		w.Header().Set("Cache-Control", "private")
		w.WriteHeader(redirect.Status)
		return
	}
	rendered := len(run.levels)
	status := http.StatusOK
	var segError *protocol.SegmentError
	if failure != nil {
		rendered = failed
		segError = h.describe(r, route, failed, failure)
		status = segError.Status
	}

	chain := make([]any, 0, rendered+1)
	heads := make([]Head, 0, rendered)
	header := http.Header{}
	var preload []string
	for _, l := range run.levels[:rendered] {
		chain = append(chain, map[string]any{"id": l.level.ID, "key": segmentKey(l.level, params, query), "data": l.view})
		heads = append(heads, l.req.head)
		for k, v := range l.req.header {
			header[k] = v
		}
	}
	if segError == nil && rendered > 0 {
		preload = append(preload, run.levels[rendered-1].level.ID)
	}
	if segError != nil {
		errorID := h.routes.Error
		if failed < len(route.Chain) {
			errorID = route.Chain[failed].Error
		}
		if errorID == "" {
			h.writePage(w, r, status, header, []byte(builtinErrorPage(status, segError.Message)))
			return
		}
		chain = append(chain, map[string]any{
			"id":    errorID,
			"key":   protocol.SegmentKey(errorID, strconv.Itoa(segError.Status), segError.Message),
			"error": map[string]any{"status": float64(segError.Status), "message": segError.Message},
		})
		preload = append(preload, errorID)
	}
	body, err := h.document(r.Context(), r, route, params, query, chain, mergeHead(h.defaults, heads), preload)
	if err != nil {
		h.logger.Error("vela app: render failed", "path", r.URL.Path, "route", route.ID, "err", err)
		message := "Internal Error"
		if h.dev {
			message = err.Error()
		}
		h.writePage(w, r, http.StatusInternalServerError, nil, []byte(builtinErrorPage(http.StatusInternalServerError, message)))
		return
	}
	h.writePage(w, r, status, header, body)
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

// routeProp is the route prop of R-1.
func routeProp(r *http.Request, route *Route, params map[string]string, query map[string][]string) map[string]any {
	ps := map[string]any{}
	for k, v := range params {
		ps[k] = v
	}
	qs := map[string]any{}
	for k, v := range query {
		if len(v) > 0 {
			qs[k] = v[0]
		}
	}
	url := r.URL.EscapedPath()
	if r.URL.RawQuery != "" {
		url += "?" + r.URL.RawQuery
	}
	return map[string]any{"id": route.ID, "url": url, "path": r.URL.EscapedPath(), "params": ps, "query": qs}
}

// document renders the shell with the head and the app root (R-5).
func (h *Handler) document(ctx context.Context, r *http.Request, route *Route, params map[string]string, query map[string][]string, chain []any, head Head, preload []string) ([]byte, error) {
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
	if err := h.routes.Root.Render(ctx, out, RootProps{Route: routeProp(r, route, params, query), Chain: chain}); err != nil {
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
