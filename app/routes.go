package app

import (
	"context"
	"io"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	vela "github.com/elauven/vela/ssr_runtime"
)

// RootID is the id of the element that holds the VelaApp root in the
// document. The generated client entry passes the same id to start().
const RootID = "vela-app"

// Deps declares the dependencies of the loaders of one directory beyond the
// parameters of its path (L-6): `var Deps = app.Deps{Query: []string{"q"}}`
// in the mirrored load.go.
//
// Keys are names of data the loaders read (task 097), such as "cart":
// invalidate('cart') in the browser runs the levels that declare it again,
// with the levels below them, and keeps the levels above. A key is 1 to 64
// characters of letters, digits and _ - . :
type Deps struct {
	Query []string
	Keys  []string
}

// ValidDependencyKey reports whether k can be a key of Deps.Keys.
func ValidDependencyKey(k string) bool {
	if k == "" || len(k) > 64 {
		return false
	}
	for i := 0; i < len(k); i++ {
		c := k[i]
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '_' || c == '-' || c == '.' || c == ':') {
			return false
		}
	}
	return true
}

// RenderMode is the response mode a page declares with
// `const Render = app.RenderData` in its load.go (L-7, R-8).
type RenderMode string

const (
	RenderAuto     RenderMode = "auto"
	RenderData     RenderMode = "data"
	RenderFragment RenderMode = "fragment"
	RenderDocument RenderMode = "document"
	// RenderClient renders the page only in the browser (task 092): the
	// document has the layouts above it and an empty page level, with the
	// page's data in the payload, and the client mounts the page after
	// hydration. vela-app gen gives the page <script context="client">, so
	// it needs no Go renderer.
	RenderClient RenderMode = "client"
)

// Params are the parameters of one URL of a route, by name.
type Params map[string]string

// Prerender renders a page when vela-app build runs (task 092):
// `var Prerender = app.Prerender{}` in the page's load.go. The server
// serves the files without running the loaders. A route with parameters
// lists them in Entries; the build also follows the links of prerendered
// pages to other prerenderable URLs.
type Prerender struct {
	// Entries returns the parameters of the URLs to prerender; nil
	// prerenders only the URLs the crawl finds (and "/" for a route
	// without parameters).
	Entries func(ctx context.Context) ([]Params, error)
}

// Cache keeps the responses of a page in the server's response cache
// (task 092): `var Cache = app.Cache{MaxAge: time.Minute}` in its
// load.go. A response is fresh for MaxAge; for StaleWhileRevalidate after
// that it is served at once while one background render replaces it.
// Responses that read a cookie or an app.Local are never cached.
// Cache-Control tells CDNs the same.
type Cache struct {
	MaxAge               time.Duration
	StaleWhileRevalidate time.Duration
}

// Loader is a loader with its result type erased. The generated route table
// wraps the typed loaders of load.go with LoaderOf.
type Loader func(ctx context.Context, r *Request) (any, error)

// LoaderOf wraps a typed loader (docs/APP.md L-1).
func LoaderOf[T any](load func(ctx context.Context, r *Request) (T, error)) Loader {
	return func(ctx context.Context, r *Request) (any, error) {
		return load(ctx, r)
	}
}

// SegmentKind is the kind of one URL segment of a pattern.
type SegmentKind uint8

const (
	// Static matches its text exactly.
	Static SegmentKind = iota
	// Param matches one non-empty segment, which its matcher accepts when
	// it names one. An optional segment is a Param of the pattern variant
	// that has it.
	Param
	// Rest matches the remaining segments, possibly none.
	Rest
)

// Segment is one segment of a pattern.
type Segment struct {
	Kind SegmentKind
	// Value is the text of a Static segment and the parameter name
	// otherwise.
	Value string
	// Matcher names the matcher of a Param ("" for none).
	Matcher string
}

// Pattern is one URL pattern of a route. A route with optional segments has
// one pattern per combination of them (docs/APP.md P-8).
type Pattern struct {
	// Route indexes Routes.Routes.
	Route    int
	Segments []Segment
}

// Level is one level of a route's chain: a layout or the page (P-10).
type Level struct {
	// ID is the segment id (P-11), for example "routes/users/Layout".
	ID string
	// Error is the segment id of the nearest Error.vela at or above the
	// level's directory ("" for none, R-3).
	Error string
	// Loader loads the level's data; nil gives data null (L-1).
	Loader Loader
	// Params are the parameters of the level's own path, its default
	// dependencies (L-6).
	Params []string
	// Deps are the declared extra dependencies.
	Deps Deps
	// Head reports that the loader may set the head: it, or a helper of its
	// package, calls Head(). A data navigation runs it for the head even
	// when the client keeps the level (N-4); other kept levels do not run.
	Head bool
	// Guard protects the level and the levels below it (G-1): it runs
	// before their loaders and before the page's action. The generated
	// table sets it from guard.go on the first level of a directory.
	Guard Guard
}

// Guard decides whether a request may reach the levels below it (G-2): nil
// lets it through; Redirect, NotFound, Error or another error stop it.
type Guard func(ctx context.Context, r *Request) error

// Route is one route of the table.
type Route struct {
	// ID is the route id (P-12).
	ID     string
	Chain  []Level
	Render RenderMode
	// Actions are the form actions of the page (task 078); nil for none.
	Actions Actions
	// Prerender and Cache are the page's rendering rules (task 092); nil
	// for none.
	Prerender *Prerender
	Cache     *Cache
}

// RootProps are the props of the generated VelaApp root (R-2).
type RootProps struct {
	Route any
	Chain []any
	Start int
}

// Root renders the generated VelaApp root.
type Root struct {
	// ID is the component id of VelaApp ("_app/VelaApp").
	ID     string
	Render func(ctx context.Context, w io.Writer, props RootProps) error
}

// Routes is the route table that vela-app generates (appgen.Routes()).
type Routes struct {
	Routes []Route
	// Patterns are in ranking order (P-8): the first full match wins.
	Patterns []Pattern
	// Matchers are the built-in matchers and the project's own (P-13, P-14).
	Matchers map[string]func(segment string) bool
	// Error is the segment id of the root Error.vela, used when no route
	// matches ("" for none).
	Error string
	// NotFound is the chain of a URL no route matches: the layouts of the
	// routes directory itself, which render around the root Error.vela.
	NotFound Route
	// Components are the server values of the route components by segment
	// id, which static fragments render (task 077).
	Components map[string]vela.Component
	// Depth is the maximum chain length.
	Depth int
	Root  Root
	// Source is the project directory relative to the Vite root ("src"):
	// a segment id plus ".vela" under it is the module of a component.
	Source string
	// Entry is the client entry, "src/_app/client.js".
	Entry string
}

// Match is the outcome of matching a path.
type Match struct {
	Route  *Route
	Params map[string]string
}

// Match finds the route of a URL path (docs/APP.md P-6, P-8). The path is
// split at / before percent-decoding, so %2F stays inside its segment. A
// path that is not absolute or cannot be decoded matches nothing. The path
// is expected without a trailing slash (P-7).
func (t Routes) Match(path string) (Match, bool) {
	segments, ok := splitPath(path)
	if !ok {
		return Match{}, false
	}
	for _, pattern := range t.Patterns {
		if params, ok := t.matchPattern(pattern.Segments, segments); ok {
			return Match{Route: &t.Routes[pattern.Route], Params: params}, true
		}
	}
	return Match{}, false
}

func (t Routes) matchPattern(pattern []Segment, segments []string) (map[string]string, bool) {
	params := map[string]string{}
	for i, segment := range pattern {
		if segment.Kind == Rest {
			params[segment.Value] = strings.Join(segments[min(i, len(segments)):], "/")
			return params, true
		}
		if i >= len(segments) {
			return nil, false
		}
		value := segments[i]
		switch segment.Kind {
		case Static:
			if value != segment.Value {
				return nil, false
			}
		case Param:
			if value == "" {
				return nil, false
			}
			if segment.Matcher != "" {
				match := t.Matchers[segment.Matcher]
				if match == nil || !match(value) {
					return nil, false
				}
			}
			params[segment.Value] = value
		}
	}
	if len(pattern) != len(segments) {
		return nil, false
	}
	return params, true
}

// splitPath splits an absolute path into decoded segments. "/" has none.
func splitPath(path string) ([]string, bool) {
	if !strings.HasPrefix(path, "/") {
		return nil, false
	}
	if path == "/" {
		return nil, true
	}
	parts := strings.Split(path[1:], "/")
	for i, part := range parts {
		decoded, err := url.PathUnescape(part)
		if err != nil || !utf8.ValidString(decoded) {
			// decodeURIComponent of the client rejects both.
			return nil, false
		}
		parts[i] = decoded
	}
	return parts, true
}
