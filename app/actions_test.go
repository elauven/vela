package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/elauven/vela/app/internal/protocol"
)

// actionRoutes is navRoutes with actions on /s/[id]: default saves a
// name, delete removes it, and the others fail in the ways of L-5.
func actionRoutes() (Routes, navCounts, *[]string) {
	routes, counts := navRoutes()
	var mu sync.Mutex
	saved := &[]string{}
	routes.Routes[0].Actions = Actions{
		"default": func(ctx context.Context, r *Request) (any, error) {
			var in struct {
				Name  string   `form:"name"`
				Age   int      `json:"age"`
				Admin bool     `form:"admin"`
				Tags  []string `form:"tag"`
			}
			if err := r.Decode(&in); err != nil {
				return nil, err
			}
			if in.Name == "" {
				return nil, Fail(422, map[string]any{"missing": "name", "age": in.Age})
			}
			mu.Lock()
			*saved = append(*saved, strings.Join(append([]string{in.Name, r.Params["id"], strings.Repeat("!", in.Age)}, in.Tags...), " ")+map[bool]string{true: " admin"}[in.Admin])
			mu.Unlock()
			return nil, nil
		},
		"delete": func(ctx context.Context, r *Request) (any, error) {
			parent, err := Parent[section](r)
			if err != nil {
				return nil, err
			}
			mu.Lock()
			*saved = append(*saved, "deleted in "+parent.Q)
			mu.Unlock()
			return nil, nil
		},
		"away":   func(ctx context.Context, r *Request) (any, error) { return nil, Redirect(303, "/s/1") },
		"broken": func(ctx context.Context, r *Request) (any, error) { return nil, errors.New("disk full") },
		"panics": func(ctx context.Context, r *Request) (any, error) { panic("boom") },
		"teapot": func(ctx context.Context, r *Request) (any, error) { return nil, Error(418, "short and stout") },
		"empty":  func(ctx context.Context, r *Request) (any, error) { return nil, Fail(400, nil) },
	}
	return routes, counts, saved
}

func post(t *testing.T, h http.Handler, target string, form url.Values, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestActionsWithoutJavaScript(t *testing.T) {
	routes, _, saved := actionRoutes()
	h := newHandler(t, Config{Routes: routes, Logger: discardLogger()})

	// Success: post/redirect/get to the page, without the action name.
	rec := post(t, h, "/s/2?q=a", url.Values{"name": {"Ada"}, "age": {"2"}, "tag": {"x", "y"}, "admin": {"on"}}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/s/2?q=a" {
		t.Errorf("default: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	rec = post(t, h, "/s/2?/delete&q=b", nil, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/s/2?q=b" {
		t.Errorf("delete: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if got := strings.Join(*saved, "|"); got != "Ada 2 !! x y admin|deleted in b" {
		t.Errorf("actions saw %q", got)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control %q", cc)
	}

	// Fail renders the page with the form prop and the status.
	rec = post(t, h, "/s/2?q=a", url.Values{"age": {"3"}}, nil)
	p := pageOf(t, rec)
	if rec.Code != 422 || len(p.props.Chain) != 3 {
		t.Fatalf("fail: %d, chain %v", rec.Code, p.props.Chain)
	}
	if form, _ := json.Marshal(p.props.Chain[2]["form"]); string(form) != `{"age":3,"missing":"name"}` {
		t.Errorf("form prop %s", form)
	}
	if data, _ := json.Marshal(p.props.Chain[2]["data"]); string(data) != `{"id":"2"}` {
		t.Errorf("the page's loader runs too: %s", data)
	}
	if p.props.Route["url"] != "/s/2?q=a" {
		t.Errorf("route %v", p.props.Route)
	}
	rec = post(t, h, "/s/2?/empty", nil, nil)
	if p := pageOf(t, rec); rec.Code != 400 || p.props.Chain[2]["form"] == nil {
		t.Errorf("fail without value: %d %v", rec.Code, p.props.Chain)
	}

	for _, c := range []struct {
		target, location string
		status           int
		errorID          string
	}{
		{"/s/2?/away", "/s/1", 303, ""},
		{"/s/2?/broken", "", 500, "routes/s/Error"},
		{"/s/2?/panics", "", 500, "routes/s/Error"},
		{"/s/2?/teapot", "", 418, "routes/s/Error"},
		{"/s/2?/nope", "", 404, "routes/s/Error"},
	} {
		rec := post(t, h, c.target, nil, nil)
		if rec.Code != c.status || rec.Header().Get("Location") != c.location {
			t.Errorf("%s: %d %q", c.target, rec.Code, rec.Header().Get("Location"))
		}
		if c.errorID != "" {
			p := pageOf(t, rec)
			if last := p.props.Chain[len(p.props.Chain)-1]; last["id"] != c.errorID || len(p.props.Chain) != 3 {
				t.Errorf("%s: chain %v", c.target, p.props.Chain)
			}
		}
	}

	// Only pages with actions take POST.
	if rec := post(t, h, "/doc", nil, nil); rec.Code != 405 || rec.Header().Get("Allow") != "GET, HEAD" {
		t.Errorf("POST without actions: %d %q", rec.Code, rec.Header().Get("Allow"))
	}
	if rec := post(t, h, "/nope", nil, nil); rec.Code != 405 {
		t.Errorf("POST to no route: %d", rec.Code)
	}
}

func pageOf(t *testing.T, rec *httptest.ResponseRecorder) page {
	t.Helper()
	p := page{status: rec.Code, header: rec.Header(), body: rec.Body.String()}
	if m := propsAttr.FindStringSubmatch(p.body); m != nil {
		if err := json.Unmarshal([]byte(unescape(m[1])), &p.props); err != nil {
			t.Fatalf("props: %v", err)
		}
	}
	return p
}

func TestActionsWithJavaScript(t *testing.T) {
	routes, counts, _ := actionRoutes()
	h := newHandler(t, Config{Routes: routes, Logger: discardLogger()})
	nav := map[string]string{protocol.HeaderNavigate: protocol.FormatNavigate(protocol.ModeAuto), protocol.HeaderHave: protocol.FormatHave(keysOf(t, h, "/s/2?q=a"))}
	decode := func(rec *httptest.ResponseRecorder) protocol.Response {
		t.Helper()
		if rec.Code != 200 {
			t.Fatalf("status %d: %s", rec.Code, rec.Body)
		}
		resp, err := protocol.Decode(rec.Body.Bytes())
		if err != nil {
			t.Fatalf("%v: %s", err, rec.Body)
		}
		return resp
	}
	counts.take()
	// Success: a data response from level 0, every loader ran again.
	resp := decode(post(t, h, "/s/2?q=a", url.Values{"name": {"Ada"}}, nav))
	if resp.Type != protocol.TypeData || resp.Start != 0 || len(resp.Segments) != 3 || resp.Route.URL != "/s/2?q=a" {
		t.Errorf("success: %s start %d, %d segments, route %+v", resp.Type, resp.Start, len(resp.Segments), resp.Route)
	}
	if ran := counts.take(); len(ran) != 3 {
		t.Errorf("loaders after an action: %v", ran)
	}
	// Fail: only the form, and no loader runs.
	resp = decode(post(t, h, "/s/2?q=a", nil, nav))
	if resp.Type != protocol.TypeForm || resp.Status != 422 || string(resp.Form) != `{"age":0,"missing":"name"}` {
		t.Errorf("fail: %+v %s", resp, resp.Form)
	}
	if ran := counts.take(); len(ran) != 0 {
		t.Errorf("loaders after a failure: %v", ran)
	}
	resp = decode(post(t, h, "/s/2?/away", nil, nav))
	if resp.Type != protocol.TypeRedirect || resp.Location != "/s/1" || resp.Status != 303 {
		t.Errorf("redirect: %+v", resp)
	}
	resp = decode(post(t, h, "/s/2?/teapot", nil, nav))
	if last := resp.Segments[len(resp.Segments)-1]; resp.Type != protocol.TypeData || last.Error == nil || last.Error.Status != 418 {
		t.Errorf("error: %+v", resp)
	}
	// Another build or protocol: reload, and the action does not run.
	other := map[string]string{protocol.HeaderNavigate: protocol.FormatNavigate(protocol.ModeAuto), protocol.HeaderBuild: "old"}
	if resp := decode(post(t, h, "/s/2?/away", nil, other)); resp.Type != protocol.TypeReload || resp.Reason != protocol.ReasonBuild {
		t.Errorf("build mismatch: %+v", resp)
	}
}

func TestActionsRejectCrossOrigin(t *testing.T) {
	routes, _, saved := actionRoutes()
	h := newHandler(t, Config{Routes: routes})
	form := url.Values{"name": {"Eve"}}
	for name, headers := range map[string]map[string]string{
		"cross-site fetch": {"Sec-Fetch-Site": "cross-site"},
		"same-site fetch":  {"Sec-Fetch-Site": "same-site"},
		"other origin":     {"Origin": "https://evil.example"},
	} {
		if rec := post(t, h, "/s/2", form, headers); rec.Code != 403 {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	for name, headers := range map[string]map[string]string{
		"same origin":     {"Sec-Fetch-Site": "same-origin"},
		"typed URL":       {"Sec-Fetch-Site": "none"},
		"same Origin":     {"Origin": "http://example.com"},
		"no browser info": {},
	} {
		if rec := post(t, h, "/s/2", form, headers); rec.Code != 303 {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	if len(*saved) != 4 {
		t.Errorf("%d actions ran, want 4", len(*saved))
	}
	// A trusted origin is configured on the protection.
	csrf := http.NewCrossOriginProtection()
	if err := csrf.AddTrustedOrigin("https://partner.example"); err != nil {
		t.Fatal(err)
	}
	h = newHandler(t, Config{Routes: routes, CrossOrigin: csrf})
	if rec := post(t, h, "/s/2", form, map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://partner.example"}); rec.Code != 303 {
		t.Errorf("trusted origin: %d", rec.Code)
	}
}

func TestDecode(t *testing.T) {
	type input struct {
		Name    string
		Count   int     `form:"n"`
		Ratio   float64 `json:"ratio,omitempty"`
		On      bool
		IDs     []uint `form:"id"`
		Skipped string `form:"-"`
		hidden  string
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("name=Ada&n=3&ratio=0.5&on=on&id=1&id=2&Skipped=x&hidden=y"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var in input
	if err := (&Request{HTTP: req}).Decode(&in); err != nil {
		t.Fatal(err)
	}
	if in.Name != "Ada" || in.Count != 3 || in.Ratio != 0.5 || !in.On || len(in.IDs) != 2 || in.IDs[1] != 2 || in.Skipped != "" || in.hidden != "" {
		t.Errorf("decoded %+v", in)
	}
	req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader("n=many"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := (&Request{HTTP: req}).Decode(&in); err == nil || !strings.Contains(err.Error(), "field n") {
		t.Errorf("bad integer: %v", err)
	}
	req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"Name":"Grace","ratio":2}`))
	req.Header.Set("Content-Type", "application/json")
	in = input{}
	if err := (&Request{HTTP: req}).Decode(&in); err != nil || in.Name != "Grace" || in.Ratio != 2 {
		t.Errorf("JSON: %+v %v", in, err)
	}
	if err := (&Request{HTTP: req}).Decode(in); err == nil {
		t.Error("a non-pointer was accepted")
	}
}

func TestActionName(t *testing.T) {
	for raw, want := range map[string][2]string{
		"":               {"default", ""},
		"q=1":            {"default", "q=1"},
		"/delete":        {"delete", ""},
		"/delete&q=1":    {"delete", "q=1"},
		"/delete=&q=1":   {"delete", "q=1"},
		"q=1&/delete":    {"default", "q=1"},
		"/":              {"default", ""},
		"/s%20ave&x=%2F": {"s ave", "x=%2F"},
	} {
		if name, rest := actionName(raw), withoutAction(raw); name != want[0] || rest != want[1] {
			t.Errorf("%q: %q %q, want %q %q", raw, name, rest, want[0], want[1])
		}
	}
}
