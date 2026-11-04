package protocol

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestParseNavigate(t *testing.T) {
	for _, tc := range []struct {
		in   string
		mode Mode
		ok   bool
	}{
		{"auto;v=1", ModeAuto, true},
		{"data;v=1", ModeData, true},
		{"fragment; v=1", ModeFragment, true},
		{" data ; x=y; v=1", ModeData, true},
		{"data", "", false},     // no version
		{"data;v=2", "", false}, // another version
		{"document;v=1", "", false},
		{"", "", false},
	} {
		mode, err := ParseNavigate(tc.in)
		if tc.ok != (err == nil) || mode != tc.mode {
			t.Errorf("ParseNavigate(%q) = %q, %v", tc.in, mode, err)
		}
		if err != nil && !errors.Is(err, ErrVersion) {
			t.Errorf("ParseNavigate(%q): %v is not ErrVersion", tc.in, err)
		}
	}
	for _, m := range []Mode{ModeAuto, ModeData, ModeFragment} {
		if got, err := ParseNavigate(FormatNavigate(m)); err != nil || got != m {
			t.Errorf("round trip of %q: %q, %v", m, got, err)
		}
	}
}

func TestParseHave(t *testing.T) {
	long := strings.Repeat("a", MaxKeyLen)
	for _, tc := range []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"one", "abc", []string{"abc"}},
		{"several with spaces", "a1, b-2 ,c_3", []string{"a1", "b-2", "c_3"}},
		{"longest key", long, []string{long}},
		{"key too long", long + "a", nil},
		{"invalid character", "a,b/c", nil},
		{"empty key", "a,,b", nil},
		{"trailing comma", "a,", nil},
		{"too many keys", strings.Repeat("k,", MaxHaveKeys) + "k", nil},
		{"too long", strings.Repeat("k", MaxHaveBytes+1), nil},
	} {
		if got := ParseHave(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: ParseHave(%.40q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
	keys := []string{SegmentKey("routes/Layout"), SegmentKey("routes/users/Page")}
	if got := ParseHave(FormatHave(keys)); !reflect.DeepEqual(got, keys) {
		t.Errorf("round trip: %q, want %q", got, keys)
	}
	max := make([]string, MaxHaveKeys)
	for i := range max {
		max[i] = SegmentKey("routes/Layout", string(rune('a'+i)))
	}
	if got := ParseHave(FormatHave(max)); len(got) != MaxHaveKeys {
		t.Errorf("%d keys of SegmentKey do not fit the limits: %d parsed", MaxHaveKeys, len(got))
	}
}

func TestParseRequest(t *testing.T) {
	h := http.Header{}
	if _, ok, err := ParseRequest(h); ok || err != nil {
		t.Fatalf("document request: ok=%v err=%v", ok, err)
	}
	h.Set(HeaderNavigate, FormatNavigate(ModeData))
	h.Set(HeaderHave, "a,b")
	h.Set(HeaderBuild, "b1")
	req, ok, err := ParseRequest(h)
	if !ok || err != nil || !reflect.DeepEqual(req, Request{Mode: ModeData, Have: []string{"a", "b"}, Build: "b1"}) {
		t.Fatalf("ParseRequest = %+v, %v, %v", req, ok, err)
	}
	h.Set(HeaderHave, "a,b/")
	if req, _, _ := ParseRequest(h); req.Have != nil {
		t.Errorf("malformed X-Vela-Have kept: %q", req.Have)
	}
	h.Add(HeaderHave, "c")
	if req, _, _ := ParseRequest(h); req.Have != nil {
		t.Errorf("two X-Vela-Have headers kept: %q", req.Have)
	}
	h.Set(HeaderNavigate, "data;v=9")
	if _, ok, err := ParseRequest(h); !ok || !errors.Is(err, ErrVersion) {
		t.Errorf("another version: ok=%v err=%v", ok, err)
	}
	h.Set(HeaderNavigate, FormatNavigate(ModeData))
	h.Add(HeaderNavigate, FormatNavigate(ModeAuto))
	if _, ok, err := ParseRequest(h); !ok || !errors.Is(err, ErrVersion) {
		t.Errorf("two X-Vela-Navigate headers: ok=%v err=%v", ok, err)
	}
}

func TestSegmentKey(t *testing.T) {
	k := SegmentKey("routes/users/[id]/Page", "id", "42")
	if !ValidKey(k) || len(k) != 22 {
		t.Fatalf("SegmentKey = %q", k)
	}
	if k != SegmentKey("routes/users/[id]/Page", "id", "42") {
		t.Error("SegmentKey is not deterministic")
	}
	distinct := []string{
		k,
		SegmentKey("routes/users/[id]/Page", "id", "43"),
		SegmentKey("routes/users/[id]/Page", "id4", "2"), // same concatenation
		SegmentKey("routes/users/[id]/Page", "id42"),
		SegmentKey("routes/users/[id]/Layout", "id", "42"),
		SegmentKey("routes/users/[id]/Page"),
	}
	seen := map[string]int{}
	for i, key := range distinct {
		if j, dup := seen[key]; dup {
			t.Errorf("inputs %d and %d have the same key %q", j, i, key)
		}
		seen[key] = i
	}
}

func TestStart(t *testing.T) {
	for _, tc := range []struct {
		have, keys []string
		want       int
	}{
		{nil, []string{"r", "u", "p1"}, 0},
		{[]string{"r", "u", "p1"}, []string{"r", "u", "p2"}, 2},
		{[]string{"r", "x", "p1"}, []string{"r", "u", "p1"}, 1},
		{[]string{"r", "u", "p1"}, []string{"r", "u", "p1"}, 3},
		{[]string{"r", "u"}, []string{"r", "u", "p1"}, 2},
		{[]string{"r", "u", "p1", "q"}, []string{"r", "u"}, 2},
		{[]string{"x"}, []string{"r"}, 0},
	} {
		if got := Start(tc.have, tc.keys); got != tc.want {
			t.Errorf("Start(%q, %q) = %d, want %d", tc.have, tc.keys, got, tc.want)
		}
	}
}

func route() *Route {
	return &Route{ID: "/users/[id=int]", URL: "/users/2?tab=a", Path: "/users/2",
		Params: map[string]string{"id": "2"}, Query: map[string]string{"tab": "a"}}
}

func TestEncodeDataResponse(t *testing.T) {
	key := SegmentKey("routes/users/[id=int]/Page", "id", "2")
	b, err := Encode(Response{
		Type: TypeData, Build: "b1", Route: route(), Start: 2,
		Segments: []Segment{{ID: "routes/users/[id=int]/Page", Key: key, Data: json.RawMessage(`{"name":"Ada"}`)}},
		Head:     &Head{Title: "User 2", Meta: []Meta{{Name: "description", Content: "d"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"v":1,"type":"data","build":"b1",` +
		`"route":{"id":"/users/[id=int]","url":"/users/2?tab=a","path":"/users/2","params":{"id":"2"},"query":{"tab":"a"}},` +
		`"start":2,"segments":[{"id":"routes/users/[id=int]/Page","key":"` + key + `","data":{"name":"Ada"}}],` +
		`"head":{"title":"User 2","meta":[{"name":"description","content":"d"}]}}`
	if string(b) != want {
		t.Fatalf("Encode =\n%s\nwant\n%s", b, want)
	}
}

func TestEncodeShapes(t *testing.T) {
	for _, tc := range []struct {
		name string
		r    Response
		want string
	}{
		{"null data, start 0, empty params", Response{Type: TypeData, Build: "b", Route: &Route{ID: "/", URL: "/", Path: "/"},
			Segments: []Segment{{ID: "routes/Page", Key: "k"}}},
			`{"v":1,"type":"data","build":"b","route":{"id":"/","url":"/","path":"/","params":{},"query":{}},"start":0,"segments":[{"id":"routes/Page","key":"k","data":null}]}`},
		{"no segments", Response{Type: TypeData, Build: "b", Route: &Route{ID: "/", URL: "/", Path: "/"}, Start: 1},
			`{"v":1,"type":"data","build":"b","route":{"id":"/","url":"/","path":"/","params":{},"query":{}},"start":1,"segments":[]}`},
		{"error segment", Response{Type: TypeData, Build: "b", Route: &Route{ID: "/", URL: "/x", Path: "/x"}, Start: 1,
			Segments: []Segment{{ID: "routes/Error", Key: "k", Data: json.RawMessage(`1`), Error: &SegmentError{Status: 404, Message: "Not Found"}}}},
			`{"v":1,"type":"data","build":"b","route":{"id":"/","url":"/x","path":"/x","params":{},"query":{}},"start":1,"segments":[{"id":"routes/Error","key":"k","error":{"status":404,"message":"Not Found"}}]}`},
		{"fragment", Response{Type: TypeFragment, Build: "b", Route: &Route{ID: "/", URL: "/", Path: "/"}, Fragment: &Fragment{HTML: "<p>x</p>", Static: true}},
			`{"v":1,"type":"fragment","build":"b","route":{"id":"/","url":"/","path":"/","params":{},"query":{}},"start":0,"segments":[],"fragment":{"html":"\u003cp\u003ex\u003c/p\u003e","static":true}}`},
		{"redirect drops data fields", Response{Type: TypeRedirect, Build: "b", Location: "/login", Status: 303, Route: route(), Start: 4},
			`{"v":1,"type":"redirect","build":"b","location":"/login","status":303}`},
		{"reload", Response{Type: TypeReload, Build: "b", Reason: ReasonBuild},
			`{"v":1,"type":"reload","build":"b","reason":"build"}`},
		{"form", Response{Type: TypeForm, Build: "b", Status: 422, Form: json.RawMessage(`{"name":"required"}`), Route: route()},
			`{"v":1,"type":"form","build":"b","status":422,"form":{"name":"required"}}`},
		{"form without value", Response{Type: TypeForm, Build: "b", Status: 400},
			`{"v":1,"type":"form","build":"b","status":400,"form":null}`},
	} {
		b, err := Encode(tc.r)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if string(b) != tc.want {
			t.Errorf("%s:\n got %s\nwant %s", tc.name, b, tc.want)
		}
	}
}

func TestEncodeRejectsInvalidResponses(t *testing.T) {
	for _, tc := range []struct {
		name string
		r    Response
		msg  string
	}{
		{"no build", Response{Type: TypeReload, Reason: ReasonBuild}, "no build id"},
		{"unknown type", Response{Type: "html", Build: "b"}, "unknown response type"},
		{"form status", Response{Type: TypeForm, Build: "b", Status: 200}, "not an error"},
		{"no route", Response{Type: TypeData, Build: "b"}, "no route"},
		{"negative start", Response{Type: TypeData, Build: "b", Route: route(), Start: -1}, "no start"},
		{"segment without id", Response{Type: TypeData, Build: "b", Route: route(), Segments: []Segment{{Key: "k"}}}, "no id"},
		{"invalid key", Response{Type: TypeData, Build: "b", Route: route(), Segments: []Segment{{ID: "s", Key: "a/b"}}}, "invalid key"},
		{"error not last", Response{Type: TypeData, Build: "b", Route: route(),
			Segments: []Segment{{ID: "e", Key: "k", Error: &SegmentError{Status: 500}}, {ID: "p", Key: "k2"}}}, "not the last"},
		{"fragment without HTML", Response{Type: TypeFragment, Build: "b", Route: route()}, "no fragment"},
		{"fragment in data", Response{Type: TypeData, Build: "b", Route: route(), Fragment: &Fragment{}}, "a fragment in a data response"},
		{"redirect without location", Response{Type: TypeRedirect, Build: "b", Status: 303}, "no location"},
		{"redirect status", Response{Type: TypeRedirect, Build: "b", Location: "/", Status: 200}, "not a redirect"},
		{"reload reason", Response{Type: TypeReload, Build: "b", Reason: "tired"}, "unknown reason"},
	} {
		_, err := Encode(tc.r)
		if err == nil || !strings.Contains(err.Error(), tc.msg) {
			t.Errorf("%s: error %v, want %q", tc.name, err, tc.msg)
		}
	}
}

func TestDecodeRoundTrip(t *testing.T) {
	for _, r := range []Response{
		{Type: TypeData, Build: "b", Route: route(), Start: 1, Head: &Head{Title: "t"},
			Segments: []Segment{{ID: "a", Key: "k1", Data: json.RawMessage(`[1,2]`)}, {ID: "b", Key: "k2"}}},
		{Type: TypeData, Build: "b", Route: route(), Start: 0, Segments: []Segment{{ID: "e", Key: "k", Error: &SegmentError{Status: 404, Message: "m"}}}},
		{Type: TypeFragment, Build: "b", Route: route(), Start: 2, Segments: []Segment{}, Fragment: &Fragment{HTML: "<b>", Static: false}},
		{Type: TypeRedirect, Build: "b", Location: "/x", Status: 307},
		{Type: TypeReload, Build: "b", Reason: ReasonProtocol},
		{Type: TypeForm, Build: "b", Status: 422, Form: json.RawMessage(`[1]`)},
	} {
		b, err := Encode(r)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Decode(b)
		if err != nil {
			t.Fatalf("Decode(%s): %v", b, err)
		}
		if r.Segments == nil && (r.Type == TypeData || r.Type == TypeFragment) {
			r.Segments = []Segment{}
		}
		if !reflect.DeepEqual(got, r) {
			t.Errorf("round trip of %s:\n got %+v\nwant %+v", b, got, r)
		}
	}
}

func TestDecodeRejects(t *testing.T) {
	for _, tc := range []struct {
		in      string
		version bool
		msg     string
	}{
		{`{"v":2,"type":"reload","build":"b","reason":"build"}`, true, "version 2"},
		{`{"type":"reload","build":"b","reason":"build"}`, true, "version 0"},
		{`{"v":1,"type":"html","build":"b"}`, false, "unknown response type"},
		{`{"v":1,"type":"data","build":"b","route":{"id":"/","url":"/","path":"/","params":{},"query":{}},"segments":[]}`, false, "no start"},
		{`not json`, false, "invalid character"},
	} {
		_, err := Decode([]byte(tc.in))
		if err == nil || !strings.Contains(err.Error(), tc.msg) || errors.Is(err, ErrVersion) != tc.version {
			t.Errorf("Decode(%s): %v, want %q (version error %v)", tc.in, err, tc.msg, tc.version)
		}
	}
}
