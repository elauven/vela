// Package about holds the rendering rules of src/routes/[[lang]]/about.
package about

import (
	"context"

	"github.com/elauven/vela/app"
)

// Prerender: vela-app build writes the about pages as files (task 092),
// one per language; the server answers them without rendering.
var Prerender = app.Prerender{Entries: func(ctx context.Context) ([]app.Params, error) {
	return []app.Params{{}, {"lang": "de"}}, nil
}}
