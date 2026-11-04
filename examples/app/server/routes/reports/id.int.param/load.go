// Package id_int_param holds the loader of src/routes/reports/[id=int].
package id_int_param

import (
	"context"
	"strconv"

	"example.com/site/server/store"
	"github.com/elauven/vela/app"
)

// Page loads one report; an unknown id is 404.
func Page(ctx context.Context, r *app.Request) (store.Report, error) {
	id, _ := strconv.Atoi(r.Params["id"])
	for _, report := range store.Reports() {
		if report.ID == id {
			r.Head().Title = report.Name + " · Reports"
			return report, nil
		}
	}
	return store.Report{}, app.NotFound()
}

// Render: the server renders a report and the client hydrates it
// (docs/APP.md L-7, R-8).
const Render = app.RenderFragment
