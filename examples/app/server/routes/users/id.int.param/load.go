// Package id_int_param holds the loader of src/routes/users/[id=int].
package id_int_param

import (
	"context"
	"strconv"

	"example.com/site/server/store"
	"github.com/elauven/vela/app"
)

// Page loads one user; an unknown id is 404.
func Page(ctx context.Context, r *app.Request) (store.User, error) {
	id, _ := strconv.Atoi(r.Params["id"])
	for _, user := range store.Users() {
		if user.ID == id {
			r.Head().Title = user.Name + " · Users"
			r.Head().SetMeta(app.Meta{Name: "description", Content: "The profile of " + user.Name})
			return user, nil
		}
	}
	return store.User{}, app.NotFound()
}

// Render keeps user pages as data navigations (docs/APP.md L-7).
const Render = app.RenderData
