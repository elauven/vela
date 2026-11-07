package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/elauven/vela/app/internal/protocol"
)

type guardUser struct {
	Name  string
	Admin bool
}

var currentUser = NewLocal[*guardUser]("user")

var errNoRows = errors.New("sql: no rows in result set")

// guardRoutes: a root layout whose guard answers 404 for anonymous users
// on URLs no route matches, an (admin) layout whose guard requires an
// admin, and a settings page with an action. counts counts loader and
// action runs by id.
func guardRoutes() (Routes, navCounts) {
	counts := navCounts{}
	for _, id := range []string{"routes/Layout", "routes/(admin)/Layout", "routes/(admin)/settings/Page", "routes/Page", "action"} {
		counts[id] = &atomic.Int32{}
	}
	root := Level{ID: "routes/Layout", Error: "routes/Error",
		Guard: func(ctx context.Context, r *Request) error {
			if r.Route != "" {
				return nil
			}
			// Reading the user makes the response user-dependent; only
			// URLs without a route do.
			if _, ok := currentUser.Get(ctx); !ok {
				return NotFound()
			}
			return nil
		},
		Loader: LoaderOf(func(ctx context.Context, r *Request) (string, error) {
			counts["routes/Layout"].Add(1)
			return "menu", nil
		})}
	admin := Level{ID: "routes/(admin)/Layout", Error: "routes/Error",
		Guard: func(ctx context.Context, r *Request) error {
			u, ok := currentUser.Get(ctx)
			switch {
			case !ok:
				return Redirect(303, "/login?next="+url.QueryEscape(r.HTTP.URL.RequestURI()))
			case u.Name == "ghost":
				return errNoRows
			case u.Name == "panic":
				panic("guard exploded")
			case !u.Admin:
				return Error(403, "Forbidden")
			}
			return nil
		},
		Loader: LoaderOf(func(ctx context.Context, r *Request) (string, error) {
			counts["routes/(admin)/Layout"].Add(1)
			return "admin", nil
		})}
	settings := Level{ID: "routes/(admin)/settings/Page", Error: "routes/Error", Loader: LoaderOf(func(ctx context.Context, r *Request) (map[string]any, error) {
		counts["routes/(admin)/settings/Page"].Add(1)
		u, _ := currentUser.Get(ctx)
		if r.Query.Has("missing") {
			return nil, errNoRows
		}
		if r.Query.Has("panic") {
			panic("loader exploded")
		}
		r.CacheControl("public, max-age=60")
		return map[string]any{"user": u.Name}, nil
	})}
	home := Level{ID: "routes/Page", Error: "routes/Error", Loader: LoaderOf(func(ctx context.Context, r *Request) (string, error) {
		counts["routes/Page"].Add(1)
		r.CacheControl("public, max-age=60")
		return "home", nil
	})}
	actions := Actions{
		"default": func(ctx context.Context, r *Request) (any, error) {
			counts["action"].Add(1)
			u, _ := currentUser.Get(ctx)
			r.SetCookie(&http.Cookie{Name: "saved", Value: u.Name})
			return nil, nil
		},
		"missing": func(ctx context.Context, r *Request) (any, error) {
			counts["action"].Add(1)
			return nil, errNoRows
		},
	}
	return Routes{
		Routes: []Route{
			{ID: "/", Chain: []Level{root, home}, Render: RenderAuto},
			{ID: "/(admin)/settings", Chain: []Level{root, admin, settings}, Render: RenderAuto, Actions: actions},
		},
		Patterns: []Pattern{
			{Route: 0},
			{Route: 1, Segments: []Segment{{Kind: Static, Value: "settings"}}},
		},
		Matchers: BuiltinMatchers(),
		Error:    "routes/Error",
		NotFound: Route{Chain: []Level{root}},
		Depth:    3,
		Root:     Root{ID: "_app/VelaApp", Render: fakeRoot},
		Source:   "src",
		Entry:    "src/_app/client.js",
	}, counts
}

// withUser is the test's middleware: X-User names the user, "admin:x" an
// admin.
func withUser(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if name := r.Header.Get("X-User"); name != "" {
			admin := strings.HasPrefix(name, "admin:")
			r = currentUser.With(r, &guardUser{Name: strings.TrimPrefix(name, "admin:"), Admin: admin})
		}
		h.ServeHTTP(w, r)
	})
}

func guardHandler(t *testing.T, cfg Config) (http.Handler, navCounts) {
	t.Helper()
	routes, counts := guardRoutes()
	cfg.Routes = routes
	if cfg.Logger == nil {
		cfg.Logger = discardLogger()
	}
	return withUser(newHandler(t, cfg)), counts
}

func get(h http.Handler, target string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func with(headers map[string]string, extra ...string) map[string]string {
	out := map[string]string{}
	for k, v := range headers {
		out[k] = v
	}
	for i := 0; i+1 < len(extra); i += 2 {
		out[extra[i]] = extra[i+1]
	}
	return out
}

func TestGuardRedirectRunsNoLoader(t *testing.T) {
	h, counts := guardHandler(t, Config{})
	rec := get(h, "/settings?tab=1", nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login?next=%2Fsettings%3Ftab%3D1" {
		t.Fatalf("document: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if got := counts.take(); len(got) != 0 {
		t.Fatalf("document: loaders ran %v", got)
	}
	// The keys of an admin's document: the client keeps both layouts.
	admin := map[string]string{"X-User": "admin:ada"}
	doc := pageOf(t, get(h, "/settings", admin))
	var keys []string
	for _, entry := range doc.props.Chain {
		keys = append(keys, entry["key"].(string))
	}
	counts.take()
	for _, tc := range []struct {
		name    string
		headers map[string]string
	}{
		{"data", dataHeaders(nil)},
		{"fragment", map[string]string{protocol.HeaderNavigate: protocol.FormatNavigate(protocol.ModeFragment)}},
		{"kept levels", dataHeaders(keys)},
		{"prefetch", with(dataHeaders(keys), protocol.HeaderPurpose, "prefetch")},
	} {
		resp, _ := navigate(t, h, "/settings", tc.headers)
		if resp.Type != protocol.TypeRedirect || resp.Status != 303 || !strings.HasPrefix(resp.Location, "/login") {
			t.Errorf("%s: %+v", tc.name, resp)
		}
		if got := counts.take(); len(got) != 0 {
			t.Errorf("%s: loaders ran %v", tc.name, got)
		}
	}
}

func TestGuardErrorRendersErrorPageWithLayoutsAbove(t *testing.T) {
	h, counts := guardHandler(t, Config{})
	user := map[string]string{"X-User": "bob"}
	p := pageOf(t, get(h, "/settings", user))
	if p.status != http.StatusForbidden {
		t.Fatalf("status %d\n%s", p.status, p.body)
	}
	if len(p.props.Chain) != 2 || p.props.Chain[0]["data"] != "menu" || p.props.Chain[1]["id"] != "routes/Error" {
		t.Fatalf("chain %v", p.props.Chain)
	}
	if got := counts.take(); got["routes/Layout"] != 1 || got["routes/(admin)/Layout"] != 0 || got["routes/(admin)/settings/Page"] != 0 {
		t.Fatalf("loaders %v", got)
	}
	// Also with the admin layout kept by the client.
	resp, _ := navigate(t, h, "/settings", dataHeaders(nil))
	_ = resp
	resp, _ = navigate(t, h, "/settings", with(dataHeaders(nil), "X-User", "bob"))
	last := resp.Segments[len(resp.Segments)-1]
	if resp.Type != protocol.TypeData || last.Error == nil || last.Error.Status != 403 || resp.Start != 0 {
		t.Fatalf("data: %+v", resp)
	}
}

func TestGuardStopsActions(t *testing.T) {
	h, counts := guardHandler(t, Config{})
	form := url.Values{"x": {"1"}}
	rec := post(t, h, "/settings", form, nil)
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/login") {
		t.Fatalf("anonymous POST: %d %v", rec.Code, rec.Header())
	}
	rec = post(t, h, "/settings", form, dataHeaders(nil))
	resp, err := protocol.Decode(rec.Body.Bytes())
	if err != nil || resp.Type != protocol.TypeRedirect {
		t.Fatalf("anonymous POST with JS: %v %+v", err, resp)
	}
	rec = post(t, h, "/settings", form, map[string]string{"X-User": "bob"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("user POST: %d", rec.Code)
	}
	rec = post(t, h, "/settings", form, with(dataHeaders(nil), "X-User", "bob"))
	resp, err = protocol.Decode(rec.Body.Bytes())
	if err != nil || resp.Type != protocol.TypeData || resp.Segments[len(resp.Segments)-1].Error.Status != 403 {
		t.Fatalf("user POST with JS: %v %+v", err, resp)
	}
	if got := counts.take(); got["action"] != 0 {
		t.Fatalf("the action ran %d times", got["action"])
	}
	// An admin's action runs, reads the local and sets a cookie, also on the
	// JSON answer.
	rec = post(t, h, "/settings", form, with(dataHeaders(nil), "X-User", "admin:ada"))
	if got := counts.take(); got["action"] != 1 {
		t.Fatalf("admin: action runs %v", got)
	}
	cookie := rec.Header().Get("Set-Cookie")
	for _, want := range []string{"saved=ada", "Path=/", "HttpOnly", "Secure", "SameSite=Lax"} {
		if !strings.Contains(cookie, want) {
			t.Errorf("Set-Cookie %q misses %s", cookie, want)
		}
	}
}

func TestRootGuardAnswersNotFound(t *testing.T) {
	h, counts := guardHandler(t, Config{})
	rec := get(h, "/nope", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("anonymous: %d", rec.Code)
	}
	if got := counts.take(); got["routes/Layout"] != 0 {
		t.Fatalf("the root loader ran for an anonymous 404: %v", got)
	}
	p := pageOf(t, get(h, "/nope", map[string]string{"X-User": "bob"}))
	if p.status != http.StatusNotFound || len(p.props.Chain) != 2 || p.props.Chain[0]["data"] != "menu" {
		t.Fatalf("signed in: %d %v", p.status, p.props.Chain)
	}
}

func TestUserDependentResponses(t *testing.T) {
	h, _ := guardHandler(t, Config{Dev: true})
	// The home page reads no local: its public Cache-Control stays.
	if cc := get(h, "/", nil).Header().Get("Cache-Control"); cc != "public, max-age=60" {
		t.Errorf("home: Cache-Control %q", cc)
	}
	resp, rec := navigate(t, h, "/", dataHeaders(nil))
	if resp.Cache != "" || strings.Contains(rec.Body.String(), `"cache"`) {
		t.Errorf("home: cache %q", resp.Cache)
	}
	admin := map[string]string{"X-User": "admin:ada"}
	if cc := get(h, "/settings", admin).Header().Get("Cache-Control"); cc != "private" {
		t.Errorf("settings: Cache-Control %q", cc)
	}
	resp, rec = navigate(t, h, "/settings", with(dataHeaders(nil), "X-User", "admin:ada"))
	if resp.Cache != protocol.CacheOnce || rec.Header().Get("Cache-Control") != "private" {
		t.Errorf("settings data: cache %q, Cache-Control %q", resp.Cache, rec.Header().Get("Cache-Control"))
	}
	// A prefetch sets no cookie.
	rec = post(t, h, "/settings", url.Values{}, with(dataHeaders(nil), "X-User", "admin:ada", protocol.HeaderPurpose, "prefetch"))
	if rec.Header().Get("Set-Cookie") != "" {
		t.Errorf("a prefetch set a cookie: %q", rec.Header().Get("Set-Cookie"))
	}
	// Outside Dev, no Secure flag in Dev.
	rec = post(t, h, "/settings", url.Values{}, with(dataHeaders(nil), "X-User", "admin:ada"))
	if c := rec.Header().Get("Set-Cookie"); strings.Contains(c, "Secure") || !strings.Contains(c, "HttpOnly") {
		t.Errorf("Dev cookie %q", c)
	}
}

func TestLocalsOfConcurrentRequests(t *testing.T) {
	h, _ := guardHandler(t, Config{})
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := fmt.Sprintf("user%d", i)
			rec := get(h, "/settings", map[string]string{"X-User": "admin:" + name})
			if !strings.Contains(rec.Body.String(), `&#34;user&#34;:&#34;`+name+`&#34;`) {
				errs <- fmt.Errorf("%s got %s", name, rec.Body.String())
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestOnError(t *testing.T) {
	var log bytes.Buffer
	var seen []string
	var mu sync.Mutex
	h, _ := guardHandler(t, Config{
		Logger: slog.New(slog.NewTextHandler(&log, nil)),
		OnError: func(ctx context.Context, r *Request, err error) error {
			mu.Lock()
			seen = append(seen, err.Error())
			mu.Unlock()
			if errors.Is(err, errNoRows) {
				return NotFound()
			}
			return nil
		},
	})
	admin := map[string]string{"X-User": "admin:ada"}
	if code := get(h, "/settings?missing", admin).Code; code != http.StatusNotFound {
		t.Errorf("loader: %d", code)
	}
	if code := get(h, "/settings", map[string]string{"X-User": "ghost"}).Code; code != http.StatusNotFound {
		t.Errorf("guard: %d", code)
	}
	if code := post(t, h, "/settings?/missing", url.Values{}, admin).Code; code != http.StatusNotFound {
		t.Errorf("action: %d", code)
	}
	if code := get(h, "/settings?panic", admin).Code; code != http.StatusInternalServerError {
		t.Errorf("panic: %d", code)
	}
	if code := get(h, "/settings", map[string]string{"X-User": "panic"}).Code; code != http.StatusInternalServerError {
		t.Errorf("guard panic: %d", code)
	}
	// Redirects never reach OnError.
	get(h, "/settings", nil)
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 5 || !strings.Contains(seen[3], "loader exploded") || !strings.Contains(seen[4], "guard exploded") {
		t.Errorf("OnError saw %q", seen)
	}
	if n := strings.Count(log.String(), "stack="); n != 2 || !strings.Contains(log.String(), "guard_test.go") {
		t.Errorf("panic stacks in the log: %d\n%s", n, log.String())
	}
}

func TestMiddlewareHelpers(t *testing.T) {
	gate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/old":
				WriteRedirect(w, r, http.StatusFound, "/")
				return
			case "/busy":
				WriteError(w, r, http.StatusTooManyRequests, "Too many requests")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	h, _ := guardHandler(t, Config{})
	h = gate(h)
	if rec := get(h, "/old", nil); rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Errorf("document redirect: %d %v", rec.Code, rec.Header())
	}
	resp, _ := navigate(t, h, "/old", with(dataHeaders(nil), protocol.HeaderBuild, "b1"))
	if resp.Type != protocol.TypeRedirect || resp.Location != "/" || resp.Status != 302 || resp.Build != "b1" {
		t.Errorf("navigation redirect: %+v", resp)
	}
	if rec := get(h, "/busy", nil); rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), "Too many") {
		t.Errorf("document error: %d", rec.Code)
	}
	resp, _ = navigate(t, h, "/busy", dataHeaders(nil))
	if resp.Type != protocol.TypeReload {
		t.Errorf("navigation error: %+v", resp)
	}
	if IsNavigation(httptest.NewRequest(http.MethodGet, "/", nil)) {
		t.Errorf("a document request is a navigation")
	}
}
