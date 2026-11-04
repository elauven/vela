package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elauven/vela/app/internal/protocol"
)

// navCounts counts the loader runs of navRoutes by level id.
type navCounts map[string]*atomic.Int32

func (c navCounts) take() map[string]int {
	out := map[string]int{}
	for id, n := range c {
		if v := n.Swap(0); v > 0 {
			out[id] = int(v)
		}
	}
	return out
}

type section struct {
	Q string `json:"q"`
}

// navRoutes is a table for data navigations: a root layout that sets the
// head, a section layout that depends on ?q, pages below it.
func navRoutes() (Routes, navCounts) {
	counts := navCounts{}
	count := func(id string) {
		if counts[id] == nil {
			counts[id] = &atomic.Int32{}
		}
		counts[id].Add(1)
	}
	for _, id := range []string{"routes/Layout", "routes/s/Layout", "routes/s/[id]/Page", "routes/doc/Page"} {
		counts[id] = &atomic.Int32{}
	}
	root := Level{ID: "routes/Layout", Error: "routes/Error", Head: true, Loader: LoaderOf(func(ctx context.Context, r *Request) (string, error) {
		count("routes/Layout")
		if r.Query.Has("rootfail") {
			return "", Error(503, "root is down")
		}
		r.Head().Title = "Site"
		r.Head().SetMeta(Meta{Name: "description", Content: "site"})
		return "root", nil
	})}
	sec := Level{ID: "routes/s/Layout", Error: "routes/s/Error", Deps: Deps{Query: []string{"q"}}, Loader: LoaderOf(func(ctx context.Context, r *Request) (section, error) {
		count("routes/s/Layout")
		r.CacheControl("public, max-age=5")
		return section{Q: r.Query.Get("q")}, nil
	})}
	pg := Level{ID: "routes/s/[id]/Page", Error: "routes/s/Error", Params: []string{"id"}, Head: true, Loader: LoaderOf(func(ctx context.Context, r *Request) (map[string]any, error) {
		count("routes/s/[id]/Page")
		switch r.Params["id"] {
		case "3":
			return nil, Redirect(307, "/s/4")
		case "7":
			parent, err := Parent[section](r)
			if err != nil {
				return nil, err
			}
			return map[string]any{"id": "7", "q": parent.Q}, nil
		case "9":
			return nil, NotFound()
		case "8":
			return nil, Error(500, "no error page above")
		}
		r.Head().Title = "Item " + r.Params["id"]
		return map[string]any{"id": r.Params["id"]}, nil
	})}
	doc := Level{ID: "routes/doc/Page", Error: "routes/Error", Loader: LoaderOf(func(ctx context.Context, r *Request) (any, error) {
		count("routes/doc/Page")
		return nil, nil
	})}
	bare := Level{ID: "routes/bare/[id]/Page", Params: []string{"id"}, Loader: LoaderOf(func(ctx context.Context, r *Request) (any, error) {
		return nil, NotFound()
	})}
	return Routes{
		Routes: []Route{
			{ID: "/s/[id]", Chain: []Level{root, sec, pg}, Render: RenderAuto},
			{ID: "/doc", Chain: []Level{root, doc}, Render: RenderDocument},
			{ID: "/bare/[id]", Chain: []Level{bare}, Render: RenderAuto},
		},
		Patterns: []Pattern{
			{Route: 0, Segments: []Segment{{Kind: Static, Value: "s"}, {Kind: Param, Value: "id"}}},
			{Route: 1, Segments: []Segment{{Kind: Static, Value: "doc"}}},
			{Route: 2, Segments: []Segment{{Kind: Static, Value: "bare"}, {Kind: Param, Value: "id"}}},
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

// navigate sends a navigation request with the given headers.
func navigate(t *testing.T, h http.Handler, target string, headers map[string]string) (protocol.Response, *httptest.ResponseRecorder) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: status %d, want 200 (N-6)\n%s", target, rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("%s: Content-Type %q", target, ct)
	}
	resp, err := protocol.Decode(rec.Body.Bytes())
	if err != nil {
		t.Fatalf("%s: %v\n%s", target, err, rec.Body)
	}
	return resp, rec
}

// keysOf are the keys a document gives the client (N-7).
func keysOf(t *testing.T, h http.Handler, target string) []string {
	t.Helper()
	p := serve(t, h, http.MethodGet, target)
	var keys []string
	for _, entry := range p.props.Chain {
		keys = append(keys, entry["key"].(string))
	}
	return keys
}

func dataHeaders(have []string) map[string]string {
	return map[string]string{
		protocol.HeaderNavigate: protocol.FormatNavigate(protocol.ModeData),
		protocol.HeaderHave:     protocol.FormatHave(have),
	}
}

func segmentIDs(resp protocol.Response) []string {
	var ids []string
	for _, s := range resp.Segments {
		ids = append(ids, s.ID)
	}
	return ids
}

func TestNavigateStart(t *testing.T) {
	routes, counts := navRoutes()
	h := newHandler(t, Config{Routes: routes})
	from := keysOf(t, h, "/s/1?q=a")
	counts.take()
	cases := []struct {
		name     string
		target   string
		have     []string
		start    int
		segments []string
		ran      map[string]int
		title    string
	}{
		{
			// The section layout is kept and does not run; the root layout
			// runs for its head only.
			name: "page changes", target: "/s/2?q=a", have: from, start: 2,
			segments: []string{"routes/s/[id]/Page"},
			ran:      map[string]int{"routes/Layout": 1, "routes/s/[id]/Page": 1}, title: "Item 2",
		},
		{
			name: "declared query changes", target: "/s/1?q=b", have: from, start: 1,
			segments: []string{"routes/s/Layout", "routes/s/[id]/Page"},
			ran:      map[string]int{"routes/Layout": 1, "routes/s/Layout": 1, "routes/s/[id]/Page": 1}, title: "Item 1",
		},
		{
			// No level depends on ?x: nothing is sent, the route changes.
			name: "undeclared query changes", target: "/s/1?q=a&x=1", have: from, start: 3,
			ran: map[string]int{"routes/Layout": 1, "routes/s/[id]/Page": 1}, title: "Item 1",
		},
		{
			name: "no keys", target: "/s/1?q=a", start: 0,
			segments: []string{"routes/Layout", "routes/s/Layout", "routes/s/[id]/Page"},
			ran:      map[string]int{"routes/Layout": 1, "routes/s/Layout": 1, "routes/s/[id]/Page": 1}, title: "Item 1",
		},
		{
			// Parent runs the kept section layout on demand; it is not sent.
			name: "parent on demand", target: "/s/7?q=a", have: from, start: 2,
			segments: []string{"routes/s/[id]/Page"},
			ran:      map[string]int{"routes/Layout": 1, "routes/s/Layout": 1, "routes/s/[id]/Page": 1}, title: "Site",
		},
		{
			name: "error segment", target: "/s/9?q=a", have: from, start: 2,
			segments: []string{"routes/s/Error"},
			ran:      map[string]int{"routes/Layout": 1, "routes/s/[id]/Page": 1}, title: "Site",
		},
		{
			// A kept level that runs for its head and fails is replaced.
			name: "kept level fails", target: "/s/2?q=a&rootfail", have: from, start: 0,
			segments: []string{"routes/Error"},
			ran:      map[string]int{"routes/Layout": 1, "routes/s/[id]/Page": 1},
		},
		{
			name: "no route", target: "/nope", have: from, start: 1,
			segments: []string{"routes/Error"},
			ran:      map[string]int{"routes/Layout": 1}, title: "Site",
		},
		{
			name: "other chain", target: "/s/1?q=a", have: []string{protocol.SegmentKey("other"), from[1], from[2]}, start: 0,
			segments: []string{"routes/Layout", "routes/s/Layout", "routes/s/[id]/Page"},
			ran:      map[string]int{"routes/Layout": 1, "routes/s/Layout": 1, "routes/s/[id]/Page": 1}, title: "Item 1",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, rec := navigate(t, h, c.target, dataHeaders(c.have))
			if resp.Type != protocol.TypeData || resp.Start != c.start {
				t.Fatalf("type %s start %d, want data %d", resp.Type, resp.Start, c.start)
			}
			if got := strings.Join(segmentIDs(resp), " "); got != strings.Join(c.segments, " ") {
				t.Errorf("segments %q, want %q", got, c.segments)
			}
			ran := counts.take()
			if len(ran) != len(c.ran) {
				t.Errorf("loaders ran %v, want %v", ran, c.ran)
			}
			for id, n := range c.ran {
				if ran[id] != n {
					t.Errorf("loaders ran %v, want %v", ran, c.ran)
					break
				}
			}
			if c.title != "" && (resp.Head == nil || resp.Head.Title != c.title) {
				t.Errorf("head %+v, want title %q", resp.Head, c.title)
			}
			if rec.Header().Get("Vary") != protocol.Vary {
				t.Errorf("Vary %q", rec.Header().Get("Vary"))
			}
			// The keys sent are the keys a document of the target gives.
			doc := keysOf(t, h, c.target)
			counts.take()
			for i, s := range resp.Segments {
				if s.Key != doc[resp.Start+i] {
					t.Errorf("segment %s key %s, the document has %s", s.ID, s.Key, doc[resp.Start+i])
				}
			}
		})
	}
}

func TestNavigateSegments(t *testing.T) {
	routes, _ := navRoutes()
	h := newHandler(t, Config{Routes: routes, Head: Head{Title: "Default"}})
	from := keysOf(t, h, "/s/1?q=a")

	resp, rec := navigate(t, h, "/s/2?q=a&tab=x", dataHeaders(from))
	if resp.Route == nil || resp.Route.ID != "/s/[id]" || resp.Route.URL != "/s/2?q=a&tab=x" || resp.Route.Path != "/s/2" || resp.Route.Params["id"] != "2" || resp.Route.Query["tab"] != "x" {
		t.Errorf("route %+v", resp.Route)
	}
	if string(resp.Segments[0].Data) != `{"id":"2"}` {
		t.Errorf("data %s", resp.Segments[0].Data)
	}
	if resp.Build != h.BuildID() {
		t.Errorf("build %q, want %q", resp.Build, h.BuildID())
	}
	if len(resp.Head.Meta) != 1 || resp.Head.Meta[0].Content != "site" {
		t.Errorf("head meta %+v: the kept root layout sets it", resp.Head.Meta)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "private" {
		t.Errorf("Cache-Control %q: the kept section did not run", cc)
	}
	_, rec = navigate(t, h, "/s/2?q=b", dataHeaders(from))
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=5" {
		t.Errorf("Cache-Control %q, want the section's", cc)
	}

	resp, _ = navigate(t, h, "/s/9?q=a", dataHeaders(from))
	last := resp.Segments[len(resp.Segments)-1]
	if last.Error == nil || last.Error.Status != 404 || last.Data != nil {
		t.Errorf("error segment %+v", last)
	}
	// The error segment has the key the document gives it.
	if doc := keysOf(t, h, "/s/9?q=a"); last.Key != doc[2] {
		t.Errorf("error key %s, document %s", last.Key, doc[2])
	}
}

func TestNavigateAnswers(t *testing.T) {
	routes, counts := navRoutes()
	h := newHandler(t, Config{Routes: routes, BuildID: "b1"})
	data := protocol.FormatNavigate(protocol.ModeData)
	cases := []struct {
		name     string
		target   string
		headers  map[string]string
		want     protocol.Type
		reason   string
		location string
		status   int
	}{
		{name: "loader redirect", target: "/s/3", headers: map[string]string{protocol.HeaderNavigate: data}, want: protocol.TypeRedirect, location: "/s/4", status: 307},
		{name: "trailing slash", target: "/s/1/?q=a", headers: map[string]string{protocol.HeaderNavigate: data}, want: protocol.TypeRedirect, location: "/s/1?q=a", status: 308},
		{name: "document route", target: "/doc", headers: map[string]string{protocol.HeaderNavigate: data}, want: protocol.TypeReload, reason: protocol.ReasonDocument},
		{name: "no Error.vela", target: "/bare/1", headers: map[string]string{protocol.HeaderNavigate: data}, want: protocol.TypeReload, reason: protocol.ReasonDocument},
		{name: "other version", target: "/s/1", headers: map[string]string{protocol.HeaderNavigate: "data;v=2"}, want: protocol.TypeReload, reason: protocol.ReasonProtocol},
		{name: "no version", target: "/s/1", headers: map[string]string{protocol.HeaderNavigate: "data"}, want: protocol.TypeReload, reason: protocol.ReasonProtocol},
		{name: "unknown mode", target: "/s/1", headers: map[string]string{protocol.HeaderNavigate: "html;v=1"}, want: protocol.TypeReload, reason: protocol.ReasonProtocol},
		{name: "other build", target: "/s/1", headers: map[string]string{protocol.HeaderNavigate: data, protocol.HeaderBuild: "b0"}, want: protocol.TypeReload, reason: protocol.ReasonBuild},
		{name: "same build", target: "/s/1", headers: map[string]string{protocol.HeaderNavigate: data, protocol.HeaderBuild: "b1"}, want: protocol.TypeData},
		{name: "auto", target: "/s/1", headers: map[string]string{protocol.HeaderNavigate: protocol.FormatNavigate(protocol.ModeAuto)}, want: protocol.TypeData},
		{name: "fragment until 077", target: "/s/1", headers: map[string]string{protocol.HeaderNavigate: protocol.FormatNavigate(protocol.ModeFragment)}, want: protocol.TypeData},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, _ := navigate(t, h, c.target, c.headers)
			if resp.Type != c.want || resp.Reason != c.reason || resp.Location != c.location || resp.Status != c.status {
				t.Errorf("got %s %q %q %d, want %s %q %q %d", resp.Type, resp.Reason, resp.Location, resp.Status, c.want, c.reason, c.location, c.status)
			}
		})
	}
	counts.take()
	navigate(t, h, "/doc", map[string]string{protocol.HeaderNavigate: data})
	if ran := counts.take(); len(ran) != 0 {
		t.Errorf("a reload ran loaders: %v", ran)
	}
	// HEAD gets the headers without the body.
	req := httptest.NewRequest(http.MethodHead, "/s/1", nil)
	req.Header.Set(protocol.HeaderNavigate, data)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Body.Len() != 0 || rec.Header().Get("Content-Length") == "0" {
		t.Errorf("HEAD: %d %q, Content-Length %s", rec.Code, rec.Body, rec.Header().Get("Content-Length"))
	}
}

// TestNavigateMalformedHave: X-Vela-Have is untrusted (N-3). A malformed
// value is empty, so every level is sent; data never comes from it.
func TestNavigateMalformedHave(t *testing.T) {
	routes, _ := navRoutes()
	h := newHandler(t, Config{Routes: routes})
	from := keysOf(t, h, "/s/1?q=a")
	long := strings.Repeat("k", protocol.MaxKeyLen+1)
	many := make([]string, protocol.MaxHaveKeys+1)
	for i := range many {
		many[i] = from[0]
	}
	for name, have := range map[string]string{
		"bad character": from[0] + ",a b," + from[2],
		"long key":      from[0] + "," + long,
		"too many keys": strings.Join(many, ","),
		"too long":      from[0] + "," + strings.Repeat("a,", protocol.MaxHaveBytes/2),
		"empty key":     from[0] + ",," + from[2],
		"spaces only":   " ",
	} {
		t.Run(name, func(t *testing.T) {
			resp, _ := navigate(t, h, "/s/2?q=a", map[string]string{protocol.HeaderNavigate: protocol.FormatNavigate(protocol.ModeData), protocol.HeaderHave: have})
			if resp.Start != 0 || len(resp.Segments) != 3 {
				t.Errorf("start %d, %d segments; want 0 and 3", resp.Start, len(resp.Segments))
			}
		})
	}
	// Two X-Vela-Have headers are malformed as well.
	req := httptest.NewRequest(http.MethodGet, "/s/2?q=a", nil)
	req.Header.Set(protocol.HeaderNavigate, protocol.FormatNavigate(protocol.ModeData))
	req.Header.Add(protocol.HeaderHave, protocol.FormatHave(from))
	req.Header.Add(protocol.HeaderHave, protocol.FormatHave(from))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var body struct{ Start int }
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Start != 0 {
		t.Errorf("two headers: start %d (%v)", body.Start, err)
	}
	// A key the server would never produce only matches nothing.
	resp, _ := navigate(t, h, "/s/2?q=a", dataHeaders([]string{from[0], protocol.SegmentKey("forged")}))
	if resp.Start != 1 {
		t.Errorf("forged key: start %d, want 1", resp.Start)
	}
}

func newHandler(t *testing.T, cfg Config) *Handler {
	t.Helper()
	h, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return h
}
