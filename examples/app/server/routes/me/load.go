// Package me redirects /me to the signed-in user.
package me

import (
	"context"

	"github.com/elauven/vela/app"
)

// Page redirects; a loader may answer any request with a redirect.
func Page(ctx context.Context, r *app.Request) (any, error) {
	return nil, app.Redirect(303, "/users/1")
}
