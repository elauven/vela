package app

import (
	"net/http"
	"net/url"

	"github.com/elauven/vela/app/internal/value"
)

// Request is what a loader receives (docs/APP.md L-3). Every level of a
// chain gets its own Request; Params, Query, HTTP and Route are shared.
type Request struct {
	// Params are the decoded parameters of the route (P-6). An absent
	// optional parameter has no entry.
	Params map[string]string
	// Query is the query of the URL.
	Query url.Values
	// HTTP is the request being served.
	HTTP *http.Request
	// Route is the route id (P-12), for example "/users/[id=int]".
	Route string

	head   Head
	header http.Header
	run    *chainRun
	level  int
}

// Head returns the title and <meta> tags of the loader's segment (R-6).
// The deepest segment that sets a title wins; a deeper segment replaces a
// meta tag with the same name or property.
func (r *Request) Head() *Head {
	return &r.head
}

// Header returns response headers of the loader's segment, such as
// Cache-Control. A deeper segment replaces a header set above it.
func (r *Request) Header() http.Header {
	if r.header == nil {
		r.header = http.Header{}
	}
	return r.header
}

// CacheControl sets the Cache-Control header of the response. Responses
// are private by default.
func (r *Request) CacheControl(value string) {
	r.Header().Set("Cache-Control", value)
}

// Parent waits for the result of the nearest layout above the loader's
// level that has a loader, and converts it to T through JSON (L-4). It
// returns that loader's error when it fails, and ErrNoParent when there is
// no such layout.
func Parent[T any](r *Request) (T, error) {
	var zero T
	if r.run == nil {
		return zero, ErrNoParent
	}
	for i := r.level - 1; i >= 0; i-- {
		if r.run.levels[i].level.Loader == nil {
			continue
		}
		result, err := r.run.wait(i)
		if err != nil {
			return zero, err
		}
		return value.Into[T](result)
	}
	return zero, ErrNoParent
}
