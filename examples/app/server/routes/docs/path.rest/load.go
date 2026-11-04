// Package path_rest holds the loader of src/routes/docs/[...path].
package path_rest

import (
	"context"

	"example.com/site/server/store"
	"github.com/elauven/vela/app"
)

// Page loads one page of the docs; an unknown path is 404.
func Page(ctx context.Context, r *app.Request) (store.Doc, error) {
	doc, ok := store.FindDoc(r.Params["path"])
	if !ok {
		return store.Doc{}, app.NotFound()
	}
	r.Head().Title = doc.Title + " · Docs"
	return doc, nil
}
