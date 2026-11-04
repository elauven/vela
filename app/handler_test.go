package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elauven/vela/ssr_runtime/vite"
)

// fakeRoot stands in for the generated VelaApp renderer: it writes its
// props as JSON, so the tests read what the root would render.
func fakeRoot(ctx context.Context, w io.Writer, props RootProps) error {
	data, err := json.Marshal(map[string]any{"route": props.Route, "chain": props.Chain, "start": props.Start})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, `<main data-props="%s"></main>`, html.EscapeString(string(data)))
	return err
}

type menu struct {
	Items []string `json:"items"`
}

type users struct {
	Count int `json:"count"`
}

// testRoutes is a hand-written table of the shape vela-app generates.
func testRoutes(barrier *sync.WaitGroup) Routes {
	rootLayout := Level{ID: "routes/Layout", Error: "routes/Error", Loader: LoaderOf(func(ctx context.Context, r *Request) (menu, error) {
		if barrier != nil {
			barrier.Done()
			barrier.Wait()
		}
		r.Head().Title = "Site"
		r.Head().SetMeta(Meta{Name: "description", Content: "all"})
		r.Head().SetMeta(Meta{Property: "og:type", Content: "website"})
		return menu{Items: []string{"home", "users"}}, nil
	})}
	usersLayout := Level{ID: "routes/users/Layout", Error: "routes/users/Error", Deps: Deps{Query: []string{"sort"}}, Loader: LoaderOf(func(ctx context.Context, r *Request) (users, error) {
		switch r.Query.Get("fail") {
		case "layout":
			return users{}, Error(503, "users are offline")
		}
		r.CacheControl("public, max-age=60")
		return users{Count: 2}, nil
	})}
	page := Level{ID: "routes/users/[id=int]/Page", Error: "routes/users/Error", Params: []string{"id"}, Loader: LoaderOf(func(ctx context.Context, r *Request) (any, error) {
		if r.Query.Get("fail") == "layout" {
			<-ctx.Done() // canceled by the failure above
			return nil, ctx.Err()
		}
		parent, err := Parent[users](r)
		if err != nil {
			return nil, err
		}
		r.Head().Title = "User " + r.Params["id"]
		r.Head().SetMeta(Meta{Name: "description", Content: "one user"})
		switch r.Params["id"] {
		case "303":
			return nil, Redirect(303, "/login")
		case "404":
			return nil, NotFound()
		case "418":
			return nil, Error(418, "short and stout")
		case "500":
			panic("boom")
		case "501":
			return nil, errors.New("database is down")
		case "502":
			return math.NaN(), nil
		}
		return map[string]any{"id": r.Params["id"], "of": parent.Count, "joined": time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}, nil
	})}
	home := Level{ID: "routes/Page", Error: "routes/Error"}
	plain := Level{ID: "routes/plain/Page"}
	return Routes{
		Routes: []Route{
			{ID: "/", Chain: []Level{rootLayout, home}, Render: RenderAuto},
			{ID: "/users/[id=int]", Chain: []Level{rootLayout, usersLayout, page}, Render: RenderAuto},
			{ID: "/plain/[...rest]", Chain: []Level{plain}, Render: RenderAuto},
		},
		Patterns: []Pattern{
			{Route: 0},
			{Route: 1, Segments: []Segment{{Kind: Static, Value: "users"}, {Kind: Param, Value: "id", Matcher: "int"}}},
			{Route: 2, Segments: []Segment{{Kind: Static, Value: "plain"}, {Kind: Rest, Value: "rest"}}},
		},
		Matchers: BuiltinMatchers(),
		Error:    "routes/Error",
		NotFound: Route{Chain: []Level{rootLayout}},
		Depth:    3,
		Root:     Root{ID: "_app/VelaApp", Render: fakeRoot},
		Source:   "src",
		Entry:    "src/_app/client.js",
	}
}

type page struct {
	status int
	header http.Header
	body   string
	props  struct {
		Route map[string]any   `json:"route"`
		Chain []map[string]any `json:"chain"`
		Start int              `json:"start"`
	}
}

var propsAttr = regexp.MustCompile(`<main data-props="([^"]*)">`)

func serve(t *testing.T, h http.Handler, method, target string) page {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	p := page{status: rec.Code, header: rec.Header(), body: rec.Body.String()}
	if m := propsAttr.FindStringSubmatch(p.body); m != nil {
		if err := json.Unmarshal([]byte(html.UnescapeString(m[1])), &p.props); err != nil {
			t.Fatalf("props: %v", err)
		}
	}
	return p
}

func newTestHandler(t *testing.T, cfg Config) *Handler {
	t.Helper()
	if cfg.Routes.Root.Render == nil {
		cfg.Routes = testRoutes(nil)
	}
	h, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestDocument(t *testing.T) {
	h := newTestHandler(t, Config{Head: Head{Title: "Default", Meta: []Meta{{Name: "description", Content: "default"}, {Name: "theme-color", Content: "#fff"}}}})
	p := serve(t, h, "GET", "/users/2?sort=name&tab=a&tab=b")
	if p.status != 200 {
		t.Fatalf("status %d\n%s", p.status, p.body)
	}
	for k, want := range map[string]string{
		"Content-Type":   "text/html; charset=utf-8",
		"Vary":           "X-Vela-Navigate, X-Vela-Have, X-Vela-Build",
		"Cache-Control":  "public, max-age=60",
		"Content-Length": strconv.Itoa(len(p.body)),
	} {
		if got := p.header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	// The deepest title, deeper meta tags replacing the same name.
	for _, want := range []string{
		"<!doctype html>",
		`<title>User 2</title><meta name="description" content="one user" data-vela-head><meta name="theme-color" content="#fff" data-vela-head><meta property="og:type" content="website" data-vela-head>`,
		`<meta name="vela-build" content="` + h.BuildID() + `">`,
		`<div id="vela-app"><main data-props=`,
	} {
		if !strings.Contains(p.body, want) {
			t.Errorf("missing %s in\n%s", want, p.body)
		}
	}
	route := p.props.Route
	if route["id"] != "/users/[id=int]" || route["url"] != "/users/2?sort=name&tab=a&tab=b" || route["path"] != "/users/2" ||
		fmt.Sprint(route["params"]) != "map[id:2]" || fmt.Sprint(route["query"]) != "map[sort:name tab:a]" {
		t.Errorf("route %v", route)
	}
	chain := p.props.Chain
	if len(chain) != 3 || chain[0]["id"] != "routes/Layout" || chain[1]["id"] != "routes/users/Layout" || chain[2]["id"] != "routes/users/[id=int]/Page" {
		t.Fatalf("chain %v", chain)
	}
	// The page saw its parent's data through Parent; time values are
	// strings, numbers float64 (L-2).
	if fmt.Sprint(chain[2]["data"]) != "map[id:2 joined:2026-01-02T03:04:05Z of:2]" || fmt.Sprint(chain[1]["data"]) != "map[count:2]" {
		t.Errorf("data %v / %v", chain[1]["data"], chain[2]["data"])
	}
	// Keys follow the dependencies: the users layout depends on ?sort, the
	// page on :id, the root layout on nothing.
	keys := func(target string) []string {
		var out []string
		for _, s := range serve(t, h, "GET", target).props.Chain {
			out = append(out, s["key"].(string))
		}
		return out
	}
	a, b, c := keys("/users/2?sort=name"), keys("/users/3?sort=name"), keys("/users/2?sort=age&tab=x")
	if a[0] != b[0] || a[0] != c[0] || a[1] != b[1] || a[1] == c[1] || a[2] == b[2] || a[2] != c[2] {
		t.Errorf("keys\n%v\n%v\n%v", a, b, c)
	}
	for _, k := range a {
		if len(k) == 0 || len(k) > 64 {
			t.Errorf("key %q", k)
		}
	}
	// A route without loaders gets null data; HEAD gets the headers only.
	plain := serve(t, h, "GET", "/plain/a/b")
	if plain.status != 200 || plain.props.Chain[0]["data"] != nil || fmt.Sprint(plain.props.Route["params"]) != "map[rest:a/b]" || plain.header.Get("Cache-Control") != "private" {
		t.Errorf("plain: %d %v %v", plain.status, plain.props, plain.header)
	}
	head := serve(t, h, "HEAD", "/users/2")
	if head.status != 200 || head.body != "" || head.header.Get("Content-Length") == "" {
		t.Errorf("HEAD: %d %q %v", head.status, head.body, head.header)
	}
}

func TestErrors(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	h := newTestHandler(t, Config{Logger: logger})
	errorSegment := func(p page) map[string]any {
		last := p.props.Chain[len(p.props.Chain)-1]
		e, _ := last["error"].(map[string]any)
		return map[string]any{"id": last["id"], "status": e["status"], "message": e["message"], "levels": len(p.props.Chain)}
	}
	for _, tc := range []struct {
		target string
		status int
		want   string // the error segment
	}{
		{"/users/404", 404, "map[id:routes/users/Error levels:3 message:Not Found status:404]"},
		{"/users/418", 418, "map[id:routes/users/Error levels:3 message:short and stout status:418]"},
		{"/users/500", 500, "map[id:routes/users/Error levels:3 message:Internal Error status:500]"},
		{"/users/501", 500, "map[id:routes/users/Error levels:3 message:Internal Error status:500]"},
		{"/users/502", 500, "map[id:routes/users/Error levels:3 message:Internal Error status:500]"},
		// The layout fails: the page is canceled and the error replaces both.
		{"/users/2?fail=layout", 503, "map[id:routes/users/Error levels:2 message:users are offline status:503]"},
		// No route: the root layout around the root error page.
		{"/nope", 404, "map[id:routes/Error levels:2 message:Not Found status:404]"},
		{"/users/x", 404, "map[id:routes/Error levels:2 message:Not Found status:404]"},
	} {
		p := serve(t, h, "GET", tc.target)
		if p.status != tc.status {
			t.Errorf("%s: status %d, want %d", tc.target, p.status, tc.status)
			continue
		}
		if got := fmt.Sprint(errorSegment(p)); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.target, got, tc.want)
		}
		if p.props.Chain[0]["data"] == nil {
			t.Errorf("%s: the levels above the error render with their data: %v", tc.target, p.props.Chain[0])
		}
	}
	if !strings.Contains(logs.String(), "panic: boom") || !strings.Contains(logs.String(), "stack=") || !strings.Contains(logs.String(), "database is down") || !strings.Contains(logs.String(), "not JSON") {
		t.Errorf("logs:\n%s", logs.String())
	}
	if strings.Contains(logs.String(), "Not Found") || strings.Contains(logs.String(), "stout") {
		t.Errorf("errors with a status are not logged:\n%s", logs.String())
	}
	// Development shows the message of an internal error.
	dev := newTestHandler(t, Config{Dev: true, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if got := errorSegment(serve(t, dev, "GET", "/users/501"))["message"]; got != "database is down" {
		t.Errorf("dev message %v", got)
	}
	// A redirect from a loader.
	p := serve(t, h, "GET", "/users/303")
	if p.status != 303 || p.header.Get("Location") != "/login" || p.body != "" {
		t.Errorf("redirect: %d %v %q", p.status, p.header, p.body)
	}
	// Without an Error.vela the built-in page answers.
	routes := testRoutes(nil)
	routes.Error, routes.NotFound = "", Route{}
	plain := newTestHandler(t, Config{Routes: routes})
	p = serve(t, plain, "GET", "/nope")
	if p.status != 404 || !strings.Contains(p.body, "<title>404 Not Found</title>") || strings.Contains(p.body, "vela-app") {
		t.Errorf("built-in page: %d\n%s", p.status, p.body)
	}
}

func TestRequests(t *testing.T) {
	h := newTestHandler(t, Config{})
	p := serve(t, h, "POST", "/")
	if p.status != 405 || p.header.Get("Allow") != "GET, HEAD" {
		t.Errorf("POST: %d %v", p.status, p.header)
	}
	for target, location := range map[string]string{
		"/users/":         "/users",
		"/users/2/?a=1":   "/users/2?a=1",
		"//evil.example/": "/evil.example",
		"/a%2F/":          "/a%2F",
	} {
		p := serve(t, h, "GET", target)
		if p.status != 308 || p.header.Get("Location") != location {
			t.Errorf("%s: %d %q, want 308 %q", target, p.status, p.header.Get("Location"), location)
		}
	}
	// The loaders of a chain run concurrently: each waits for the other.
	var barrier sync.WaitGroup
	barrier.Add(2)
	routes := testRoutes(&barrier)
	routes.Routes[0].Chain[1].Loader = func(ctx context.Context, r *Request) (any, error) {
		barrier.Done()
		barrier.Wait()
		return "home", nil
	}
	concurrent := newTestHandler(t, Config{Routes: routes})
	done := make(chan page)
	go func() { done <- serve(t, concurrent, "GET", "/") }()
	select {
	case p := <-done:
		if p.status != 200 || p.props.Chain[1]["data"] != "home" {
			t.Errorf("concurrent: %d %v", p.status, p.props.Chain)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the loaders of a chain do not run concurrently")
	}
	// A response over MaxBytes fails instead of being truncated.
	small := newTestHandler(t, Config{MaxBytes: 300, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if p := serve(t, small, "GET", "/users/2"); p.status != 500 || !strings.Contains(p.body, "Internal Error") {
		t.Errorf("MaxBytes: %d\n%s", p.status, p.body)
	}
}

func TestParentWithoutLayout(t *testing.T) {
	routes := testRoutes(nil)
	routes.Routes[2].Chain[0].Loader = func(ctx context.Context, r *Request) (any, error) {
		_, err := Parent[menu](r)
		return nil, err
	}
	h := newTestHandler(t, Config{Routes: routes, Dev: true, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	p := serve(t, h, "GET", "/plain/x")
	if p.status != 500 || !strings.Contains(p.body, ErrNoParent.Error()) {
		t.Errorf("%d\n%s", p.status, p.body)
	}
}

func TestDocumentShellAndAssets(t *testing.T) {
	dir := t.TempDir()
	shellFile := filepath.Join(dir, "document.html")
	os.WriteFile(shellFile, []byte("<!doctype html><html><head>%vela.head%<link rel=icon href=/i.png></head><body class=x>%vela.body%</body></html>"), 0o644)
	manifest := `{
  "src/_app/client.js": {"file": "assets/client-A1.js", "isEntry": true, "imports": ["_runtime-R1.js"], "css": ["assets/client-C1.css"]},
  "_runtime-R1.js": {"file": "assets/runtime-R1.js"},
  "src/routes/users/[id=int]/Page.vela": {"file": "assets/Page-P1.js", "isDynamicEntry": true, "imports": ["_runtime-R1.js"], "css": ["assets/Page-P1.css"]},
  "src/routes/users/Error.vela": {"file": "assets/Error-E1.js", "isDynamicEntry": true}
}`
	assets, err := vite.Parse([]byte(manifest), "/")
	if err != nil {
		t.Fatal(err)
	}
	h := newTestHandler(t, Config{Document: shellFile, Assets: assets})
	p := serve(t, h, "GET", "/users/2")
	want := `<link rel="stylesheet" href="/assets/client-C1.css"><link rel="modulepreload" href="/assets/runtime-R1.js"><script type="module" src="/assets/client-A1.js"></script>` +
		`<link rel="stylesheet" href="/assets/Page-P1.css"><link rel="modulepreload" href="/assets/Page-P1.js"><link rel=icon href=/i.png></head><body class=x><div id="vela-app">`
	if !strings.Contains(p.body, want) || !strings.HasPrefix(p.body, "<!doctype html><html><head><title>User 2</title>") {
		t.Errorf("document:\n%s\nwant\n%s", p.body, want)
	}
	// The error page of a failed level is preloaded instead of the page.
	if p := serve(t, h, "GET", "/users/404"); !strings.Contains(p.body, `<link rel="modulepreload" href="/assets/Error-E1.js">`) || strings.Contains(p.body, "Page-P1.js") {
		t.Errorf("error preload:\n%s", p.body)
	}
	// The build id follows the client files.
	other, _ := vite.Parse([]byte(strings.ReplaceAll(manifest, "client-A1", "client-A2")), "/")
	if h.BuildID() == newTestHandler(t, Config{Assets: other}).BuildID() {
		t.Error("the build id does not change with the client")
	}
	if fixed := newTestHandler(t, Config{BuildID: "b7"}); fixed.BuildID() != "b7" {
		t.Error("BuildID is ignored")
	}

	for name, cfg := range map[string]Config{
		"no head":       {Document: writeShell(t, dir, "a.html", "<body>%vela.body%</body>")},
		"two bodies":    {Document: writeShell(t, dir, "b.html", "%vela.head%%vela.body%%vela.body%")},
		"order":         {Document: writeShell(t, dir, "c.html", "%vela.body%%vela.head%")},
		"missing file":  {Document: filepath.Join(dir, "none.html")},
		"no entry":      {Assets: mustParse(t, `{"src/other.js": {"file": "o.js", "isEntry": true}}`)},
		"bad manifest":  {Manifest: writeShell(t, dir, "m.json", "{")},
		"no root":       {Routes: Routes{Root: Root{}}},
		"unknown match": {Routes: func() Routes { r := testRoutes(nil); r.Matchers = nil; return r }()},
	} {
		if cfg.Routes.Root.Render == nil && name != "no root" {
			cfg.Routes = testRoutes(nil)
		}
		if _, err := New(cfg); err == nil {
			t.Errorf("%s: New accepted it", name)
		}
	}
}

func writeShell(t *testing.T, dir, name, data string) string {
	file := filepath.Join(dir, name)
	if err := os.WriteFile(file, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return file
}

func mustParse(t *testing.T, manifest string) *vite.Assets {
	a, err := vite.Parse([]byte(manifest), "/")
	if err != nil {
		t.Fatal(err)
	}
	return a
}
