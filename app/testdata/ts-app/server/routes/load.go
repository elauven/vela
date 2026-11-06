// Package routes holds the loaders of src/routes.
package routes

import (
	"context"

	"github.com/elauven/vela/app"
)

type Site struct {
	Site string `json:"site"`
}

// Layout loads the site name.
func Layout(ctx context.Context, r *app.Request) (Site, error) {
	return Site{Site: "TS App"}, nil
}
