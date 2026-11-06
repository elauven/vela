// Package routes holds the loaders of src/routes: the function of a
// component returns its data prop.
package routes

import (
	"context"

	"github.com/elauven/vela/app"
)

// Site is the data of Layout.
type Site struct {
	Site string `json:"site"`
}

// Greeting is the data of Page.
type Greeting struct {
	Greeting string `json:"greeting"`
}

// Layout loads the data of every page.
func Layout(ctx context.Context, r *app.Request) (Site, error) {
	return Site{Site: "__NAME__"}, nil
}

// Page loads the data of /.
func Page(ctx context.Context, r *app.Request) (Greeting, error) {
	r.Head().Title = "Home"
	return Greeting{Greeting: "Hello from Go"}, nil
}
