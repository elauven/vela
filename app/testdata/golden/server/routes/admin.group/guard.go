// Package admin_group protects src/routes/(admin) (docs/APP.md G-1).
package admin_group

import (
	"context"

	"github.com/elauven/vela/app"
)

// Guard runs before every loader and action below (admin).
func Guard(ctx context.Context, r *app.Request) error {
	if r.HTTP.Header.Get("X-Admin") == "" {
		return app.Redirect(303, "/")
	}
	return nil
}
