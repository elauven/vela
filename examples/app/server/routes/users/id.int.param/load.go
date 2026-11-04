// Package id_int_param holds the loader of src/routes/users/[id=int].
package id_int_param

import (
	"context"
	"strconv"

	"example.com/site/server/store"
	"github.com/elauven/vela/app"
)

// Page loads one user.
func Page(ctx context.Context, r *app.Request) (store.User, error) {
	id, _ := strconv.Atoi(r.Params["id"])
	for _, user := range store.Users() {
		if user.ID == id {
			return user, nil
		}
	}
	return store.User{}, errNotFound
}

// errNotFound becomes app.NotFound() in task 075.
var errNotFound = errorString("user not found")

type errorString string

func (e errorString) Error() string { return string(e) }

// Render keeps user pages as data navigations (docs/APP.md L-7).
const Render = app.RenderData
