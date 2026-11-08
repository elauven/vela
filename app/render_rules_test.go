package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elauven/vela/app/internal/protocol"
)

// The rendering rules of task 092: the response cache and prerendering.

// rulesRoutes is a table with a cached page (/news), a cached page that
// reads a cookie (/me), and prerendered docs (/docs/[slug]) whose root
// layout links to them.
func rulesRoutes(calls map[string]*atomic.Int64, entries func(context.Context) ([]Params, error)) Routes {
	counted := func(name string, load func(r *Request) (any, error)) Loader {
		calls[name] = &atomic.Int64{}
		return func(ctx context.Context, r *Request) (any, error) {
			calls[name].Add(1)
			return load(r)
		}
	}
	root := Level{ID: "routes/Layout", Loader: counted("layout", func(r *Request) (any, error) {
		return map[string]any{"links": []string{"/docs/a", "/docs/b"}}, nil
	})}
	cache := &Cache{MaxAge: time.Minute, StaleWhileRevalidate: time.Hour}
	return Routes{
		Routes: []Route{
			{ID: "/news", Chain: []Level{root, {ID: "routes/news/Page", Loader: counted("news", func(r *Request) (any, error) { return "news", nil })}}, Render: RenderAuto, Cache: cache},
			{ID: "/me", Chain: []Level{root, {ID: "routes/me/Page", Loader: counted("me", func(r *Request) (any, error) {
				_, _ = r.Cookie("session")
				return "me", nil
			})}}, Render: RenderAuto, Cache: cache},
			{ID: "/docs/[slug]", Chain: []Level{root, {ID: "routes/docs/[slug]/Page", Params: []string{"slug"}, Loader: counted("docs", func(r *Request) (any, error) {
				if r.Params["slug"] == "broken" {
					return nil, errors.New("db down")
				}
				return map[string]any{"slug": r.Params["slug"]}, nil
			})}}, Render: RenderAuto, Prerender: &Prerender{Entries: entries}},
		},
		Patterns: []Pattern{
			{Route: 0, Segments: []Segment{{Kind: Static, Value: "news"}}},
			{Route: 1, Segments: []Segment{{Kind: Static, Value: "me"}}},
			{Route: 2, Segments: []Segment{{Kind: Static, Value: "docs"}, {Kind: Param, Value: "slug"}}},
		},
		Matchers: BuiltinMatchers(),
		Depth:    2,
		Root:     Root{ID: "_app/VelaApp", Render: fakeRoot},
	}
}

func rulesHandler(t *testing.T, cfg Config, calls map[string]*atomic.Int64, entries func(context.Context) ([]Params, error)) *Handler {
	t.Helper()
	cfg.Routes = rulesRoutes(calls, entries)
	cfg.Routes.Root.Render = func(ctx context.Context, w io.Writer, props RootProps) error {
		if err := fakeRoot(ctx, w, props); err != nil {
			return err
		}
		_, err := w.Write([]byte(`<a href="/docs/a">a</a> <a href="/docs/b?x=1">b</a> <a href="https://elsewhere/docs/c">c</a> <a href="/news">news</a>`))
		return err
	}
	h, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func fetch(h http.Handler, target string, header map[string]string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, target, nil)
	for k, v := range header {
		r.Header.Set(k, v)
	}
	h.ServeHTTP(rec, r)
	return rec
}

func TestCacheServesFreshStaleAndRevalidatesOnce(t *testing.T) {
	calls := map[string]*atomic.Int64{}
	h := rulesHandler(t, Config{}, calls, nil)
	now := time.Unix(1_000_000, 0)
	var clock sync.Mutex
	h.now = func() time.Time {
		clock.Lock()
		defer clock.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		clock.Lock()
		now = now.Add(d)
		clock.Unlock()
	}
	state := func(rec *httptest.ResponseRecorder) string { return rec.Header().Get("X-Vela-Cache") }

	first := fetch(h, "/news", nil)
	if first.Code != 200 || state(first) != "miss" || first.Header().Get("Cache-Control") != "public, max-age=60, stale-while-revalidate=3600" {
		t.Fatalf("first: %d %q %q", first.Code, state(first), first.Header().Get("Cache-Control"))
	}
	if second := fetch(h, "/news", nil); state(second) != "hit" || second.Body.String() != first.Body.String() || calls["news"].Load() != 1 {
		t.Fatalf("second: %q, %d loads", state(second), calls["news"].Load())
	}
	// A navigation is another key.
	if nav := fetch(h, "/news", map[string]string{protocol.HeaderNavigate: "auto;v=1"}); state(nav) != "miss" || calls["news"].Load() != 2 {
		t.Fatalf("navigation: %q, %d loads", state(nav), calls["news"].Load())
	}
	// Stale: served at once, one background render for many requests.
	advance(2 * time.Minute)
	for range 5 {
		if rec := fetch(h, "/news", nil); state(rec) != "stale" {
			t.Fatalf("stale: %q", state(rec))
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for calls["news"].Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if n := calls["news"].Load(); n != 3 {
		t.Fatalf("background renders: %d loads, want 3", n)
	}
	if rec := fetch(h, "/news", nil); state(rec) != "hit" {
		t.Fatalf("after revalidation: %q", state(rec))
	}
	// Expired: rendered again now.
	advance(2 * time.Hour)
	if rec := fetch(h, "/news", nil); state(rec) != "miss" {
		t.Fatalf("expired: %q", state(rec))
	}
	// Purge drops it.
	if err := h.Purge(context.Background(), "/news"); err != nil {
		t.Fatal(err)
	}
	if rec := fetch(h, "/news", nil); state(rec) != "miss" {
		t.Fatalf("after purge: %q", state(rec))
	}
}

func TestCacheNeverKeepsUserDependentResponses(t *testing.T) {
	calls := map[string]*atomic.Int64{}
	h := rulesHandler(t, Config{}, calls, nil)
	for range 2 {
		rec := fetch(h, "/me", map[string]string{"Cookie": "session=abc"})
		if rec.Code != 200 || rec.Header().Get("X-Vela-Cache") != "miss" || rec.Header().Get("Cache-Control") != "private" {
			t.Fatalf("/me: %d %q %q", rec.Code, rec.Header().Get("X-Vela-Cache"), rec.Header().Get("Cache-Control"))
		}
	}
	if n := calls["me"].Load(); n != 2 {
		t.Fatalf("/me loads %d, want 2", n)
	}
}

func TestMemoryCacheLimitAndPurgePatterns(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryCache(300)
	for _, p := range []string{"/blog/a", "/blog/b", "/blog/b/c", "/about"} {
		store.Set(ctx, "k"+p, CachedResponse{Path: p, Body: []byte("x")}, 0)
	}
	store.Purge(ctx, "/blog/*")
	for p, want := range map[string]bool{"/blog/a": false, "/blog/b": false, "/blog/b/c": true, "/about": true} {
		if _, ok, _ := store.Get(ctx, "k"+p); ok != want {
			t.Errorf("%s kept %v, want %v", p, ok, want)
		}
	}
	store.Purge(ctx, "/blog/**")
	if _, ok, _ := store.Get(ctx, "k/blog/b/c"); ok {
		t.Error("/blog/** kept /blog/b/c")
	}
	// The limit drops the least recently used.
	big := CachedResponse{Path: "/big", Body: []byte(strings.Repeat("x", 200))}
	store.Set(ctx, "k/about", CachedResponse{Path: "/about", Body: []byte("x")}, 0)
	store.Set(ctx, "k/big", big, 0)
	store.Set(ctx, "k/big2", big, 0)
	if _, ok, _ := store.Get(ctx, "k/big"); ok {
		t.Error("the limit kept the least recently used entry")
	}
	if _, ok, _ := store.Get(ctx, "k/big2"); !ok {
		t.Error("the limit dropped the newest entry")
	}
}

func TestPrerenderWritesAndServesFiles(t *testing.T) {
	calls := map[string]*atomic.Int64{}
	entries := func(context.Context) ([]Params, error) { return []Params{{"slug": "a"}}, nil }
	h := rulesHandler(t, Config{}, calls, entries)
	dir := t.TempDir()
	report, err := h.Prerender(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	// /docs/a from Entries, /docs/b from its link; /news is not prerendered.
	if strings.Join(report.Paths, " ") != "/docs/a /docs/b" {
		t.Fatalf("paths %v", report.Paths)
	}
	for _, f := range []string{"docs/a/index.html", "docs/a/index.json", "docs/b/index.html", "docs/b/index.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatal(err)
		}
	}

	calls = map[string]*atomic.Int64{}
	served := rulesHandler(t, Config{Prerendered: dir}, calls, entries)
	doc := fetch(served, "/docs/b", nil)
	if doc.Code != 200 || doc.Header().Get("X-Vela-Prerendered") != "1" || !strings.Contains(doc.Body.String(), `&#34;slug&#34;`) && !strings.Contains(doc.Body.String(), "slug") {
		t.Fatalf("document: %d %v\n%s", doc.Code, doc.Header(), doc.Body.String())
	}
	// A navigation gets the stored chain trimmed by X-Vela-Have.
	full := fetch(served, "/docs/a", map[string]string{protocol.HeaderNavigate: "auto;v=1"})
	resp, err := protocol.Decode(full.Body.Bytes())
	if err != nil || resp.Start != 0 || len(resp.Segments) != 2 {
		t.Fatalf("full navigation: %v %+v", err, resp)
	}
	trimmed := fetch(served, "/docs/b", map[string]string{protocol.HeaderNavigate: "auto;v=1", protocol.HeaderHave: resp.Segments[0].Key})
	if resp, err := protocol.Decode(trimmed.Body.Bytes()); err != nil || resp.Start != 1 || len(resp.Segments) != 1 || resp.Segments[0].ID != "routes/docs/[slug]/Page" {
		t.Fatalf("trimmed navigation: %v %+v", err, resp)
	}
	for name, n := range calls {
		if n.Load() != 0 {
			t.Errorf("loader %s ran %d times for prerendered pages", name, n.Load())
		}
	}
	// A query, or a page without files, renders as usual.
	if rec := fetch(served, "/docs/a?x=1", nil); rec.Header().Get("X-Vela-Prerendered") != "" || calls["docs"].Load() != 1 {
		t.Errorf("a query was served from the files")
	}
	if rec := fetch(served, "/docs/z", nil); rec.Code != 200 || rec.Header().Get("X-Vela-Prerendered") != "" {
		t.Errorf("/docs/z: %d %v", rec.Code, rec.Header())
	}
}

func TestPrerenderFailures(t *testing.T) {
	calls := map[string]*atomic.Int64{}
	broken := rulesHandler(t, Config{}, calls, func(context.Context) ([]Params, error) { return []Params{{"slug": "broken"}}, nil })
	if _, err := broken.Prerender(context.Background(), t.TempDir()); err == nil || !strings.Contains(err.Error(), "/docs/broken") {
		t.Errorf("a loader error: %v", err)
	}
	cased := rulesHandler(t, Config{}, calls, func(context.Context) ([]Params, error) { return []Params{{"slug": "a"}, {"slug": "A"}}, nil })
	if _, err := cased.Prerender(context.Background(), t.TempDir()); err == nil || !strings.Contains(err.Error(), "ignore case") {
		t.Errorf("paths that differ in case: %v", err)
	}
	wrong := rulesHandler(t, Config{}, calls, func(context.Context) ([]Params, error) { return []Params{{"id": "1"}}, nil })
	if _, err := wrong.Prerender(context.Background(), t.TempDir()); err == nil || !strings.Contains(err.Error(), "does not fit") {
		t.Errorf("an entry with other parameters: %v", err)
	}
}
