// Package docs holds the loaders of src/routes/docs, a static section.
package docs

import (
	"context"

	"example.com/site/server/store"
	"github.com/elauven/vela/app"
)

type TOC struct {
	Pages []store.DocLink `json:"pages"`
}

// Layout loads the table of contents.
func Layout(ctx context.Context, r *app.Request) (TOC, error) {
	return TOC{Pages: store.DocLinks()}, nil
}
