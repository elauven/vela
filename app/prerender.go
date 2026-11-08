package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/elauven/vela/app/internal/protocol"
)

// Prerendering (task 092). vela-app build runs the built server with
// PrerenderEnv set to a directory: app.New then renders every page that
// declares Prerender into it and exits, so the server's own main needs no
// change. A page is written twice: the document, prerendered/<path>/
// index.html, and the data response of its whole chain (start 0),
// prerendered/<path>/index.json, which a navigation gets trimmed by
// X-Vela-Have. Handler.Prerender does the same for a test or another
// build tool.

// PrerenderEnv is the environment variable vela-app build sets to the
// directory to prerender into.
const PrerenderEnv = "VELA_APP_PRERENDER"

// PrerenderReport lists the prerendered URL paths in order.
type PrerenderReport struct {
	Paths []string
}

type prerenderingKey struct{}

// Prerender renders the pages that declare Prerender into dir: the URLs of
// their Entries, the parameterless routes, and the links to prerenderable
// URLs found in the pages. A page that does not render with status 200
// fails it, and so do two paths whose files differ only in case.
func (h *Handler) Prerender(ctx context.Context, dir string) (PrerenderReport, error) {
	var report PrerenderReport
	ctx = context.WithValue(ctx, prerenderingKey{}, true)
	var queue []string
	seen := map[string]bool{}
	files := map[string]string{} // lower-cased path → path
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			queue = append(queue, p)
		}
	}
	for i := range h.routes.Routes {
		route := &h.routes.Routes[i]
		if route.Prerender == nil {
			continue
		}
		urls, err := h.prerenderURLs(ctx, i, route)
		if err != nil {
			return report, fmt.Errorf("prerender %s: %w", route.ID, err)
		}
		for _, u := range urls {
			add(u)
		}
	}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		match, ok := h.routes.Match(p)
		if !ok || match.Route.Prerender == nil {
			continue
		}
		lower := strings.ToLower(p)
		if other, ok := files[lower]; ok && other != p {
			return report, fmt.Errorf("VELA-APP-RENDER: prerendered paths %s and %s are one file on file systems that ignore case", other, p)
		}
		files[lower] = p
		document, err := h.prerenderRequest(ctx, p, "")
		if err != nil {
			return report, err
		}
		data, err := h.prerenderRequest(ctx, p, string(protocol.ModeData)+";v="+fmt.Sprint(protocol.Version))
		if err != nil {
			return report, err
		}
		target := filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(p, "/")))
		if err := os.MkdirAll(target, 0o755); err != nil {
			return report, err
		}
		if err := os.WriteFile(filepath.Join(target, "index.html"), document, 0o644); err != nil {
			return report, err
		}
		if err := os.WriteFile(filepath.Join(target, "index.json"), data, 0o644); err != nil {
			return report, err
		}
		report.Paths = append(report.Paths, p)
		for _, link := range pageLinks(document) {
			add(link)
		}
	}
	return report, nil
}

// prerenderRequest renders path as a document (nav "") or as a navigation
// with the given X-Vela-Navigate value and returns the body.
func (h *Handler) prerenderRequest(ctx context.Context, p, nav string) ([]byte, error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, p, nil)
	if err != nil {
		return nil, err
	}
	r.RequestURI = p
	if nav != "" {
		r.Header.Set(protocol.HeaderNavigate, nav)
		r.Header.Set(protocol.HeaderBuild, h.build)
	}
	rec := &recorder{header: http.Header{}, status: http.StatusOK}
	h.ServeHTTP(rec, r)
	if rec.status != http.StatusOK {
		return nil, fmt.Errorf("prerender %s: status %d", p, rec.status)
	}
	if nav != "" {
		resp, err := protocol.Decode(rec.body)
		if err != nil {
			return nil, fmt.Errorf("prerender %s: %w", p, err)
		}
		if resp.Type != protocol.TypeData {
			return nil, fmt.Errorf("prerender %s: the page answers a navigation with %q, not data", p, resp.Type)
		}
		for _, s := range resp.Segments {
			if s.Error != nil {
				return nil, fmt.Errorf("prerender %s: %s fails with %d %s", p, s.ID, s.Error.Status, s.Error.Message)
			}
		}
	}
	return rec.body, nil
}

// prerenderURLs lists the URLs of a prerendered route: one per Entries
// element, or the route's URL when it has no parameters.
func (h *Handler) prerenderURLs(ctx context.Context, index int, route *Route) ([]string, error) {
	var patterns [][]Segment
	for _, p := range h.routes.Patterns {
		if p.Route == index {
			patterns = append(patterns, p.Segments)
		}
	}
	if route.Prerender.Entries == nil {
		for _, segments := range patterns {
			if u, ok := patternURL(segments, nil); ok {
				return []string{u}, nil
			}
		}
		return nil, nil
	}
	entries, err := route.Prerender.Entries(ctx)
	if err != nil {
		return nil, err
	}
	var urls []string
	for _, params := range entries {
		found := false
		for _, segments := range patterns {
			if u, ok := patternURL(segments, params); ok {
				urls = append(urls, u)
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("the entry %v does not fit the parameters of the route", params)
		}
	}
	return urls, nil
}

// patternURL builds the URL of a pattern from params; ok is false when the
// pattern's parameters are not exactly the given ones.
func patternURL(segments []Segment, params Params) (string, bool) {
	used := 0
	var parts []string
	for _, s := range segments {
		switch s.Kind {
		case Static:
			parts = append(parts, url.PathEscape(s.Value))
		case Param:
			v, ok := params[s.Value]
			if !ok || v == "" {
				return "", false
			}
			used++
			parts = append(parts, url.PathEscape(v))
		case Rest:
			v, ok := params[s.Value]
			if !ok {
				return "", false
			}
			used++
			for _, part := range strings.Split(v, "/") {
				if part != "" {
					parts = append(parts, url.PathEscape(part))
				}
			}
		}
	}
	if used != len(params) {
		return "", false
	}
	return "/" + strings.Join(parts, "/"), true
}

var hrefPattern = regexp.MustCompile(`\shref="(/[^"#?]*)`)

// pageLinks lists the same-site paths of a document's links, sorted.
func pageLinks(document []byte) []string {
	set := map[string]bool{}
	for _, m := range hrefPattern.FindAllSubmatch(document, -1) {
		p := string(m[1])
		if strings.HasPrefix(p, "//") {
			continue
		}
		if len(p) > 1 {
			p = strings.TrimRight(p, "/")
		}
		set[p] = true
	}
	links := make([]string, 0, len(set))
	for p := range set {
		links = append(links, p)
	}
	sort.Strings(links)
	return links
}

// servePrerendered answers a GET or HEAD of a prerendered page from its
// files, without running guards or loaders. A URL with a query, and a
// page whose files are missing, render as usual.
func (h *Handler) servePrerendered(w http.ResponseWriter, r *http.Request, route *Route, isNav bool, nav protocol.Request) bool {
	if h.prerendered == "" || route.Prerender == nil || r.URL.RawQuery != "" || r.Context().Value(prerenderingKey{}) != nil {
		return false
	}
	clean := path.Clean("/" + r.URL.Path)
	dir := filepath.Join(h.prerendered, filepath.FromSlash(strings.TrimPrefix(clean, "/")))
	if !isNav {
		body, err := os.ReadFile(filepath.Join(dir, "index.html"))
		if err != nil {
			return false
		}
		w.Header().Set("X-Vela-Prerendered", "1")
		h.writePage(w, r, http.StatusOK, nil, body)
		return true
	}
	data, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		return false
	}
	resp, err := protocol.Decode(data)
	if err != nil {
		h.logger.Warn("vela app: a prerendered navigation response does not decode", "path", r.URL.Path, "err", err)
		return false
	}
	keys := make([]string, len(resp.Segments))
	for i, s := range resp.Segments {
		keys[i] = s.Key
	}
	start := protocol.Start(nav.Have, keys)
	if start >= len(keys) && len(keys) > 0 {
		// The client keeps everything; it still gets the page level.
		start = len(keys) - 1
	}
	resp.Start, resp.Segments = start, resp.Segments[start:]
	w.Header().Set("X-Vela-Prerendered", "1")
	h.writeJSON(w, r, nil, resp)
	return true
}

// prerenderFromEnv runs the prerendering vela-app build asked for with
// PrerenderEnv and exits the process.
func (h *Handler) prerenderFromEnv(dir string, stdout io.Writer) {
	report, err := h.Prerender(context.Background(), dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vela app: prerender: %v\n", err)
		os.Exit(1)
	}
	for _, p := range report.Paths {
		fmt.Fprintf(stdout, "prerendered %s\n", p)
	}
	os.Exit(0)
}
