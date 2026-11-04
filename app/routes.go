package app

import (
	"context"
	"io"
	"net/url"
	"strings"
	"unicode/utf8"
)

// RootID is the id of the element that holds the VelaApp root in the
// document. The generated client entry passes the same id to start().
const RootID = "vela-app"

// Deps declares the dependencies of the loaders of one directory beyond the
// parameters of its path (L-6): `var Deps = app.Deps{Query: []string{"q"}}`
// in the mirrored load.go.
type Deps struct {
	Query []string
}

// RenderMode is the response mode a page declares with
// `const Render = app.RenderData` in its load.go (L-7, R-8).
type RenderMode string

const (
	RenderAuto     RenderMode = "auto"
	RenderData     RenderMode = "data"
	RenderFragment RenderMode = "fragment"
	RenderDocument RenderMode = "document"
)

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
}

// Route is one route of the table.
type Route struct {
	// ID is the route id (P-12).
	ID     string
	Chain  []Level
	Render RenderMode
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
