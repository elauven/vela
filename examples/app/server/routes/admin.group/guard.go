// Package admin_group protects src/routes/(admin) (docs/APP.md G-1).
package admin_group

import (
	"context"
	"net/url"

	"example.com/site/internal/auth"
	"github.com/elauven/vela/app"
)

// Guard runs before every loader and action of the (admin) pages: an
// anonymous visitor goes to the sign-in page, and comes back after it.
func Guard(ctx context.Context, r *app.Request) error {
	if _, ok := auth.Current.Get(ctx); !ok {
		return app.Redirect(303, "/login?next="+url.QueryEscape(r.HTTP.URL.RequestURI()))
	}
	return nil
}
