// Package notes holds the loader of src/routes/notes.
package notes

import (
	"context"

	"github.com/elauven/vela/app"
)

type Notes struct {
	Notes []string `json:"notes"`
}

// Page loads the notes; the browser renders them (RenderClient).
func Page(ctx context.Context, r *app.Request) (Notes, error) {
	r.Head().Title = "Notes"
	return Notes{Notes: []string{"Buy milk"}}, nil
}

// Render: the page is rendered only in the browser (task 092); vela-app
// gen gives src/routes/notes/Page.vela <script context="client">.
const Render = app.RenderClient
