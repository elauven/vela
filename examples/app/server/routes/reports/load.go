// Package reports holds the loaders of src/routes/reports.
package reports

import (
	"context"
	"time"

	"example.com/site/server/store"
	"github.com/elauven/vela/app"
)

type Summary struct {
	Count int `json:"count"`
}

// Layout loads the header of the reports.
func Layout(ctx context.Context, r *app.Request) (Summary, error) {
	return Summary{Count: len(store.Reports())}, nil
}

type List struct {
	Reports []store.Report `json:"reports"`
}

// Page lists the reports.
func Page(ctx context.Context, r *app.Request) (List, error) {
	r.Head().Title = "Reports"
	return List{Reports: store.Reports()}, nil
}

// Render: the list is a data navigation (docs/APP.md L-7).
const Render = app.RenderData

// Cache: the list is the same for every visitor; the server keeps it for a
// minute and serves a stale one for an hour while it renders it again
// (task 092).
var Cache = app.Cache{MaxAge: time.Minute, StaleWhileRevalidate: time.Hour}
